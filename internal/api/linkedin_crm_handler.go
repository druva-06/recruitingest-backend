package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/druva-06/recruitingest-backend/internal/repository"
)

// NewCreateJobPostingHandler handles creating a new job role tracker
func NewCreateJobPostingHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSONError(w, http.StatusMethodNotAllowed, "Only POST is allowed")
			return
		}
		session := SessionFromContext(r.Context())

		var req struct {
			CompanyName string `json:"company_name"`
			RoleTitle   string `json:"role_title"`
			JobURL      string `json:"job_url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "Invalid request body")
			return
		}

		if req.CompanyName == "" || req.RoleTitle == "" {
			writeJSONError(w, http.StatusBadRequest, "Company name and role title are required")
			return
		}

		id, err := repository.CreateJobPosting(r.Context(), db, session.Email, req.CompanyName, req.RoleTitle, req.JobURL)
		if err != nil {
			slog.Error("Failed to create job posting", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "Failed to save job posting")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id": id,
		})
	}
}

// NewGetJobPostingsHandler fetches all active job postings for the dropdown
func NewGetJobPostingsHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSONError(w, http.StatusMethodNotAllowed, "Only GET is allowed")
			return
		}
		session := SessionFromContext(r.Context())

		postings, err := repository.GetJobPostings(r.Context(), db, session.Email)
		if err != nil {
			slog.Error("Failed to get job postings", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "Failed to retrieve job postings")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"job_postings": postings,
		})
	}
}

// NewLogLinkedInOutreachHandler logs a profile to a job
func NewLogLinkedInOutreachHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSONError(w, http.StatusMethodNotAllowed, "Only POST is allowed")
			return
		}
		session := SessionFromContext(r.Context())

		var req struct {
			JobPostingID   int    `json:"job_posting_id"`
			LinkedInURL    string `json:"linkedin_url"`
			ProfileName    string `json:"profile_name"`
			CurrentCompany string `json:"current_company"`
			CurrentRole    string `json:"current_role"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "Invalid request body")
			return
		}

		if req.JobPostingID == 0 || req.LinkedInURL == "" {
			writeJSONError(w, http.StatusBadRequest, "Job posting ID and LinkedIn URL are required")
			return
		}

		requestID, err := repository.LogOutreach(r.Context(), db, session.Email, req.JobPostingID, req.LinkedInURL, req.ProfileName, req.CurrentCompany, req.CurrentRole)
		if err != nil {
			slog.Error("Failed to log linkedin outreach", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "Failed to save profile request")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"referral_request_id": requestID,
			"status":              "Pending",
		})
	}
}

// NewBatchUpdateReferralHandler handles updating profiles from connections page
func NewBatchUpdateReferralHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			writeJSONError(w, http.StatusMethodNotAllowed, "Only PATCH is allowed")
			return
		}
		session := SessionFromContext(r.Context())

		var req struct {
			LinkedInURLs []string `json:"linkedin_urls"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "Invalid request body")
			return
		}

		affected, err := repository.BatchUpdateReferralStatusByURL(r.Context(), db, session.Email, req.LinkedInURLs, "Pending", "Accepted")
		if err != nil {
			slog.Error("Failed to batch update statuses", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "Failed to batch update")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"updated_count": affected,
		})
	}
}

// NewUpdateReferralStatusHandler handles updating a specific request
func NewUpdateReferralStatusHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			writeJSONError(w, http.StatusMethodNotAllowed, "Only PATCH is allowed")
			return
		}
		session := SessionFromContext(r.Context())

		var req struct {
			ReferralRequestID int    `json:"referral_request_id"`
			Status            string `json:"status"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "Invalid request body")
			return
		}

		if req.ReferralRequestID == 0 || req.Status == "" {
			writeJSONError(w, http.StatusBadRequest, "Referral request ID and status are required")
			return
		}

		err := repository.UpdateReferralStatus(r.Context(), db, req.ReferralRequestID, session.Email, req.Status)
		if err != nil {
			slog.Error("Failed to update referral status", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "Failed to update status")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
		})
	}
}

// NewGetDashboardReferralsHandler fetches data for the CRM dashboard
func NewGetDashboardReferralsHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSONError(w, http.StatusMethodNotAllowed, "Only GET is allowed")
			return
		}
		session := SessionFromContext(r.Context())

		referrals, err := repository.GetDashboardReferrals(r.Context(), db, session.Email)
		if err != nil {
			slog.Error("Failed to get dashboard referrals", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "Failed to fetch dashboard data")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"referrals": referrals,
		})
	}
}

// NewDeleteReferralHandler handles deleting a referral request
func NewDeleteReferralHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			writeJSONError(w, http.StatusMethodNotAllowed, "Only DELETE is allowed")
			return
		}
		
		idStr := r.PathValue("id")
		if idStr == "" {
			writeJSONError(w, http.StatusBadRequest, "Missing referral ID")
			return
		}

		var id int
		if _, err := fmt.Sscanf(idStr, "%d", &id); err != nil {
			writeJSONError(w, http.StatusBadRequest, "Invalid referral ID")
			return
		}

		session := SessionFromContext(r.Context())

		if err := repository.DeleteReferralRequest(r.Context(), db, id, session.Email); err != nil {
			slog.Error("Failed to delete referral request", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "Failed to delete referral request")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
		})
	}
}
