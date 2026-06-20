package api

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/druva-06/recruitingest-backend/config"
	"github.com/druva-06/recruitingest-backend/internal/llm"
	"github.com/druva-06/recruitingest-backend/internal/models"
	"github.com/druva-06/recruitingest-backend/internal/repository"
	"golang.org/x/oauth2"
)

// SearchRecruitersByCompanyResponse is the response structure.
type SearchRecruitersByCompanyResponse struct {
	Recruiters []models.RecruiterRecord `json:"recruiters"`
}

// NewOutreachSearchHandler searches recruiters by company name for outreach.
func NewOutreachSearchHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slog.Info("Executing NewOutreachSearchHandler")
		if r.Method != http.MethodPost {
			slog.Warn("Method not allowed", "method", r.Method)
			writeJSONError(w, http.StatusMethodNotAllowed, "Only POST method is allowed")
			return
		}

		session := SessionFromContext(r.Context())
		if session == nil {
			writeJSONError(w, http.StatusUnauthorized, "Not authenticated")
			return
		}

		var req struct {
			CompanyName string `json:"company_name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			slog.Warn("Failed to decode outreach search payload", "error", err)
			writeJSONError(w, http.StatusBadRequest, "Invalid request body")
			return
		}
		slog.Debug("Decoded request payload successfully")

		company := strings.TrimSpace(req.CompanyName)
		if company == "" {
			writeJSONError(w, http.StatusBadRequest, "Company name is required")
			return
		}

		// Search database for recruiters matching this company name
		slog.Info("Searching recruiters by company", "company", company)
		query := "SELECT id, recruiter_name, COALESCE(recruiter_title, ''), recruiter_email, COALESCE(company_name, ''), COALESCE(location, ''), COALESCE(linkedin_url, ''), COALESCE(source_file, ''), created_at FROM recruiters WHERE company_name LIKE ?"
		rows, err := db.QueryContext(r.Context(), query, "%"+company+"%")
		if err != nil {
			slog.Error("Failed to search recruiters", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "Database search failed")
			return
		}
		defer rows.Close()
		slog.Debug("Executed search query successfully")

		recruiters := []models.RecruiterRecord{}
		for rows.Next() {
			var rec models.RecruiterRecord
			if err := rows.Scan(&rec.ID, &rec.Name, &rec.Title, &rec.Email, &rec.Company, &rec.Location, &rec.LinkedinUrl, &rec.SourceFile, &rec.CreatedAt); err != nil {
				slog.Error("Failed to scan recruiter", "error", err)
				writeJSONError(w, http.StatusInternalServerError, "Failed to read recruiter records")
				return
			}
			recruiters = append(recruiters, rec)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(SearchRecruitersByCompanyResponse{
			Recruiters: recruiters,
		})
	}
}

// NewGeneratePitchHandler handles generating the cold email draft.
func NewGeneratePitchHandler(cfg *config.Config, db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slog.Info("Executing NewGeneratePitchHandler")
		if r.Method != http.MethodPost {
			slog.Warn("Method not allowed", "method", r.Method)
			writeJSONError(w, http.StatusMethodNotAllowed, "Only POST method is allowed")
			return
		}

		session := SessionFromContext(r.Context())
		if session == nil {
			writeJSONError(w, http.StatusUnauthorized, "Not authenticated")
			return
		}

		var req struct {
			JobDescription string `json:"job_description"`
			CompanyName    string `json:"company_name"`
			RecruiterEmail string `json:"recruiter_email"`
			RecruiterName  string `json:"recruiter_name"`
			RecruiterTitle string `json:"recruiter_title"`
			Location       string `json:"location"`
			LinkedinUrl    string `json:"linkedin_url"`
			PitchType      string `json:"pitch_type"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "Invalid request body")
			return
		}

		jobDesc := strings.TrimSpace(req.JobDescription)
		companyName := strings.TrimSpace(req.CompanyName)
		recruiterEmail := strings.ToLower(strings.TrimSpace(req.RecruiterEmail))
		recruiterName := strings.TrimSpace(req.RecruiterName)
		recruiterTitle := strings.TrimSpace(req.RecruiterTitle)
		location := strings.TrimSpace(req.Location)
		linkedinUrl := strings.TrimSpace(req.LinkedinUrl)
		pitchType := strings.TrimSpace(req.PitchType)
		if pitchType == "" {
			pitchType = "outreach"
		}

		if jobDesc == "" || companyName == "" {
			writeJSONError(w, http.StatusBadRequest, "Job description and company name are required")
			return
		}

		if recruiterEmail == "" {
			slog.Warn("Validation failed: recruiter email missing")
			writeJSONError(w, http.StatusBadRequest, "Recruiter email is required to send email")
			return
		}

		// 1. Fetch user's resume from database
		slog.Debug("Fetching user resume configuration", "email", session.Email)
		resume, err := repository.GetUserResume(r.Context(), db, session.Email)
		if err != nil {
			slog.Error("Failed to fetch user resume", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "Failed to retrieve your resume configuration")
			return
		}

		if resume == nil || (resume.DriveLink == "" && resume.ResumeText == "") {
			writeJSONError(w, http.StatusBadRequest, "Please upload your resume PDF or configure a Google Drive link in 'My Resume' first.")
			return
		}

		// 2. Insert recruiter if not already present
		var count int
		err = db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM recruiters WHERE recruiter_email = ?", recruiterEmail).Scan(&count)
		if err != nil {
			slog.Error("Failed to check existing recruiter", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "Database error")
			return
		}

		if count == 0 {
			slog.Debug("Inserting new recruiter into database", "email", recruiterEmail)
			_, err = db.ExecContext(r.Context(),
				"INSERT INTO recruiters (recruiter_name, recruiter_title, recruiter_email, company_name, location, linkedin_url, source_file) VALUES (?, ?, ?, ?, ?, ?, ?)",
				recruiterName, recruiterTitle, recruiterEmail, companyName, location, linkedinUrl, "pitch-outreach")
			if err != nil {
				slog.Error("Failed to save new recruiter", "error", err)
				writeJSONError(w, http.StatusInternalServerError, "Failed to save recruiter to contacts")
				return
			}
			slog.Info("Successfully inserted new recruiter")
		} else {
			// Update the fields if they are provided (for existing recruiters)
			slog.Debug("Updating existing recruiter details", "email", recruiterEmail)
			_, err = db.ExecContext(r.Context(),
				"UPDATE recruiters SET recruiter_title = COALESCE(NULLIF(?, ''), recruiter_title), location = COALESCE(NULLIF(?, ''), location), linkedin_url = COALESCE(NULLIF(?, ''), linkedin_url) WHERE recruiter_email = ?",
				recruiterTitle, location, linkedinUrl, recruiterEmail)
			if err != nil {
				slog.Error("Failed to update recruiter details", "error", err)
			}
		}

		payloadBytes, _ := json.Marshal(map[string]string{
			"job_description": req.JobDescription,
			"company_name":    req.CompanyName,
			"recruiter_email": req.RecruiterEmail,
			"recruiter_name":  req.RecruiterName,
			"recruiter_title": req.RecruiterTitle,
			"location":        req.Location,
			"linkedin_url":    req.LinkedinUrl,
			"user_name":       session.Name,
			"pitch_type":      pitchType,
		})

		jobID, err := repository.CreateAIJob(r.Context(), db, session.Email, "generate_pitch", payloadBytes)
		if err != nil {
			slog.Error("Failed to create AI job record", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "Internal server error while creating job tracker")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "processing",
			"message": "Generating pitch in the background",
			"job_id":  jobID,
		})
	}
}

