package api

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/druva-06/recruitingest-backend/config"
	"github.com/druva-06/recruitingest-backend/internal/llm"
	"github.com/druva-06/recruitingest-backend/internal/repository"
)

// NewReminderDraftsHandler — GET: list pending drafts + badge count.
func NewReminderDraftsHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSONError(w, http.StatusMethodNotAllowed, "Only GET allowed")
			return
		}
		session := SessionFromContext(r.Context())
		if session == nil {
			writeJSONError(w, http.StatusUnauthorized, "Not authenticated")
			return
		}
		drafts, err := repository.GetPendingDraftsByUser(r.Context(), db, session.Email)
		if err != nil {
			log.Printf("[Reminder] Failed to get drafts: %v", err)
			writeJSONError(w, http.StatusInternalServerError, "Failed to retrieve reminder drafts")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"drafts": drafts,
			"count":  len(drafts),
		})
	}
}

// NewReminderDraftActionHandler — PATCH to edit, DELETE to reject a single draft.
func NewReminderDraftActionHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := SessionFromContext(r.Context())
		if session == nil {
			writeJSONError(w, http.StatusUnauthorized, "Not authenticated")
			return
		}

		// Extract ID from path: /api/v1/reminders/drafts/{id}
		parts := strings.Split(strings.TrimSuffix(r.URL.Path, "/"), "/")
		if len(parts) == 0 {
			writeJSONError(w, http.StatusBadRequest, "Missing draft ID")
			return
		}
		idStr := parts[len(parts)-1]
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "Invalid draft ID")
			return
		}

		switch r.Method {
		case http.MethodPatch:
			var req struct {
				Subject string `json:"subject"`
				Body    string `json:"body"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeJSONError(w, http.StatusBadRequest, "Invalid request body")
				return
			}
			if err := repository.UpdateDraftContent(r.Context(), db, id, req.Subject, req.Body); err != nil {
				writeJSONError(w, http.StatusInternalServerError, "Failed to update draft")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "updated"})

		case http.MethodDelete:
			if err := repository.RejectDraft(r.Context(), db, id); err != nil {
				writeJSONError(w, http.StatusInternalServerError, "Failed to reject draft")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "rejected"})

		default:
			writeJSONError(w, http.StatusMethodNotAllowed, "Only PATCH and DELETE allowed")
		}
	}
}

// NewSendReminderHandler — POST: send one or more approved reminder drafts.
func NewSendReminderHandler(cfg *config.Config, db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSONError(w, http.StatusMethodNotAllowed, "Only POST allowed")
			return
		}
		session := SessionFromContext(r.Context())
		if session == nil {
			writeJSONError(w, http.StatusUnauthorized, "Not authenticated")
			return
		}

		var req struct {
			DraftIDs []int64 `json:"draft_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.DraftIDs) == 0 {
			writeJSONError(w, http.StatusBadRequest, "draft_ids array is required")
			return
		}

		gmailClient, err := getGmailClient(r.Context(), db, cfg, session)
		if err != nil {
			log.Printf("[Reminder] Failed to get Gmail client: %v", err)
			writeJSONError(w, http.StatusInternalServerError, "Gmail auth failed. Please sign in again.")
			return
		}

		var sent, failed []int64
		for _, draftID := range req.DraftIDs {
			draft, err := repository.GetDraftByID(r.Context(), db, draftID)
			if err != nil || draft == nil {
				log.Printf("[Reminder] Draft %d not found: %v", draftID, err)
				failed = append(failed, draftID)
				continue
			}

			// Build in-thread MIME reply
			mimeMsg := createReminderMimeMessage(session.Email, draft.RecruiterEmail, draft.Subject, draft.Body, draft.GmailMessageID)
			rawMsg := base64.URLEncoding.EncodeToString([]byte(mimeMsg))

			sendPayload, _ := json.Marshal(map[string]string{
				"raw":      rawMsg,
				"threadId": draft.GmailThreadID,
			})

			resp, err := gmailClient.Post(
				"https://gmail.googleapis.com/gmail/v1/users/me/messages/send",
				"application/json",
				strings.NewReader(string(sendPayload)),
			)
			if err != nil {
				log.Printf("[Reminder] Gmail send failed for draft %d: %v", draftID, err)
				failed = append(failed, draftID)
				continue
			}
			resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				log.Printf("[Reminder] Gmail returned %d for draft %d", resp.StatusCode, draftID)
				failed = append(failed, draftID)
				continue
			}

			// Mark draft as sent
			_ = repository.MarkDraftSent(r.Context(), db, draftID)

			// Update parent outreach email status
			tsField := "reminder1_sent_at"
			newStatus := "reminder_1_sent"
			if draft.ReminderNumber == 2 {
				tsField = "reminder2_sent_at"
				newStatus = "reminder_2_sent"
			}
			_ = repository.UpdateOutreachStatus(r.Context(), db, draft.OutreachEmailID, newStatus, tsField)

			sent = append(sent, draftID)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"sent":   sent,
			"failed": failed,
		})
	}
}

