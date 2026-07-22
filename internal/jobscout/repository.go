package jobscout

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

func UpsertConfig(ctx context.Context, db *sql.DB, config *JobScoutConfig) error {
	q := `
		INSERT INTO job_scout_config (
			user_email, apify_api_key, apify_actor_id, gemini_api_key, gemini_model,
			default_scrape_limit, scrape_company, score_rate_mode, score_rate_value,
			score_interval_seconds, top_n_results, user_skills_experience, scoring_prompt, default_linkedin_url
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			apify_api_key = IF(VALUES(apify_api_key) = '' OR VALUES(apify_api_key) IS NULL, apify_api_key, VALUES(apify_api_key)),
			apify_actor_id = VALUES(apify_actor_id),
			gemini_api_key = IF(VALUES(gemini_api_key) = '' OR VALUES(gemini_api_key) IS NULL, gemini_api_key, VALUES(gemini_api_key)),
			gemini_model = VALUES(gemini_model),
			default_scrape_limit = VALUES(default_scrape_limit),
			scrape_company = VALUES(scrape_company),
			score_rate_mode = VALUES(score_rate_mode),
			score_rate_value = VALUES(score_rate_value),
			score_interval_seconds = VALUES(score_interval_seconds),
			top_n_results = VALUES(top_n_results),
			user_skills_experience = VALUES(user_skills_experience),
			scoring_prompt = VALUES(scoring_prompt),
			default_linkedin_url = VALUES(default_linkedin_url),
			updated_at = CURRENT_TIMESTAMP
	`
	_, err := db.ExecContext(ctx, q,
		config.UserEmail, config.ApifyAPIKey, config.ApifyActorID, config.GeminiAPIKey, config.GeminiModel,
		config.DefaultScrapeLimit, config.ScrapeCompany, config.ScoreRateMode, config.ScoreRateValue,
		config.ScoreIntervalSeconds, config.TopNResults, config.UserSkillsExperience, config.ScoringPrompt, config.DefaultLinkedInURL,
	)
	return err
}

