package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"github.com/druva-06/recruitingest-backend/config"
)

func resolveProspeoKey(r *http.Request, cfg *config.Config) string {
	key := r.Header.Get("X-Prospeo-API-Key")
	if key == "" {
		key = cfg.ProspeoAPIKey
	}
	return key
}

// NewProspeoEnrichHandler enriches a specific person from Prospeo, revealing their email
func NewProspeoEnrichHandler(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slog.Info("Executing NewProspeoEnrichHandler")
		apiKey := resolveProspeoKey(r, cfg)
		if apiKey == "" {
			slog.Warn("Validation failed: Prospeo API key missing")
			writeJSONError(w, http.StatusBadRequest, "Prospeo API key is required")
			return
		}
		slog.Debug("Resolved Prospeo API key")

		var req struct {
			FirstName   string `json:"first_name"`
			LastName    string `json:"last_name"`
			FullName    string `json:"full_name"`
			CompanyName string `json:"company_name"`
			LinkedinUrl string `json:"linkedin_url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			slog.Warn("Failed to decode enrichment request body", "error", err)
			writeJSONError(w, http.StatusBadRequest, "Invalid request body")
			return
		}
		slog.Debug("Decoded request payload successfully", "linkedin_url", req.LinkedinUrl, "full_name", req.FullName)

		payload := map[string]interface{}{}
		dataPayload := map[string]interface{}{}

		if req.LinkedinUrl != "" {
			dataPayload["linkedin_url"] = req.LinkedinUrl
		} else {
			if req.FullName != "" {
				dataPayload["full_name"] = req.FullName
			} else {
				dataPayload["first_name"] = req.FirstName
				dataPayload["last_name"] = req.LastName
			}
			dataPayload["company_name"] = req.CompanyName
		}
		slog.Debug("Constructed payload for Prospeo API", "payload", dataPayload)

		payload["data"] = dataPayload

		payloadBytes, _ := json.Marshal(payload)

		reqHttp, err := http.NewRequest("POST", "https://api.prospeo.io/enrich-person", bytes.NewReader(payloadBytes))
		if err != nil {
			slog.Error("Enrich request creation failed", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "Failed to create request")
			return
		}
		reqHttp.Header.Set("Content-Type", "application/json")
		reqHttp.Header.Set("X-KEY", apiKey)

		slog.Info("Sending enrichment request to Prospeo API")
		resp, err := http.DefaultClient.Do(reqHttp)
		if err != nil {
			slog.Error("Enrich request failed", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "Failed to contact Prospeo API")
			return
		}
		defer resp.Body.Close()
		slog.Debug("Received response from Prospeo API", "status_code", resp.StatusCode)

		bodyBytes, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			slog.Error("Enrich returned error status", "statusCode", resp.StatusCode, "body", string(bodyBytes))
			writeJSONError(w, resp.StatusCode, "Prospeo API returned an error")
			return
		}
		slog.Info("Successfully enriched person from Prospeo API")

		w.Header().Set("Content-Type", "application/json")
		w.Write(bodyBytes)
	}
}