// NewConfirmPitchHandler sends the approved pitch and records thread/message IDs.
func NewConfirmPitchHandler(cfg *config.Config, db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slog.Info("Executing NewConfirmPitchHandler")
		if r.Method != http.MethodPost {
			slog.Warn("Method not allowed", "method", r.Method)
			writeJSONError(w, http.StatusMethodNotAllowed, "Only POST method is allowed")
			return
		}

		session := SessionFromContext(r.Context())
		if session == nil {
			writeJSONError(w, http.StatusUnauthorized, "Not authenticated")
			return
		}

		var req struct {
			RecruiterEmail     string `json:"recruiter_email"`
			RecruiterName      string `json:"recruiter_name"`
			CompanyName        string `json:"company_name"`
			Subject            string `json:"subject"`
			Body               string `json:"body"`
			Reminder1DelayDays int    `json:"reminder1_delay_days"`
			Reminder2DelayDays int    `json:"reminder2_delay_days"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "Invalid request body")
			return
		}

		recruiterEmail := strings.ToLower(strings.TrimSpace(req.RecruiterEmail))
		if recruiterEmail == "" {
			writeJSONError(w, http.StatusBadRequest, "Recruiter email is required")
			return
		}

		// Apply defaults from user settings if not provided
		if req.Reminder1DelayDays <= 0 || req.Reminder2DelayDays <= 0 {
			rs, err := repository.GetReminderSettings(r.Context(), db, session.Email)
			if err == nil && rs != nil {
				if req.Reminder1DelayDays <= 0 {
					req.Reminder1DelayDays = rs.Reminder1DelayDays
				}
				if req.Reminder2DelayDays <= 0 {
					req.Reminder2DelayDays = rs.Reminder2DelayDays
				}
			} else {
				if req.Reminder1DelayDays <= 0 {
					req.Reminder1DelayDays = 5
				}
				if req.Reminder2DelayDays <= 0 {
					req.Reminder2DelayDays = 10
				}
			}
		}

		gmailClient, err := getGmailClient(r.Context(), db, cfg, session)
		if err != nil {
			slog.Error("Failed to get Gmail client", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "Failed to authorize with Google. Please log out and sign in again.")
			return
		}

		mimeMsg := createMimeMessage(session.Email, recruiterEmail, req.Subject, req.Body, "", nil)
		rawMsg := base64.URLEncoding.EncodeToString([]byte(mimeMsg))

		sendReqBody, _ := json.Marshal(map[string]string{
			"raw": rawMsg,
		})

		resp, err := gmailClient.Post("https://gmail.googleapis.com/gmail/v1/users/me/messages/send", "application/json", strings.NewReader(string(sendReqBody)))
		if err != nil {
			slog.Error("Gmail send API call failed", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "Gmail API error")
			return
		}
		defer resp.Body.Close()

		respBodyBytes, _ := io.ReadAll(resp.Body)

		if resp.StatusCode != http.StatusOK {
			slog.Error("Gmail send returned error status", "statusCode", resp.StatusCode, "body", string(respBodyBytes))
			writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Gmail API returned error status: %d", resp.StatusCode))
			return
		}

		// Parse Gmail response to get threadId and messageId for reply detection
		var gmailResp struct {
			ID       string `json:"id"`
			ThreadID string `json:"threadId"`
		}
		if jsonErr := json.Unmarshal(respBodyBytes, &gmailResp); jsonErr != nil {
			slog.Warn("Could not parse Gmail send response", "error", jsonErr)
		}

		// Record the sent email in our database, keyed by the user's login
		_, saveErr := repository.SaveOutreachEmail(r.Context(), db, &repository.OutreachEmail{
			UserEmail:          session.Email,
			RecruiterEmail:     recruiterEmail,
			RecruiterName:      req.RecruiterName,
			CompanyName:        req.CompanyName,
			Subject:            req.Subject,
			Body:               req.Body,
			GmailThreadID:      gmailResp.ThreadID,
			GmailMessageID:     gmailResp.ID,
			Reminder1DelayDays: req.Reminder1DelayDays,
			Reminder2DelayDays: req.Reminder2DelayDays,
		})
		if saveErr != nil {
			slog.Warn("Failed to save sent email record", "error", saveErr)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "sent",
		})
	}
}

// NewSentEmailsHandler returns the list of sent outreach emails for the current user.
func NewSentEmailsHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slog.Info("Executing NewSentEmailsHandler")
		if r.Method != http.MethodGet {
			slog.Warn("Method not allowed", "method", r.Method)
			writeJSONError(w, http.StatusMethodNotAllowed, "Only GET method is allowed")
			return
		}
		session := SessionFromContext(r.Context())
		if session == nil {
			writeJSONError(w, http.StatusUnauthorized, "Not authenticated")
			return
		}
		emails, err := repository.GetOutreachEmailsByUser(r.Context(), db, session.Email)
		if err != nil {
			slog.Error("Failed to get sent emails", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "Failed to retrieve sent emails")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"emails": emails,
			"user":   session.Email,
		})
	}
}

// NewEnhancePitchHandler refines an existing pitch draft using AI.
func NewEnhancePitchHandler(cfg *config.Config, db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSONError(w, http.StatusMethodNotAllowed, "Only POST method is allowed")
			return
		}

		session := SessionFromContext(r.Context())
		if session == nil {
			writeJSONError(w, http.StatusUnauthorized, "Not authenticated")
			return
		}

		var req struct {
			Subject     string `json:"subject"`
			Body        string `json:"body"`
			Instruction string `json:"instruction"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "Invalid request body")
			return
		}

		if req.Instruction == "" {
			writeJSONError(w, http.StatusBadRequest, "Enhancement instruction is required")
			return
		}

		settings, err := repository.GetUserAISettings(r.Context(), db, session.Email)
		var apiKey, modelName string
		if err == nil && settings != nil {
			apiKey = settings.GeminiAPIKey
			modelName = settings.GeminiModel
		}
		if apiKey == "" {
			apiKey = cfg.GeminiAPIKey
		}
		if modelName == "" {
			modelName = cfg.GeminiModel
		}

		geminiSvc, err := llm.NewGeminiService(r.Context(), apiKey, modelName)
		if err != nil {
			slog.Error("AI service unavailable", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "AI service unavailable")
			return
		}
		defer geminiSvc.Close()

		newSubj, newBody, err := geminiSvc.EnhanceEmailContent(r.Context(), modelName, req.Subject, req.Body, req.Instruction)
		if err != nil {
			slog.Error("Failed to enhance email", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "Failed to enhance email draft")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"subject": newSubj,
			"body":    newBody,
		})
	}
}