func GetConfig(ctx context.Context, db *sql.DB, email string) (*JobScoutConfig, error) {
	q := `SELECT id, user_email, apify_api_key, apify_actor_id, gemini_api_key, gemini_model,
		default_scrape_limit, scrape_company, score_rate_mode, score_rate_value,
		score_interval_seconds, top_n_results, user_skills_experience, scoring_prompt, default_linkedin_url, created_at, updated_at
		FROM job_scout_config WHERE user_email = ?`

	var c JobScoutConfig
	err := db.QueryRowContext(ctx, q, email).Scan(
		&c.ID, &c.UserEmail, &c.ApifyAPIKey, &c.ApifyActorID, &c.GeminiAPIKey, &c.GeminiModel,
		&c.DefaultScrapeLimit, &c.ScrapeCompany, &c.ScoreRateMode, &c.ScoreRateValue,
		&c.ScoreIntervalSeconds, &c.TopNResults, &c.UserSkillsExperience, &c.ScoringPrompt, &c.DefaultLinkedInURL, &c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func CreateRun(ctx context.Context, db *sql.DB, run *JobScoutRun) error {
	q := `INSERT INTO job_scout_runs (user_email, linkedin_url, scrape_limit) VALUES (?, ?, ?)`
	res, err := db.ExecContext(ctx, q, run.UserEmail, run.LinkedInURL, run.ScrapeLimit)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	run.ID = int(id)
	run.Status = "pending"
	return nil
}

func UpdateRun(ctx context.Context, db *sql.DB, run *JobScoutRun) error {
	q := `UPDATE job_scout_runs SET
		apify_run_id = ?, status = ?, total_scraped = ?, total_scored = ?,
		error_message = ?, started_at = ?, finished_at = ?
		WHERE id = ?`
	_, err := db.ExecContext(ctx, q,
		run.ApifyRunID, run.Status, run.TotalScraped, run.TotalScored,
		run.ErrorMessage, run.StartedAt, run.FinishedAt, run.ID,
	)
	return err
}

func GetRun(ctx context.Context, db *sql.DB, id int, email string) (*JobScoutRun, error) {
	q := `SELECT id, user_email, apify_run_id, linkedin_url, scrape_limit, status,
		total_scraped, total_scored, error_message, created_at, started_at, finished_at
		FROM job_scout_runs WHERE id = ? AND user_email = ?`

	var r JobScoutRun
	err := db.QueryRowContext(ctx, q, id, email).Scan(
		&r.ID, &r.UserEmail, &r.ApifyRunID, &r.LinkedInURL, &r.ScrapeLimit, &r.Status,
		&r.TotalScraped, &r.TotalScored, &r.ErrorMessage, &r.CreatedAt, &r.StartedAt, &r.FinishedAt,
	)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func GetRuns(ctx context.Context, db *sql.DB, email string) ([]JobScoutRun, error) {
	q := `SELECT id, user_email, apify_run_id, linkedin_url, scrape_limit, status,
		total_scraped, total_scored, error_message, created_at, started_at, finished_at
		FROM job_scout_runs WHERE user_email = ? ORDER BY created_at DESC`

	rows, err := db.QueryContext(ctx, q, email)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []JobScoutRun
	for rows.Next() {
		var r JobScoutRun
		if err := rows.Scan(
			&r.ID, &r.UserEmail, &r.ApifyRunID, &r.LinkedInURL, &r.ScrapeLimit, &r.Status,
			&r.TotalScraped, &r.TotalScored, &r.ErrorMessage, &r.CreatedAt, &r.StartedAt, &r.FinishedAt,
		); err != nil {
			return nil, err
		}
		runs = append(runs, r)
	}
	return runs, nil
}

// RescoreRun resets all jobs in a run back to "pending" scoring status
// and puts the run back into "scoring" state so the worker picks it up again.
func RescoreRun(ctx context.Context, db *sql.DB, runID int, email string) error {
	// Reset all job scores for this run
	_, err := db.ExecContext(ctx,
		`UPDATE job_scout_jobs
		 SET score_status = 'pending', fit_score = NULL, fit_reasoning = NULL,
		     matching_skills = NULL, missing_skills = NULL, scored_at = NULL
		 WHERE run_id = ? AND user_email = ?`,
		runID, email,
	)
	if err != nil {
		return fmt.Errorf("failed to reset job scores: %w", err)
	}

	// Reset the run counters and status back to "scoring"
	_, err = db.ExecContext(ctx,
		`UPDATE job_scout_runs
		 SET status = 'scoring', total_scored = 0, error_message = NULL, finished_at = NULL
		 WHERE id = ? AND user_email = ?`,
		runID, email,
	)
	if err != nil {
		return fmt.Errorf("failed to reset run status: %w", err)
	}

	return nil
}

func BulkInsertJobs(ctx context.Context, db *sql.DB, jobs []JobScoutJob) error {
	if len(jobs) == 0 {
		return nil
	}

	q := `INSERT INTO job_scout_jobs (
		run_id, user_email, title, company, location, description,
		linkedin_url, salary, job_type, experience_level, posted_at,
		applicant_count, company_url, company_logo
	) VALUES `

	var args []interface{}
	for i, j := range jobs {
		if i > 0 {
			q += ", "
		}
		q += "(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"
		args = append(args, j.RunID, j.UserEmail, j.Title, j.Company, j.Location, j.Description,
			j.LinkedInURL, j.Salary, j.JobType, j.ExperienceLevel, j.PostedAt,
			j.ApplicantCount, j.CompanyURL, j.CompanyLogo)
	}

	_, err := db.ExecContext(ctx, q, args...)
	return err
}

func GetUnscoredJobs(ctx context.Context, db *sql.DB, runID int) ([]JobScoutJob, error) {
	q := `SELECT id, run_id, user_email, title, company, location, description,
		linkedin_url, salary, job_type, experience_level, posted_at, applicant_count,
		company_url, company_logo, score_status
		FROM job_scout_jobs
		WHERE run_id = ? AND score_status = 'pending'
		ORDER BY id ASC`

	rows, err := db.QueryContext(ctx, q, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []JobScoutJob
	for rows.Next() {
		var j JobScoutJob
		if err := rows.Scan(
			&j.ID, &j.RunID, &j.UserEmail, &j.Title, &j.Company, &j.Location, &j.Description,
			&j.LinkedInURL, &j.Salary, &j.JobType, &j.ExperienceLevel, &j.PostedAt, &j.ApplicantCount,
			&j.CompanyURL, &j.CompanyLogo, &j.ScoreStatus,
		); err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, nil
}

func UpdateJobScore(ctx context.Context, db *sql.DB, jobID int, status string, score *FitScoreResult) error {
	var fitScore *float64
	var reasoning, matching, missing *string
	now := time.Now()
	var scoredAt *time.Time

	if score != nil {
		fitScore = &score.FitScore
		reasoning = &score.Reasoning
		
		bMatch, _ := json.Marshal(score.MatchingSkills)
		sMatch := string(bMatch)
		matching = &sMatch
		
		bMiss, _ := json.Marshal(score.MissingSkills)
		sMiss := string(bMiss)
		missing = &sMiss
		scoredAt = &now
	}

	q := `UPDATE job_scout_jobs SET
		score_status = ?, fit_score = ?, fit_reasoning = ?,
		matching_skills = ?, missing_skills = ?, scored_at = ?
		WHERE id = ?`
	_, err := db.ExecContext(ctx, q,
		status, fitScore, reasoning, matching, missing, scoredAt, jobID,
	)
	return err
}

func GetTopScoredJobs(ctx context.Context, db *sql.DB, runID int, limit int) ([]JobScoutJob, error) {
	q := `SELECT id, run_id, user_email, title, company, location, description,
		linkedin_url, salary, job_type, experience_level, posted_at, applicant_count,
		company_url, company_logo, score_status, fit_score, fit_reasoning,
		matching_skills, missing_skills, scored_at, created_at
		FROM job_scout_jobs
		WHERE run_id = ?
		ORDER BY (CASE WHEN fit_score IS NOT NULL THEN 0 ELSE 1 END), fit_score DESC, id ASC`

	var rows *sql.Rows
	var err error
	if limit > 0 {
		q += " LIMIT ?"
		rows, err = db.QueryContext(ctx, q, runID, limit)
	} else {
		rows, err = db.QueryContext(ctx, q, runID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []JobScoutJob
	rank := 1
	for rows.Next() {
		var j JobScoutJob
		if err := rows.Scan(
			&j.ID, &j.RunID, &j.UserEmail, &j.Title, &j.Company, &j.Location, &j.Description,
			&j.LinkedInURL, &j.Salary, &j.JobType, &j.ExperienceLevel, &j.PostedAt, &j.ApplicantCount,
			&j.CompanyURL, &j.CompanyLogo, &j.ScoreStatus, &j.FitScore, &j.FitReasoning,
			&j.MatchingSkills, &j.MissingSkills, &j.ScoredAt, &j.CreatedAt,
		); err != nil {
			return nil, err
		}
		j.Rank = rank
		rank++
		jobs = append(jobs, j)
	}
	return jobs, nil
}

func DeleteRun(ctx context.Context, db *sql.DB, id int, email string) error {
	// Let's delete the jobs first, then the run
	_, err := db.ExecContext(ctx, "DELETE FROM job_scout_jobs WHERE run_id = ? AND user_email = ?", id, email)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, "DELETE FROM job_scout_runs WHERE id = ? AND user_email = ?", id, email)
	return err
}

// Added this to support worker querying
func GetRunsByStatus(ctx context.Context, db *sql.DB, status string) ([]JobScoutRun, error) {
	q := `SELECT id, user_email, apify_run_id, linkedin_url, scrape_limit, status,
		total_scraped, total_scored, error_message, created_at, started_at, finished_at
		FROM job_scout_runs WHERE status = ?`

	rows, err := db.QueryContext(ctx, q, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []JobScoutRun
	for rows.Next() {
		var r JobScoutRun
		if err := rows.Scan(
			&r.ID, &r.UserEmail, &r.ApifyRunID, &r.LinkedInURL, &r.ScrapeLimit, &r.Status,
			&r.TotalScraped, &r.TotalScored, &r.ErrorMessage, &r.CreatedAt, &r.StartedAt, &r.FinishedAt,
		); err != nil {
			return nil, err
		}
		runs = append(runs, r)
	}
	return runs, nil
}

// Helper to get user resume - since it's in internal/repository/resume.go, we might need a local copy or to import it. 
// For now, I'll just declare it here as a local DB call to maintain isolation matrix rule.
func GetUserResumeText(ctx context.Context, db *sql.DB, email string) (string, error) {
	q := `SELECT resume_text FROM user_resumes WHERE user_email = ?`
	var text string
	err := db.QueryRowContext(ctx, q, email).Scan(&text)
	if err != nil {
		return "", err
	}
	return text, nil
}