// NewEmailStatusHandler — PATCH: manually update status of an outreach email.
func NewEmailStatusHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			writeJSONError(w, http.StatusMethodNotAllowed, "Only PATCH allowed")
			return
		}
		session := SessionFromContext(r.Context())
		if session == nil {
			writeJSONError(w, http.StatusUnauthorized, "Not authenticated")
			return
		}

		parts := strings.Split(strings.TrimSuffix(r.URL.Path, "/"), "/")
		idStr := parts[len(parts)-2] // path: /emails/{id}/status
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "Invalid email ID")
			return
		}

		var req struct {
			Status string `json:"status"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "Invalid request body")
			return
		}

		allowed := map[string]string{
			"replied": "replied_at",
			"closed":  "",
			"ghosted": "ghosted_at",
		}
		tsField, ok := allowed[req.Status]
		if !ok {
			writeJSONError(w, http.StatusBadRequest, "Invalid status. Allowed: replied, closed, ghosted")
			return
		}

		if err := repository.UpdateOutreachStatus(r.Context(), db, id, req.Status, tsField); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "Failed to update status")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": req.Status})
	}
}

// NewEmailDelayHandler — PATCH: update per-email reminder delay from Sent Emails view.
func NewEmailDelayHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			writeJSONError(w, http.StatusMethodNotAllowed, "Only PATCH allowed")
			return
		}
		session := SessionFromContext(r.Context())
		if session == nil {
			writeJSONError(w, http.StatusUnauthorized, "Not authenticated")
			return
		}

		parts := strings.Split(strings.TrimSuffix(r.URL.Path, "/"), "/")
		idStr := parts[len(parts)-2] // /emails/{id}/delays
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "Invalid email ID")
			return
		}

		var req struct {
			Reminder1DelayDays int `json:"reminder1_delay_days"`
			Reminder2DelayDays int `json:"reminder2_delay_days"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "Invalid request body")
			return
		}

		if err := repository.UpdateEmailDelays(r.Context(), db, id, req.Reminder1DelayDays, req.Reminder2DelayDays); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "Failed to update delays")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
	}
}

// NewReminderSettingsHandler — GET/POST: global user reminder delay settings.
func NewReminderSettingsHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := SessionFromContext(r.Context())
		if session == nil {
			writeJSONError(w, http.StatusUnauthorized, "Not authenticated")
			return
		}
		switch r.Method {
		case http.MethodGet:
			s, err := repository.GetReminderSettings(r.Context(), db, session.Email)
			if err != nil {
				writeJSONError(w, http.StatusInternalServerError, "Failed to load reminder settings")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(s)
		case http.MethodPost:
			var req repository.ReminderSettings
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeJSONError(w, http.StatusBadRequest, "Invalid request body")
				return
			}
			req.Email = session.Email
			if err := repository.SaveReminderSettings(r.Context(), db, &req); err != nil {
				writeJSONError(w, http.StatusInternalServerError, "Failed to save reminder settings")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "saved"})
		default:
			writeJSONError(w, http.StatusMethodNotAllowed, "Only GET and POST allowed")
		}
	}
}

// NewPendingCountHandler — GET: returns badge count of pending reminder drafts.
func NewPendingCountHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := SessionFromContext(r.Context())
		if session == nil {
			writeJSONError(w, http.StatusUnauthorized, "Not authenticated")
			return
		}
		n, err := repository.CountPendingDrafts(r.Context(), db, session.Email)
		if err != nil {
			n = 0
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]int{"count": n})
	}
}

// createReminderMimeMessage creates an in-thread reply MIME message.
func createReminderMimeMessage(sender, to, subject, body, inReplyToMessageID string) string {
	headers := fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/html; charset=\"UTF-8\"",
		sender, to, subject,
	)
	if inReplyToMessageID != "" {
		// Gmail message IDs need angle brackets in MIME headers
		msgRef := "<" + inReplyToMessageID + "@mail.gmail.com>"
		headers += fmt.Sprintf("\r\nIn-Reply-To: %s\r\nReferences: %s", msgRef, msgRef)
	}
	return headers + "\r\n\r\n" + body
}

