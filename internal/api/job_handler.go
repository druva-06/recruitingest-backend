package api

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/druva-06/recruitingest-backend/internal/repository"
)

// NewJobStatusHandler returns an http.HandlerFunc to poll the background AI job progress.
func NewJobStatusHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slog.Info("Executing NewJobStatusHandler")
		if r.Method != http.MethodGet {
			slog.Warn("Method not allowed", "method", r.Method)
			writeJSONError(w, http.StatusMethodNotAllowed, "Only GET method is allowed")
			return
		}

		session := SessionFromContext(r.Context())
		if session == nil {
			slog.Warn("Unauthorized access attempt")
			writeJSONError(w, http.StatusUnauthorized, "Not authenticated")
			return
		}
		slog.Debug("Authenticated session found", "email", session.Email)

		jobIDStr := r.PathValue("job_id")
		if jobIDStr == "" {
			slog.Warn("Validation failed: missing job_id")
			writeJSONError(w, http.StatusBadRequest, "Missing job_id in URL")
			return
		}

		jobID, err := strconv.ParseInt(jobIDStr, 10, 64)
		if err != nil {
			slog.Warn("Validation failed: invalid job_id format", "job_id", jobIDStr)
			writeJSONError(w, http.StatusBadRequest, "Invalid job ID format")
			return
		}
		slog.Debug("Parsed job_id successfully", "job_id", jobID)

		job, err := repository.GetAIJobByID(r.Context(), db, jobID)
		if err != nil {
			slog.Error("Database query for job failed", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "Failed to retrieve job status")
			return
		}
		if job == nil || job.UserEmail != session.Email {
			slog.Warn("Job not found or access denied", "job_id", jobID, "user_email", session.Email)
			writeJSONError(w, http.StatusNotFound, "Job not found")
			return
		}
		slog.Info("Successfully retrieved job status", "job_id", jobID, "status", job.Status)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(job)
	}
}

// NewRecentJobsHandler returns an http.HandlerFunc to fetch the latest 3 jobs for the user.
func NewRecentJobsHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slog.Info("Executing NewRecentJobsHandler")
		if r.Method != http.MethodGet {
			slog.Warn("Method not allowed", "method", r.Method)
			writeJSONError(w, http.StatusMethodNotAllowed, "Only GET method is allowed")
			return
		}

		session := SessionFromContext(r.Context())
		if session == nil {
			slog.Warn("Unauthorized access attempt")
			writeJSONError(w, http.StatusUnauthorized, "Not authenticated")
			return
		}
		slog.Debug("Authenticated session found", "email", session.Email)

		jobs, err := repository.GetRecentJobs(r.Context(), db, session.Email)
		if err != nil {
			slog.Error("Database query for recent jobs failed", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "Failed to retrieve recent jobs")
			return
		}
		slog.Info("Successfully retrieved recent jobs", "count", len(jobs))

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(jobs)
	}
}