func getGmailClient(ctx context.Context, db *sql.DB, cfg *config.Config, session *repository.Session) (*http.Client, error) {
	oc := OAuthConfig(cfg)
	token := &oauth2.Token{
		AccessToken:  session.AccessToken,
		RefreshToken: session.RefreshToken,
		Expiry:       time.Now().Add(-1 * time.Hour), // force token refresh
	}

	tokenSource := oc.TokenSource(ctx, token)
	newToken, err := tokenSource.Token()
	if err != nil {
		return nil, fmt.Errorf("failed to refresh oauth token: %w", err)
	}

	if newToken.AccessToken != session.AccessToken {
		slog.Info("Saving refreshed access token for user", "email", session.Email)
		const q = "UPDATE sessions SET access_token = ? WHERE session_id = ?"
		_, dbErr := db.ExecContext(ctx, q, newToken.AccessToken, session.SessionID)
		if dbErr != nil {
			slog.Warn("Failed to save refreshed token to DB", "error", dbErr)
		}
	}

	return oc.Client(ctx, newToken), nil
}

func createMimeMessage(sender, to, subject, body string, attachmentFilename string, attachmentContent []byte) string {
	if len(attachmentContent) == 0 {
		return fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/html; charset=\"UTF-8\"\r\n\r\n%s",
			sender, to, subject, body)
	}

	boundary := "recruitingest_boundary_12345"
	mime := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=\"%s\"\r\n\r\n",
		sender, to, subject, boundary)

	// Body part
	mime += fmt.Sprintf("--%s\r\nContent-Type: text/html; charset=\"UTF-8\"\r\nContent-Transfer-Encoding: 7bit\r\n\r\n%s\r\n\r\n",
		boundary, body)

	// Attachment part
	mime += fmt.Sprintf("--%s\r\nContent-Type: application/pdf; name=\"%s\"\r\nContent-Disposition: attachment; filename=\"%s\"\r\nContent-Transfer-Encoding: base64\r\n\r\n",
		boundary, attachmentFilename, attachmentFilename)

	mime += base64.StdEncoding.EncodeToString(attachmentContent) + "\r\n\r\n"
	mime += fmt.Sprintf("--%s--", boundary)

	return mime
}

