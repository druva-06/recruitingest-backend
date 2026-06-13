package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/druva-06/recruitingest-backend/internal/repository"
)

// NewJobStatusHandler returns an http.HandlerFunc to poll the background AI job progress.
func NewJobStatusHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSONError(w, http.StatusMethodNotAllowed, "Only GET method is allowed")
			return
		}

		session := SessionFromContext(r.Context())
		if session == nil {
			writeJSONError(w, http.StatusUnauthorized, "Not authenticated")
			return
		}

		jobIDStr := r.PathValue("job_id")
		if jobIDStr == "" {
			writeJSONError(w, http.StatusBadRequest, "Missing job_id in URL")
			return
		}

		jobID, err := strconv.ParseInt(jobIDStr, 10, 64)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "Invalid job ID format")
			return
		}

		job, err := repository.GetAIJobByID(r.Context(), db, jobID)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "Failed to retrieve job status")
			return
		}
		if job == nil || job.UserEmail != session.Email {
			writeJSONError(w, http.StatusNotFound, "Job not found")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(job)
	}
}

// NewRecentJobsHandler returns an http.HandlerFunc to fetch the latest 3 jobs for the user.
func NewRecentJobsHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSONError(w, http.StatusMethodNotAllowed, "Only GET method is allowed")
			return
		}

		session := SessionFromContext(r.Context())
		if session == nil {
			writeJSONError(w, http.StatusUnauthorized, "Not authenticated")
			return
		}

		jobs, err := repository.GetRecentJobs(r.Context(), db, session.Email)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "Failed to retrieve recent jobs")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(jobs)
	}
}
