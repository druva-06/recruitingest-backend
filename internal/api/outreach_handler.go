package api

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
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
			CompanyName string `json:"company_name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "Invalid request body")
			return
		}

		company := strings.TrimSpace(req.CompanyName)
		if company == "" {
			writeJSONError(w, http.StatusBadRequest, "Company name is required")
			return
		}

		// Search database for recruiters matching this company name
		query := "SELECT id, recruiter_name, COALESCE(recruiter_title, ''), recruiter_email, COALESCE(company_name, ''), COALESCE(source_file, ''), created_at FROM recruiters WHERE company_name LIKE ?"
		rows, err := db.QueryContext(r.Context(), query, "%"+company+"%")
		if err != nil {
			log.Printf("[Outreach] Failed to search recruiters: %v", err)
			writeJSONError(w, http.StatusInternalServerError, "Database search failed")
			return
		}
		defer rows.Close()

		recruiters := []models.RecruiterRecord{}
		for rows.Next() {
			var rec models.RecruiterRecord
			if err := rows.Scan(&rec.ID, &rec.Name, &rec.Title, &rec.Email, &rec.Company, &rec.SourceFile, &rec.CreatedAt); err != nil {
				log.Printf("[Outreach] Failed to scan recruiter: %v", err)
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

// NewSendPitchHandler handles generating the cold email and sending it.
func NewSendPitchHandler(cfg *config.Config, db *sql.DB) http.HandlerFunc {
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
			JobDescription string `json:"job_description"`
			CompanyName    string `json:"company_name"`
			RecruiterEmail string `json:"recruiter_email"`
			RecruiterName  string `json:"recruiter_name"`
			RecruiterTitle string `json:"recruiter_title"`
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

		if jobDesc == "" || companyName == "" {
			writeJSONError(w, http.StatusBadRequest, "Job description and company name are required")
			return
		}

		if recruiterEmail == "" {
			writeJSONError(w, http.StatusBadRequest, "Recruiter email is required to send email")
			return
		}

		// 1. Fetch user's resume from database
		resume, err := repository.GetUserResume(r.Context(), db, session.Email)
		if err != nil {
			log.Printf("[Outreach] Failed to fetch user resume: %v", err)
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
			log.Printf("[Outreach] Failed to check existing recruiter: %v", err)
			writeJSONError(w, http.StatusInternalServerError, "Database error")
			return
		}

		if count == 0 {
			_, err = db.ExecContext(r.Context(),
				"INSERT INTO recruiters (recruiter_name, recruiter_title, recruiter_email, company_name, source_file) VALUES (?, ?, ?, ?, ?)",
				recruiterName, recruiterTitle, recruiterEmail, companyName, "pitch-outreach")
			if err != nil {
				log.Printf("[Outreach] Failed to save new recruiter: %v", err)
				writeJSONError(w, http.StatusInternalServerError, "Failed to save recruiter to contacts")
				return
			}
		} else if recruiterTitle != "" {
			// Update the title if recruiter exists but title is updated
			_, err = db.ExecContext(r.Context(),
				"UPDATE recruiters SET recruiter_title = ? WHERE recruiter_email = ?",
				recruiterTitle, recruiterEmail)
			if err != nil {
				log.Printf("[Outreach] Failed to update recruiter title: %v", err)
			}
		}

		// 3. Resolve Gemini credentials
		reqAPIKey := r.Header.Get("X-Gemini-API-Key")
		if reqAPIKey == "" {
			reqAPIKey = cfg.GeminiAPIKey
		}
		if reqAPIKey == "" {
			writeJSONError(w, http.StatusBadRequest, "Gemini API key is required. Please set it in Settings.")
			return
		}

		reqModel := r.Header.Get("X-Gemini-Model")
		if reqModel == "" {
			reqModel = cfg.GeminiModel
		}
		if reqModel == "" {
			reqModel = "gemini-3.5-flash"
		}

		// 4. Load custom prompt if configured
		var customPrompt string
		err = db.QueryRowContext(r.Context(), "SELECT custom_prompt FROM user_prompts WHERE email = ?", session.Email).Scan(&customPrompt)
		if err != nil && err != sql.ErrNoRows {
			log.Printf("[Outreach] Warning: failed to query custom prompt from database: %v", err)
		}

		// Initialize Gemini service and generate email content
		geminiSvc, err := llm.NewGeminiService(r.Context(), reqAPIKey, reqModel)
		if err != nil {
			log.Printf("[Outreach] Failed to initialize Gemini service: %v", err)
			writeJSONError(w, http.StatusInternalServerError, "Failed to initialize AI engine")
			return
		}
		defer geminiSvc.Close()

		subject, body, err := geminiSvc.GenerateEmailContent(r.Context(), reqModel, jobDesc, companyName, recruiterName, session.Name, session.Email, resume.ResumeText, resume.DriveLink, customPrompt)
		if err != nil {
			log.Printf("[Outreach] Gemini generation failed: %v", err)
			writeJSONError(w, http.StatusInternalServerError, "Failed to generate outreach email with AI")
			return
		}

		// 5. Get Gmail HTTP client
		gmailClient, err := getGmailClient(r.Context(), db, cfg, session)
		if err != nil {
			log.Printf("[Outreach] Failed to get Gmail client: %v", err)
			writeJSONError(w, http.StatusInternalServerError, "Failed to authorize with Google. Please log out and sign in again.")
			return
		}

		// 6. Build MIME message (no file attachment, text highlighted and Drive Link is referenced in the body)
		mimeMsg := createMimeMessage(session.Email, recruiterEmail, subject, body, "", nil)
		rawMsg := base64.URLEncoding.EncodeToString([]byte(mimeMsg))

		// 7. Send via Gmail API
		sendReqBody, _ := json.Marshal(map[string]string{
			"raw": rawMsg,
		})

		resp, err := gmailClient.Post("https://gmail.googleapis.com/gmail/v1/users/me/messages/send", "application/json", strings.NewReader(string(sendReqBody)))
		if err != nil {
			log.Printf("[Outreach] Gmail send API call failed: %v", err)
			writeJSONError(w, http.StatusInternalServerError, "Gmail API error")
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			respBodyBytes, _ := io.ReadAll(resp.Body)
			log.Printf("[Outreach] Gmail send returned status %d. Body: %s", resp.StatusCode, string(respBodyBytes))
			writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Gmail API returned error status: %d", resp.StatusCode))
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "sent",
			"subject": subject,
			"body":    body,
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
		log.Printf("[Gmail] Saving refreshed access token for user: %s", session.Email)
		const q = "UPDATE sessions SET access_token = ? WHERE session_id = ?"
		_, dbErr := db.ExecContext(ctx, q, newToken.AccessToken, session.SessionID)
		if dbErr != nil {
			log.Printf("[Gmail] Warning: failed to save refreshed token to DB: %v", dbErr)
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
		session := SessionFromContext(r.Context())
		if session == nil {
			writeJSONError(w, http.StatusUnauthorized, "Not authenticated")
			return
		}

		switch r.Method {
		case http.MethodGet:
			getCustomPrompt(w, r, db, session.Email)
		case http.MethodPost:
			saveCustomPrompt(w, r, db, session.Email)
		default:
			writeJSONError(w, http.StatusMethodNotAllowed, "Only GET and POST methods are allowed")
		}
	}
}

func getCustomPrompt(w http.ResponseWriter, r *http.Request, db *sql.DB, email string) {
	var customPrompt string
	err := db.QueryRowContext(r.Context(), "SELECT custom_prompt FROM user_prompts WHERE email = ?", email).Scan(&customPrompt)
	if err == sql.ErrNoRows {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"email":         email,
			"custom_prompt": "",
		})
		return
	}
	if err != nil {
		log.Printf("[Outreach] Failed to query custom prompt: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "Failed to retrieve custom prompt")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"email":         email,
		"custom_prompt": customPrompt,
	})
}

func saveCustomPrompt(w http.ResponseWriter, r *http.Request, db *sql.DB, email string) {
	var req struct {
		CustomPrompt string `json:"custom_prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	prompt := strings.TrimSpace(req.CustomPrompt)

	const q = `
		INSERT INTO user_prompts (email, custom_prompt)
		VALUES (?, ?)
		ON DUPLICATE KEY UPDATE custom_prompt = VALUES(custom_prompt)
	`
	_, err := db.ExecContext(r.Context(), q, email, prompt)
	if err != nil {
		log.Printf("[Outreach] Failed to save custom prompt: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "Failed to save custom prompt")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":        "success",
		"custom_prompt": prompt,
	})
}