// NewPromptSettingsHandler handles GET and POST for custom outreach prompts.
func NewPromptSettingsHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slog.Info("Executing NewPromptSettingsHandler")
		session := SessionFromContext(r.Context())
		if session == nil {
			slog.Warn("Unauthorized access attempt")
			writeJSONError(w, http.StatusUnauthorized, "Not authenticated")
			return
		}

		switch r.Method {
		case http.MethodGet:
			slog.Debug("Handling GET custom prompt request")
			getCustomPrompt(w, r, db, session.Email)
		case http.MethodPost:
			slog.Debug("Handling POST custom prompt request")
			saveCustomPrompt(w, r, db, session.Email)
		default:
			slog.Warn("Method not allowed", "method", r.Method)
			writeJSONError(w, http.StatusMethodNotAllowed, "Only GET and POST methods are allowed")
		}
	}
}

func getCustomPrompt(w http.ResponseWriter, r *http.Request, db *sql.DB, email string) {
	var customPrompt string
	var referralPrompt sql.NullString
	var linkedinPrompt sql.NullString
	err := db.QueryRowContext(r.Context(), "SELECT custom_prompt, referral_prompt, linkedin_outreach_prompt FROM user_prompts WHERE email = ?", email).Scan(&customPrompt, &referralPrompt, &linkedinPrompt)
	if err == sql.ErrNoRows {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"email":                    email,
			"custom_prompt":            "",
			"referral_prompt":          "",
			"linkedin_outreach_prompt": "",
		})
		return
	}
	if err != nil {
		slog.Error("Failed to query custom prompt", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "Failed to retrieve custom prompt")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"email":                    email,
		"custom_prompt":            customPrompt,
		"referral_prompt":          referralPrompt.String,
		"linkedin_outreach_prompt": linkedinPrompt.String,
	})
}

func saveCustomPrompt(w http.ResponseWriter, r *http.Request, db *sql.DB, email string) {
	var req struct {
		CustomPrompt           string `json:"custom_prompt"`
		ReferralPrompt         string `json:"referral_prompt"`
		LinkedinOutreachPrompt string `json:"linkedin_outreach_prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	prompt := strings.TrimSpace(req.CustomPrompt)
	refPrompt := strings.TrimSpace(req.ReferralPrompt)
	linkedinPrompt := strings.TrimSpace(req.LinkedinOutreachPrompt)

	const q = `
		INSERT INTO user_prompts (email, custom_prompt, referral_prompt, linkedin_outreach_prompt)
		VALUES (?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE custom_prompt = VALUES(custom_prompt), referral_prompt = VALUES(referral_prompt), linkedin_outreach_prompt = VALUES(linkedin_outreach_prompt)
	`
	_, err := db.ExecContext(r.Context(), q, email, prompt, refPrompt, linkedinPrompt)
	if err != nil {
		slog.Error("Failed to save custom prompt", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "Failed to save custom prompt")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":                   "success",
		"custom_prompt":            prompt,
		"referral_prompt":          refPrompt,
		"linkedin_outreach_prompt": linkedinPrompt,
	})
}
