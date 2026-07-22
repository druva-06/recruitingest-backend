package jobscout

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// activeScoreRuns tracks run IDs that currently have a scoring goroutine running.
// This prevents the 30-second ticker from spawning duplicate goroutines for the same run.
var activeScoreRuns sync.Map

func StartScraperWorker(db *sql.DB) {
	slog.Info("jobscout: starting scraper worker (polls every 10s)")
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				processPendingRuns(context.Background(), db)
			}
		}
	}()
}

func processPendingRuns(ctx context.Context, db *sql.DB) {
	runs, err := GetRunsByStatus(ctx, db, "pending")
	if err != nil {
		slog.Error("jobscout scraper: failed to get pending runs", "error", err)
		return
	}

	if len(runs) == 0 {
		return // nothing to do, skip noisy log
	}

	slog.Info("jobscout scraper: found pending runs to process", "count", len(runs))

	for _, run := range runs {
		slog.Info("jobscout scraper: processing pending run",
			"run_id", run.ID,
			"user_email", run.UserEmail,
			"linkedin_url", run.LinkedInURL,
			"scrape_limit", run.ScrapeLimit,
		)

		config, err := GetConfig(ctx, db, run.UserEmail)
		if err != nil {
			slog.Error("jobscout scraper: failed to get config for user",
				"run_id", run.ID,
				"user_email", run.UserEmail,
				"error", err,
			)
			failRun(ctx, db, &run, fmt.Sprintf("failed to get config: %v", err))
			continue
		}

		if config.ApifyAPIKey == "" {
			slog.Error("jobscout scraper: Apify API key is empty in config",
				"run_id", run.ID,
				"user_email", run.UserEmail,
			)
			failRun(ctx, db, &run, "Apify API key is not configured")
			continue
		}

		client := NewApifyClient(config.ApifyAPIKey)

		now := time.Now()
		run.StartedAt = &now

		limit := run.ScrapeLimit
		if limit <= 0 {
			if config.DefaultScrapeLimit > 0 {
				slog.Info("jobscout scraper: using default scrape limit from config",
					"run_id", run.ID,
					"default_scrape_limit", config.DefaultScrapeLimit,
				)
				limit = config.DefaultScrapeLimit
			} else {
				slog.Info("jobscout scraper: no scrape limit set, falling back to 25", "run_id", run.ID)
				limit = 25
			}
		}
		if limit < 10 {
			slog.Warn("jobscout scraper: scrape limit below Apify minimum, clamping to 10",
				"run_id", run.ID,
				"original_limit", limit,
			)
			limit = 10
		}

		input := ApifyRunInput{
			URLs:          []string{run.LinkedInURL},
			Count:         limit,
			ScrapeCompany: config.ScrapeCompany,
		}

		slog.Info("jobscout scraper: sending run to Apify",
			"run_id", run.ID,
			"actor_id", config.ApifyActorID,
			"linkedin_url", run.LinkedInURL,
			"limit", limit,
		)

		runID, err := client.StartRun(config.ApifyActorID, input)
		if err != nil {
			slog.Error("jobscout scraper: failed to start Apify run",
				"run_id", run.ID,
				"actor_id", config.ApifyActorID,
				"error", err,
			)
			failRun(ctx, db, &run, fmt.Sprintf("failed to start apify run: %v", err))
			continue
		}

		run.ApifyRunID = &runID
		run.Status = "scraping"
		UpdateRun(ctx, db, &run)

		slog.Info("jobscout scraper: Apify run started, polling in background",
			"run_id", run.ID,
			"apify_run_id", runID,
		)
		go pollApifyRun(context.Background(), db, run, config)
	}
}

