package api

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/druva-06/recruitingest-backend/internal/models"
	"github.com/druva-06/recruitingest-backend/internal/repository"
)

func NewAISettingsHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slog.Info("Executing NewAISettingsHandler")
		session := SessionFromContext(r.Context())
		if session == nil {
			slog.Warn("Unauthorized access attempt")
			writeJSONError(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		email := session.Email
		slog.Debug("Authenticated session found", "email", email)

		if r.Method == http.MethodGet {
			slog.Debug("Handling GET AI settings request")
			settings, err := repository.GetUserAISettings(r.Context(), db, email)
			if err != nil {
				slog.Error("Failed to fetch AI settings", "error", err)
				writeJSONError(w, http.StatusInternalServerError, "Internal server error")
				return
			}
			if settings == nil {
				slog.Info("No custom AI settings found, using defaults")
				// Return default if none exists
				settings = &models.UserAISettings{
					UserEmail:                email,
					RateLimitRequests:        15,
					RateLimitIntervalSeconds: 60,
				}
			} else {
				slog.Debug("Successfully retrieved user AI settings")
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(settings)
			return
		}

		if r.Method == http.MethodPost {
			slog.Debug("Handling POST AI settings request")
			var settings models.UserAISettings
			if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
				slog.Warn("Failed to decode AI settings payload", "error", err)
				writeJSONError(w, http.StatusBadRequest, "Invalid JSON")
				return
			}
			slog.Debug("Decoded settings payload successfully")

			settings.UserEmail = email // Enforce updating own settings
			if settings.RateLimitRequests == 0 {
				settings.RateLimitRequests = 15
			}
			if settings.RateLimitIntervalSeconds == 0 {
				settings.RateLimitIntervalSeconds = 60
			}

			if err := repository.UpsertUserAISettings(r.Context(), db, &settings); err != nil {
				slog.Error("Failed to upsert AI settings", "error", err)
				writeJSONError(w, http.StatusInternalServerError, "Internal server error")
				return
			}
			slog.Info("Successfully updated AI settings", "email", email)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "success"})
			return
		}

		slog.Warn("Method not allowed", "method", r.Method)
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}
