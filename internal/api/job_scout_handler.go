package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/druva-06/recruitingest-backend/internal/jobscout"
)

func NewJobScoutConfigHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := SessionFromContext(r.Context())
		if session == nil {
			writeJSONError(w, http.StatusUnauthorized, "Unauthorized")
			return
		}

		if r.Method == http.MethodGet {
			slog.Info("jobscout api: GET config", "user_email", session.Email)
			config, err := jobscout.GetConfig(r.Context(), db, session.Email)
			if err != nil {
				if err == sql.ErrNoRows {
					slog.Info("jobscout api: no config found for user", "user_email", session.Email)
					writeJSON(w, http.StatusOK, map[string]interface{}{"status": "not_found"})
					return
				}
				slog.Error("jobscout api: failed to get config", "user_email", session.Email, "error", err)
				writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to get config: %v", err))
				return
			}
			slog.Info("jobscout api: config fetched successfully", "user_email", session.Email)
			writeJSON(w, http.StatusOK, config)
			return
		}

		if r.Method == http.MethodPut {
			slog.Info("jobscout api: PUT config", "user_email", session.Email)
			var config jobscout.JobScoutConfig
			if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
				slog.Warn("jobscout api: invalid config payload", "user_email", session.Email, "error", err)
				writeJSONError(w, http.StatusBadRequest, "Invalid request payload")
				return
			}
			config.UserEmail = session.Email

			if err := jobscout.UpsertConfig(r.Context(), db, &config); err != nil {
				slog.Error("jobscout api: failed to save config", "user_email", session.Email, "error", err)
				writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to save config: %v", err))
				return
			}

			slog.Info("jobscout api: config saved successfully",
				"user_email", session.Email,
				"default_scrape_limit", config.DefaultScrapeLimit,
				"gemini_model", config.GeminiModel,
				"score_rate_mode", config.ScoreRateMode,
				"score_rate_value", config.ScoreRateValue,
				"has_skills", len(config.UserSkillsExperience) > 0,
				"has_custom_prompt", len(config.ScoringPrompt) > 0,
				"has_default_linkedin_url", len(config.DefaultLinkedInURL) > 0,
			)
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"status": "saved",
				"config": config,
			})
			return
		}

		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func NewJobScoutStartHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := SessionFromContext(r.Context())
		if session == nil {
			writeJSONError(w, http.StatusUnauthorized, "Unauthorized")
			return
		}

		var req struct {
			LinkedInURL string `json:"linkedin_url"`
			ScrapeLimit int    `json:"scrape_limit"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			slog.Warn("jobscout api: invalid start run payload", "user_email", session.Email, "error", err)
			writeJSONError(w, http.StatusBadRequest, "Invalid request payload")
			return
		}

		slog.Info("jobscout api: received start run request",
			"user_email", session.Email,
			"requested_url", req.LinkedInURL,
			"requested_limit", req.ScrapeLimit,
		)

		config, err := jobscout.GetConfig(r.Context(), db, session.Email)
		hasConfig := err == nil
		if err != nil {
			slog.Warn("jobscout api: could not load config for start run — will use request values only",
				"user_email", session.Email,
				"error", err,
			)
		}

		url := req.LinkedInURL
		if url == "" && hasConfig && config.DefaultLinkedInURL != "" {
			slog.Info("jobscout api: no URL provided, using default from config",
				"user_email", session.Email,
				"default_url", config.DefaultLinkedInURL,
			)
			url = config.DefaultLinkedInURL
		}
		if url == "" {
			slog.Warn("jobscout api: LinkedIn URL missing and no default configured", "user_email", session.Email)
			writeJSONError(w, http.StatusBadRequest, "LinkedIn URL is required")
			return
		}

		limit := req.ScrapeLimit
		if limit <= 0 {
			if hasConfig && config.DefaultScrapeLimit > 0 {
				slog.Info("jobscout api: using default scrape limit from config",
					"user_email", session.Email,
					"default_scrape_limit", config.DefaultScrapeLimit,
				)
				limit = config.DefaultScrapeLimit
			} else {
				slog.Info("jobscout api: no scrape limit configured, falling back to 25", "user_email", session.Email)
				limit = 25
			}
		}
		if limit < 10 {
			slog.Warn("jobscout api: scrape limit below Apify minimum of 10",
				"user_email", session.Email,
				"requested_limit", limit,
			)
			writeJSONError(w, http.StatusBadRequest, "Scrape limit must be at least 10")
			return
		}

		run := jobscout.JobScoutRun{
			UserEmail:   session.Email,
			LinkedInURL: url,
			ScrapeLimit: limit,
		}

		if err := jobscout.CreateRun(r.Context(), db, &run); err != nil {
			slog.Error("jobscout api: failed to create run record in DB",
				"user_email", session.Email,
				"linkedin_url", url,
				"scrape_limit", limit,
				"error", err,
			)
			writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to create run: %v", err))
			return
		}

		slog.Info("jobscout api: run created and queued for scraping",
			"run_id", run.ID,
			"user_email", session.Email,
			"linkedin_url", url,
			"scrape_limit", limit,
		)

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":  "started",
			"run_id":  run.ID,
			"message": "Scraping started",
		})
	}
}

func NewJobScoutRunsHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := SessionFromContext(r.Context())
		if session == nil {
			writeJSONError(w, http.StatusUnauthorized, "Unauthorized")
			return
		}

		slog.Info("jobscout api: GET runs list", "user_email", session.Email)
		runs, err := jobscout.GetRuns(r.Context(), db, session.Email)
		if err != nil {
			slog.Error("jobscout api: failed to fetch runs", "user_email", session.Email, "error", err)
			writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to fetch runs: %v", err))
			return
		}

		slog.Info("jobscout api: runs fetched", "user_email", session.Email, "run_count", len(runs))
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"runs": runs,
		})
	}
}

func NewJobScoutRunDetailHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := SessionFromContext(r.Context())
		if session == nil {
			writeJSONError(w, http.StatusUnauthorized, "Unauthorized")
			return
		}

		idStr := r.PathValue("id")
		id, err := strconv.Atoi(idStr)
		if err != nil {
			slog.Warn("jobscout api: invalid run ID in path", "id_str", idStr, "error", err)
			writeJSONError(w, http.StatusBadRequest, "Invalid run ID")
			return
		}

		slog.Info("jobscout api: GET run detail", "run_id", id, "user_email", session.Email)

		run, err := jobscout.GetRun(r.Context(), db, id, session.Email)
		if err != nil {
			if err == sql.ErrNoRows {
				slog.Warn("jobscout api: run not found", "run_id", id, "user_email", session.Email)
				writeJSONError(w, http.StatusNotFound, "Run not found")
				return
			}
			slog.Error("jobscout api: failed to get run", "run_id", id, "user_email", session.Email, "error", err)
			writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to get run: %v", err))
			return
		}

		topN := 0 // 0 means return ALL jobs for the run
		if limitParam := r.URL.Query().Get("limit"); limitParam != "" {
			if parsed, err := strconv.Atoi(limitParam); err == nil && parsed > 0 {
				topN = parsed
			}
		}

		slog.Info("jobscout api: fetching scored jobs",
			"run_id", id,
			"top_n", topN,
			"run_status", run.Status,
			"total_scraped", run.TotalScraped,
			"total_scored", run.TotalScored,
		)

		topJobs, err := jobscout.GetTopScoredJobs(r.Context(), db, id, topN)
		if err != nil {
			slog.Error("jobscout api: failed to get top scored jobs", "run_id", id, "error", err)
			writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to get top jobs: %v", err))
			return
		}

		percent := 0
		if run.TotalScraped > 0 {
			percent = (run.TotalScored * 100) / run.TotalScraped
		} else if run.Status == "completed" {
			percent = 100
		}

		slog.Info("jobscout api: run detail response prepared",
			"run_id", id,
			"percent_scored", percent,
			"top_jobs_count", len(topJobs),
		)

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"run": run,
			"progress": map[string]interface{}{
				"percent":          percent,
				"scored":           run.TotalScored,
				"total":            run.TotalScraped,
				"rate_description": "Rate limited",
			},
			"top_jobs":       topJobs,
			"all_jobs_count": run.TotalScraped,
		})
	}
}

func NewJobScoutDeleteRunHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := SessionFromContext(r.Context())
		if session == nil {
			writeJSONError(w, http.StatusUnauthorized, "Unauthorized")
			return
		}

		idStr := r.PathValue("id")
		id, err := strconv.Atoi(idStr)
		if err != nil {
			slog.Warn("jobscout api: invalid run ID for delete", "id_str", idStr, "error", err)
			writeJSONError(w, http.StatusBadRequest, "Invalid run ID")
			return
		}

		slog.Info("jobscout api: DELETE run", "run_id", id, "user_email", session.Email)

		if err := jobscout.DeleteRun(r.Context(), db, id, session.Email); err != nil {
			slog.Error("jobscout api: failed to delete run", "run_id", id, "user_email", session.Email, "error", err)
			writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to delete run: %v", err))
			return
		}

		slog.Info("jobscout api: run deleted successfully", "run_id", id, "user_email", session.Email)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status": "deleted",
		})
	}
}

func NewJobScoutRescoreHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := SessionFromContext(r.Context())
		if session == nil {
			writeJSONError(w, http.StatusUnauthorized, "Unauthorized")
			return
		}

		idStr := r.PathValue("id")
		id, err := strconv.Atoi(idStr)
		if err != nil {
			slog.Warn("jobscout api: invalid run ID for rescore", "id_str", idStr, "error", err)
			writeJSONError(w, http.StatusBadRequest, "Invalid run ID")
			return
		}

		slog.Info("jobscout api: RESCORE run — resetting all job scores", "run_id", id, "user_email", session.Email)

		if err := jobscout.RescoreRun(r.Context(), db, id, session.Email); err != nil {
			slog.Error("jobscout api: failed to rescore run", "run_id", id, "user_email", session.Email, "error", err)
			writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to rescore run: %v", err))
			return
		}

		slog.Info("jobscout api: run queued for re-scoring", "run_id", id, "user_email", session.Email)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":  "rescoring",
			"message": "All jobs have been reset and queued for re-scoring.",
		})
	}
}