func pollApifyRun(ctx context.Context, db *sql.DB, run JobScoutRun, config *JobScoutConfig) {
	client := NewApifyClient(config.ApifyAPIKey)
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	slog.Info("jobscout poller: starting poll loop for Apify run",
		"run_id", run.ID,
		"apify_run_id", *run.ApifyRunID,
	)

	for {
		select {
		case <-ctx.Done():
			slog.Warn("jobscout poller: context cancelled, stopping poll", "run_id", run.ID)
			return
		case <-ticker.C:
			status, err := client.GetRunStatus(*run.ApifyRunID)
			if err != nil {
				slog.Error("jobscout poller: failed to poll Apify run status",
					"run_id", run.ID,
					"apify_run_id", *run.ApifyRunID,
					"error", err,
				)
				continue
			}

			slog.Info("jobscout poller: Apify run status",
				"run_id", run.ID,
				"apify_run_id", *run.ApifyRunID,
				"apify_status", status.Data.Status,
			)

			if status.Data.Status == "SUCCEEDED" {
				slog.Info("jobscout poller: Apify run succeeded, fetching dataset items",
					"run_id", run.ID,
					"apify_run_id", *run.ApifyRunID,
					"dataset_id", status.Data.DatasetID,
				)

				items, err := client.GetDatasetItems(status.Data.DatasetID)
				if err != nil {
					slog.Error("jobscout poller: failed to get dataset items",
						"run_id", run.ID,
						"dataset_id", status.Data.DatasetID,
						"error", err,
					)
					failRun(ctx, db, &run, fmt.Sprintf("failed to get dataset items: %v", err))
					return
				}

				slog.Info("jobscout poller: inserting scraped jobs into DB",
					"run_id", run.ID,
					"job_count", len(items),
				)

				var dbJobs []JobScoutJob
				for _, it := range items {
					dbJobs = append(dbJobs, JobScoutJob{
						RunID:           run.ID,
						UserEmail:       run.UserEmail,
						Title:           it.Title,
						Company:         it.Company,
						Location:        it.Location,
						Description:     it.GetDescription(),
						LinkedInURL:     CleanLinkedInJobURL(it.GetURL()),
						Salary:          it.Salary,
						JobType:         it.GetJobType(),
						ExperienceLevel: it.GetExperienceLevel(),
						PostedAt:        it.PostedAt,
						ApplicantCount:  it.ApplicantCount,
						CompanyURL:      it.CompanyURL,
						CompanyLogo:     it.CompanyLogo,
					})
				}

				if err := BulkInsertJobs(ctx, db, dbJobs); err != nil {
					slog.Error("jobscout poller: failed to bulk insert jobs",
						"run_id", run.ID,
						"job_count", len(dbJobs),
						"error", err,
					)
					failRun(ctx, db, &run, fmt.Sprintf("failed to bulk insert jobs: %v", err))
					return
				}

				run.Status = "scoring"
				run.TotalScraped = len(dbJobs)
				UpdateRun(ctx, db, &run)

				slog.Info("jobscout poller: run transitioned to scoring phase",
					"run_id", run.ID,
					"total_scraped", len(dbJobs),
				)
				return

			} else if status.Data.Status == "FAILED" || status.Data.Status == "ABORTED" || status.Data.Status == "TIMED-OUT" {
				slog.Error("jobscout poller: Apify run ended with non-success status",
					"run_id", run.ID,
					"apify_run_id", *run.ApifyRunID,
					"apify_status", status.Data.Status,
				)
				failRun(ctx, db, &run, fmt.Sprintf("apify run failed with status: %s", status.Data.Status))
				return
			}
		}
	}
}

func failRun(ctx context.Context, db *sql.DB, run *JobScoutRun, msg string) {
	slog.Error("jobscout: marking run as failed",
		"run_id", run.ID,
		"user_email", run.UserEmail,
		"reason", msg,
	)
	run.Status = "failed"
	run.ErrorMessage = &msg
	if err := UpdateRun(ctx, db, run); err != nil {
		slog.Error("jobscout: failed to update run status to failed",
			"run_id", run.ID,
			"error", err,
		)
	}
}

func StartScoringWorker(db *sql.DB) {
	slog.Info("jobscout: starting scoring worker (polls every 30s)")
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				processScoringRuns(context.Background(), db)
			}
		}
	}()
}

