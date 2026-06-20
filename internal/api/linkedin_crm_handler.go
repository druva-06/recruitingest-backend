package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

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

// NewGetJobPostingsHandler fetches job postings for the dropdown, with optional search and limit
func NewGetJobPostingsHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSONError(w, http.StatusMethodNotAllowed, "Only GET is allowed")
			return
		}
		session := SessionFromContext(r.Context())

		query := r.URL.Query().Get("q")
		limitStr := r.URL.Query().Get("limit")
		limit := 0
		if limitStr != "" {
			if l, err := strconv.Atoi(limitStr); err == nil {
				limit = l
			}
		}

		postings, err := repository.GetJobPostings(r.Context(), db, session.Email, query, limit)
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

// NewGetProfileReferralsHandler fetches all referrals for a specific LinkedIn profile URL.
// Query param: ?url=<linkedin_profile_url>
// This is used by the extension to check if a profile already exists in the CRM.
func NewGetProfileReferralsHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSONError(w, http.StatusMethodNotAllowed, "Only GET is allowed")
			return
		}
		session := SessionFromContext(r.Context())

		profileURL := r.URL.Query().Get("url")
		if profileURL == "" {
			writeJSONError(w, http.StatusBadRequest, "url parameter is required")
			return
		}

		referrals, err := repository.GetProfileReferralsByURL(r.Context(), db, session.Email, profileURL)
		if err != nil {
			slog.Error("Failed to get profile referrals", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "Failed to fetch profile referrals")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"referrals": referrals,
		})
	}
}

// NewLogLinkedInOutreachHandler logs a LinkedIn profile against a job posting
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
			"status":              "Logged",
		})
	}
}

// NewBatchUpdateConnectionStatusHandler marks visible LinkedIn connections as Connected
// Called from the connections page to sync who has accepted connection requests
func NewBatchUpdateConnectionStatusHandler(db *sql.DB) http.HandlerFunc {
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

		affected, err := repository.UpdateProfileConnectionStatus(r.Context(), db, session.Email, req.LinkedInURLs, "Connected")
		if err != nil {
			slog.Error("Failed to batch update connection statuses", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "Failed to batch update")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"updated_count": affected,
		})
	}
}

// NewUpdateReferralStatusHandler updates the workflow status of a specific referral request
// Valid statuses: Logged, Messaged, Referred, Follow-Up
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

		// Validate status values
		validStatuses := map[string]bool{"Logged": true, "Messaged": true, "Referred": true, "Follow-Up": true}
		if !validStatuses[req.Status] {
			writeJSONError(w, http.StatusBadRequest, "Invalid status. Valid values: Logged, Messaged, Referred, Follow-Up")
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