// GenerateReminderDraftsHandler — POST: trigger on-demand Gemini draft generation for overdue emails.
func NewGenerateReminderDraftsHandler(cfg *config.Config, db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSONError(w, http.StatusMethodNotAllowed, "Only POST allowed")
			return
		}
		session := SessionFromContext(r.Context())
		if session == nil {
			writeJSONError(w, http.StatusUnauthorized, "Not authenticated")
			return
		}

		jobID, err := repository.CreateAIJob(r.Context(), db, session.Email, "generate_reminders", nil)
		if err != nil {
			log.Printf("[Error] Failed to create AI job record: %v\n", err)
			writeJSONError(w, http.StatusInternalServerError, "Failed to queue draft generation")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "processing",
			"message": "Generating reminder drafts in the background",
			"job_id":  jobID,
		})
	}
}

// generateDraftsForUser is the shared logic called by both the on-demand handler and the background poller.
func generateDraftsForUser(ctx context.Context, db *sql.DB, cfg *config.Config, userEmail, apiKey, modelName string) (int, error) {
	// Get all emails for this user that are still pending reply
	emails, err := repository.GetOutreachEmailsByUser(ctx, db, userEmail)
	if err != nil {
		return 0, err
	}

	geminiSvc, err := llm.NewGeminiService(ctx, apiKey, modelName)
	if err != nil {
		return 0, fmt.Errorf("failed to init Gemini: %w", err)
	}
	defer geminiSvc.Close()

	generated := 0
	now := time.Now()

	for _, e := range emails {
		if e.Status == "replied" || e.Status == "closed" || e.Status == "ghosted" {
			continue
		}

		daysSinceSent := int(now.Sub(e.SentAt).Hours() / 24)

		// Check if Reminder 1 is due
		if e.Status == "awaiting_reply" && daysSinceSent >= e.Reminder1DelayDays {
			subject, body, genErr := geminiSvc.GenerateFollowUpEmail(
				ctx, modelName,
				e.RecruiterName, e.CompanyName, userEmail, userEmail,
				daysSinceSent, e.Subject, e.Body, 1,
			)
			if genErr != nil {
				log.Printf("[Reminder] Gemini follow-up gen failed for email %d: %v", e.ID, genErr)
				continue
			}
			_, dbErr := repository.CreateReminderDraft(ctx, db, &repository.ReminderDraft{
				OutreachEmailID: e.ID,
				UserEmail:       userEmail,
				ReminderNumber:  1,
				RecruiterEmail:  e.RecruiterEmail,
				RecruiterName:   e.RecruiterName,
				CompanyName:     e.CompanyName,
				GmailThreadID:   e.GmailThreadID,
				GmailMessageID:  e.GmailMessageID,
				Subject:         subject,
				Body:            body,
			})
			if dbErr == nil {
				generated++
			}
		}

		// Check if Reminder 2 is due (after Reminder 1 was sent)
		if e.Status == "reminder_1_sent" && e.Reminder1SentAt != nil {
			daysSinceR1 := int(now.Sub(*e.Reminder1SentAt).Hours() / 24)
			if daysSinceR1 >= e.Reminder2DelayDays {
				subject, body, genErr := geminiSvc.GenerateFollowUpEmail(
					ctx, modelName,
					e.RecruiterName, e.CompanyName, userEmail, userEmail,
					daysSinceSent, e.Subject, e.Body, 2,
				)
				if genErr != nil {
					log.Printf("[Reminder] Gemini follow-up gen failed for email %d R2: %v", e.ID, genErr)
					continue
				}
				_, dbErr := repository.CreateReminderDraft(ctx, db, &repository.ReminderDraft{
					OutreachEmailID: e.ID,
					UserEmail:       userEmail,
					ReminderNumber:  2,
					RecruiterEmail:  e.RecruiterEmail,
					RecruiterName:   e.RecruiterName,
					CompanyName:     e.CompanyName,
					GmailThreadID:   e.GmailThreadID,
					GmailMessageID:  e.GmailMessageID,
					Subject:         subject,
					Body:            body,
				})
				if dbErr == nil {
					generated++
				}
			}
		}

		// Auto-ghost: Reminder 2 sent + enough days with no reply
		if e.Status == "reminder_2_sent" && e.Reminder2SentAt != nil {
			daysSinceR2 := int(now.Sub(*e.Reminder2SentAt).Hours() / 24)
			if daysSinceR2 >= 7 { // 7 days after last reminder → ghosted
				_ = repository.UpdateOutreachStatus(ctx, db, e.ID, "ghosted", "ghosted_at")
			}
		}
	}

	return generated, nil
}
