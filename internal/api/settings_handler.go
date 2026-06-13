package api

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/druva-06/recruitingest-backend/internal/models"
	"github.com/druva-06/recruitingest-backend/internal/repository"
)

func NewAISettingsHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := SessionFromContext(r.Context())
		if session == nil {
			writeJSONError(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		email := session.Email

		if r.Method == http.MethodGet {
			settings, err := repository.GetUserAISettings(r.Context(), db, email)
			if err != nil {
				writeJSONError(w, http.StatusInternalServerError, "Internal server error")
				return
			}
			if settings == nil {
				// Return default if none exists
				settings = &models.UserAISettings{
					UserEmail:                email,
					RateLimitRequests:        15,
					RateLimitIntervalSeconds: 60,
				}
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(settings)
			return
		}

		if r.Method == http.MethodPost {
			var settings models.UserAISettings
			if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
				writeJSONError(w, http.StatusBadRequest, "Invalid JSON")
				return
			}

			settings.UserEmail = email // Enforce updating own settings
			if settings.RateLimitRequests == 0 {
				settings.RateLimitRequests = 15
			}
			if settings.RateLimitIntervalSeconds == 0 {
				settings.RateLimitIntervalSeconds = 60
			}

			if err := repository.UpsertUserAISettings(r.Context(), db, &settings); err != nil {
				writeJSONError(w, http.StatusInternalServerError, "Internal server error")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "success"})
			return
		}

		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}
