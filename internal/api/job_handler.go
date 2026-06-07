package api

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/druva-06/recruitingest-backend/internal/repository"
)

// NewJobStatusHandler returns an http.HandlerFunc to poll the background job progress.
func NewJobStatusHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Only allow GET requests
		if r.Method != http.MethodGet {
			writeJSONError(w, http.StatusMethodNotAllowed, "Only GET method is allowed")
			return
		}

		// Retrieve the job_id from the URL path (requires Go 1.22+)
		jobID := r.PathValue("job_id")
		if jobID == "" {
			writeJSONError(w, http.StatusBadRequest, "Missing job_id in URL")
			return
		}

		// Query the database
		job, err := repository.GetJobByID(r.Context(), db, jobID)
		if err != nil {
			if err == sql.ErrNoRows {
				writeJSONError(w, http.StatusNotFound, "Job not found")
				return
			}
			writeJSONError(w, http.StatusInternalServerError, "Failed to retrieve job status")
			return
		}

		// Return the current status
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(job)
	}
}