func processScoringRuns(ctx context.Context, db *sql.DB) {
	runs, err := GetRunsByStatus(ctx, db, "scoring")
	if err != nil {
		slog.Error("jobscout scoring: failed to get scoring runs", "error", err)
		return
	}

	if len(runs) == 0 {
		return // nothing to do, skip noisy log
	}

	slog.Info("jobscout scoring: found runs in scoring phase", "count", len(runs))

	for _, run := range runs {
		// Guard: skip runs that already have an active goroutine to avoid duplicate scorers
		// which would each have their own rate limiter and collectively exceed the Gemini quota.
		if _, alreadyRunning := activeScoreRuns.LoadOrStore(run.ID, true); alreadyRunning {
			slog.Info("jobscout scoring: run already being scored, skipping duplicate", "run_id", run.ID)
			continue
		}
		slog.Info("jobscout scoring: spawning scorer goroutine", "run_id", run.ID)
		go scoreRunJobs(context.Background(), db, run)
	}
}

func scoreRunJobs(ctx context.Context, db *sql.DB, run JobScoutRun) {
	// Always release the active-run slot when this goroutine exits (success or failure)
	defer activeScoreRuns.Delete(run.ID)

	slog.Info("jobscout scoring: starting score job loop", "run_id", run.ID, "user_email", run.UserEmail)

	config, err := GetConfig(ctx, db, run.UserEmail)
	if err != nil {
		slog.Error("jobscout scoring: failed to get config",
			"run_id", run.ID,
			"user_email", run.UserEmail,
			"error", err,
		)
		failRun(ctx, db, &run, fmt.Sprintf("failed to get config: %v", err))
		return
	}

	// Determine candidate info source
	resumeText := config.UserSkillsExperience
	if resumeText != "" {
		slog.Info("jobscout scoring: using UserSkillsExperience from config",
			"run_id", run.ID,
			"skills_char_count", len(resumeText),
		)
	} else {
		slog.Info("jobscout scoring: UserSkillsExperience is empty, falling back to uploaded resume", "run_id", run.ID)
		dbResumeText, err := GetUserResumeText(ctx, db, run.UserEmail)
		if err != nil {
			slog.Warn("jobscout scoring: no resume found in DB either, scoring without candidate info",
				"run_id", run.ID,
				"user_email", run.UserEmail,
				"error", err,
			)
			resumeText = "No resume or skills provided."
		} else {
			slog.Info("jobscout scoring: using uploaded resume text from DB",
				"run_id", run.ID,
				"resume_char_count", len(dbResumeText),
			)
			resumeText = dbResumeText
		}
	}

	scorer, err := NewFitScorer(ctx, config.GeminiAPIKey, config.GeminiModel)
	if err != nil {
		slog.Error("jobscout scoring: failed to initialize Gemini scorer",
			"run_id", run.ID,
			"gemini_model", config.GeminiModel,
			"error", err,
		)
		failRun(ctx, db, &run, fmt.Sprintf("failed to init scorer: %v", err))
		return
	}

	limiter := buildRateLimiter(config)
	ratePerSecond := float64(config.ScoreRateValue) / rateModeInterval(config).Seconds()
	slog.Info("jobscout scoring: rate limiter configured",
		"run_id", run.ID,
		"score_rate_mode", config.ScoreRateMode,
		"score_rate_value", config.ScoreRateValue,
		"score_interval_seconds", config.ScoreIntervalSeconds,
		"effective_rate_per_second", ratePerSecond,
	)

	jobs, err := GetUnscoredJobs(ctx, db, run.ID)
	if err != nil {
		slog.Error("jobscout scoring: failed to get unscored jobs",
			"run_id", run.ID,
			"error", err,
		)
		return
	}

	slog.Info("jobscout scoring: beginning to score jobs",
		"run_id", run.ID,
		"unscored_job_count", len(jobs),
	)

	for i, job := range jobs {
		slog.Info("jobscout scoring: waiting for rate limiter token",
			"run_id", run.ID,
			"job_index", i+1,
			"total_jobs", len(jobs),
			"job_id", job.ID,
		)
		if err := limiter.Wait(ctx); err != nil {
			slog.Error("jobscout scoring: rate limiter wait error — context may have been cancelled",
				"run_id", run.ID,
				"job_id", job.ID,
				"error", err,
			)
			continue
		}

		slog.Info("jobscout scoring: scoring job",
			"run_id", run.ID,
			"job_id", job.ID,
			"job_title", job.Title,
			"company", job.Company,
		)
		UpdateJobScore(ctx, db, job.ID, "scoring", nil)

		// Retry up to 3 times with backoff on 429 quota errors
		var score *FitScoreResult
		var scoreErr error
		for attempt := 1; attempt <= 3; attempt++ {
			score, scoreErr = scorer.ScoreJob(ctx, &job, resumeText, config.ScoringPrompt)
			if scoreErr == nil {
				break
			}
			errStr := scoreErr.Error()
			is429 := len(errStr) > 3 && (errStr[0:3] == "429" || contains(errStr, "429") || contains(errStr, "Quota exceeded") || contains(errStr, "quota"))
			if is429 && attempt < 3 {
				retryWait := time.Duration(35*attempt) * time.Second
				slog.Warn("jobscout scoring: Gemini quota hit, will retry after backoff",
					"run_id", run.ID,
					"job_id", job.ID,
					"attempt", attempt,
					"retry_wait_seconds", retryWait.Seconds(),
					"error", scoreErr,
				)
				time.Sleep(retryWait)
				continue
			}
			break
		}

		if scoreErr != nil {
			slog.Error("jobscout scoring: failed to score job after retries",
				"run_id", run.ID,
				"job_id", job.ID,
				"job_title", job.Title,
				"error", scoreErr,
			)
			errMsg := fmt.Sprintf("AI Scoring Failed: %v", scoreErr)
			UpdateJobScore(ctx, db, job.ID, "failed", &FitScoreResult{Reasoning: errMsg})
		} else {
			slog.Info("jobscout scoring: job scored successfully",
				"run_id", run.ID,
				"job_id", job.ID,
				"job_title", job.Title,
				"fit_score", score.FitScore,
			)
			UpdateJobScore(ctx, db, job.ID, "scored", score)
		}

		// Refresh run to update scored count
		currRun, err := GetRun(ctx, db, run.ID, run.UserEmail)
		if err == nil {
			currRun.TotalScored++
			if updateErr := UpdateRun(ctx, db, currRun); updateErr != nil {
				slog.Warn("jobscout scoring: failed to update run scored count",
					"run_id", run.ID,
					"error", updateErr,
				)
			}
		}
	}

	// Mark run as completed
	currRun, err := GetRun(ctx, db, run.ID, run.UserEmail)
	if err == nil {
		now := time.Now()
		currRun.FinishedAt = &now
		currRun.Status = "completed"
		if updateErr := UpdateRun(ctx, db, currRun); updateErr != nil {
			slog.Error("jobscout scoring: failed to mark run as completed",
				"run_id", run.ID,
				"error", updateErr,
			)
		} else {
			slog.Info("jobscout scoring: run completed",
				"run_id", run.ID,
				"total_scraped", currRun.TotalScraped,
				"total_scored", currRun.TotalScored,
			)
		}
	} else {
		slog.Error("jobscout scoring: failed to fetch run for completion update",
			"run_id", run.ID,
			"error", err,
		)
	}
}

func rateModeInterval(config *JobScoutConfig) time.Duration {
	switch config.ScoreRateMode {
	case "per_minute":
		return time.Minute
	case "per_hour":
		return time.Hour
	case "per_day":
		return 24 * time.Hour
	case "custom_interval":
		return time.Duration(config.ScoreIntervalSeconds) * time.Second
	default:
		return time.Minute
	}
}

func buildRateLimiter(config *JobScoutConfig) *rate.Limiter {
	interval := rateModeInterval(config)
	ratePerSecond := float64(config.ScoreRateValue) / interval.Seconds()
	return rate.NewLimiter(rate.Limit(ratePerSecond), 1) // burst of 1
}

// contains is a simple substring check helper
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
			return false
		}())
}

// CleanLinkedInJobURL normalizes LinkedIn job URLs to standard format:
// https://www.linkedin.com/jobs/view/<slug-or-id>/
func CleanLinkedInJobURL(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	u.Host = "www.linkedin.com"
	u.RawQuery = ""
	u.Fragment = ""

	if !strings.HasSuffix(u.Path, "/") {
		u.Path += "/"
	}
	return u.String()
}
