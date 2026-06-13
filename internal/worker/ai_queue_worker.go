package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/druva-06/recruitingest-backend/config"
	"github.com/druva-06/recruitingest-backend/internal/llm"
	"github.com/druva-06/recruitingest-backend/internal/models"
	"github.com/druva-06/recruitingest-backend/internal/parser"
	"github.com/druva-06/recruitingest-backend/internal/pdfparser"
	"github.com/druva-06/recruitingest-backend/internal/repository"
	"golang.org/x/time/rate"
)

func StartAIQueueWorker(db *sql.DB, cfg *config.Config) {
	ticker := time.NewTicker(5 * time.Second)
	go func() {
		for range ticker.C {
			processPendingAIJobs(db, cfg)
		}
	}()
}

func processPendingAIJobs(db *sql.DB, cfg *config.Config) {
	ctx := context.Background()
	jobs, err := repository.GetPendingAIJobs(ctx, db)
	if err != nil {
		slog.Error("Failed to get pending jobs", "error", err)
		return
	}

	for _, job := range jobs {
		processSingleJob(ctx, db, cfg, job)
	}
}

func processSingleJob(ctx context.Context, db *sql.DB, cfg *config.Config, job models.AIJob) {
	settings, err := repository.GetUserAISettings(ctx, db, job.UserEmail)
	if err != nil || settings == nil {
		settings = &models.UserAISettings{
			UserEmail:                job.UserEmail,
			GeminiAPIKey:             cfg.GeminiAPIKey,
			GeminiModel:              cfg.GeminiModel,
			RateLimitRequests:        15,
			RateLimitIntervalSeconds: 60,
		}
	}

	apiKey := settings.GeminiAPIKey
	if apiKey == "" {
		apiKey = cfg.GeminiAPIKey
	}
	modelName := settings.GeminiModel
	if modelName == "" {
		modelName = cfg.GeminiModel
	}

	if err := repository.UpdateAIJobStatus(ctx, db, int64(job.ID), "processing", nil, nil); err != nil {
		slog.Error("Failed to mark job as processing", "jobID", job.ID, "error", err)
		return
	}

	var result map[string]interface{}
	var processErr error

	switch job.JobType {
	case "parse_resume":
		result, processErr = handleParseResume(ctx, db, cfg, job, apiKey, modelName, settings)
	case "extract_text_recruiters":
		result, processErr = handleExtractTextRecruiters(ctx, db, cfg, job, apiKey, modelName, settings)
	case "generate_pitch":
		result, processErr = handleGeneratePitch(ctx, db, cfg, job, apiKey, modelName)
	case "generate_reminders":
		result, processErr = handleGenerateReminders(ctx, db, cfg, job, apiKey, modelName)
	default:
		processErr = fmt.Errorf("unknown job type: %s", job.JobType)
	}

	status := "completed"
	var errMsg *string
	if processErr != nil {
		status = "failed"
		errStr := processErr.Error()
		errMsg = &errStr
	}

	var resultJSON json.RawMessage
	if result != nil {
		b, _ := json.Marshal(result)
		resultJSON = json.RawMessage(b)
	}

	if err := repository.UpdateAIJobStatus(ctx, db, int64(job.ID), status, resultJSON, errMsg); err != nil {
		slog.Error("Failed to mark job status", "jobID", job.ID, "status", status, "error", err)
	}
}

func handleParseResume(ctx context.Context, db *sql.DB, cfg *config.Config, job models.AIJob, apiKey, modelName string, settings *models.UserAISettings) (map[string]interface{}, error) {
	var payload struct {
		FilePath string `json:"file_path"`
		FileName string `json:"file_name"`
	}
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}

	text, err := pdfparser.ExtractText(payload.FilePath)
	if err != nil {
		return nil, fmt.Errorf("extract text failed: %w", err)
	}

	chunks := parser.ChunkText(text, 6000)

	llmSvc, err := llm.NewGeminiService(ctx, apiKey, modelName)
	if err != nil {
		return nil, fmt.Errorf("llm init failed: %w", err)
	}
	defer llmSvc.Close()

	var limiter *rate.Limiter
	if settings.RateLimitRequests > 0 && settings.RateLimitIntervalSeconds > 0 {
		limit := rate.Limit(float64(settings.RateLimitRequests) / float64(settings.RateLimitIntervalSeconds))
		limiter = rate.NewLimiter(limit, settings.RateLimitRequests)
	}

	var allRecruiters []models.Recruiter
	for _, chunk := range chunks {
		if limiter != nil {
			_ = limiter.Wait(ctx)
		}
		recruiters, err := llmSvc.ExtractRecruiters(ctx, chunk)
		if err != nil {
			slog.Warn("Chunk extraction failed", "error", err)
			continue
		}
		allRecruiters = append(allRecruiters, recruiters...)
	}

	insertedCount := int64(0)
	if len(allRecruiters) > 0 {
		insertedCount, err = repository.BulkInsertRecruiters(ctx, db, allRecruiters, payload.FileName)
		if err != nil {
			return nil, fmt.Errorf("bulk insert failed: %w", err)
		}
	}

	return map[string]interface{}{
		"total_extracted": len(allRecruiters),
		"total_inserted":  insertedCount,
	}, nil
}

func handleGeneratePitch(ctx context.Context, db *sql.DB, cfg *config.Config, job models.AIJob, apiKey, modelName string) (map[string]interface{}, error) {
	var payload struct {
		JobDescription string `json:"job_description"`
		CompanyName    string `json:"company_name"`
		RecruiterEmail string `json:"recruiter_email"`
		RecruiterName  string `json:"recruiter_name"`
		RecruiterTitle string `json:"recruiter_title"`
		Location       string `json:"location"`
		LinkedinUrl    string `json:"linkedin_url"`
		UserName       string `json:"user_name"`
	}
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}

	resume, err := repository.GetUserResume(ctx, db, job.UserEmail)
	if err != nil {
		return nil, fmt.Errorf("fetch resume failed: %w", err)
	}
	if resume == nil || (resume.DriveLink == "" && resume.ResumeText == "") {
		return nil, fmt.Errorf("resume not configured")
	}

	var customPrompt string
	err = db.QueryRowContext(ctx, "SELECT custom_prompt FROM user_prompts WHERE email = ?", job.UserEmail).Scan(&customPrompt)
	if err != nil && err != sql.ErrNoRows {
		slog.Warn("Failed to query custom prompt from database", "error", err)
	}

	geminiSvc, err := llm.NewGeminiService(ctx, apiKey, modelName)
	if err != nil {
		return nil, fmt.Errorf("llm init failed: %w", err)
	}
	defer geminiSvc.Close()

	subject, body, err := geminiSvc.GenerateEmailContent(
		ctx, modelName,
		payload.JobDescription, payload.CompanyName, payload.RecruiterName, payload.UserName, job.UserEmail, resume.ResumeText, resume.DriveLink, customPrompt,
	)
	if err != nil {
		return nil, fmt.Errorf("pitch generation failed: %w", err)
	}

	return map[string]interface{}{
		"subject": subject,
		"body":    body,
	}, nil
}

func handleGenerateReminders(ctx context.Context, db *sql.DB, cfg *config.Config, job models.AIJob, apiKey, modelName string) (map[string]interface{}, error) {
	// Call the existing generateDraftsForUser logic
	// However, generateDraftsForUser from reminder_handler.go is in the api package.
	// It's probably better to copy it or move it to a shared place.
	// Wait, I can just implement it directly here to decouple.
	emails, err := repository.GetOutreachEmailsByUser(ctx, db, job.UserEmail)
	if err != nil {
		return nil, fmt.Errorf("fetch emails failed: %w", err)
	}

	geminiSvc, err := llm.NewGeminiService(ctx, apiKey, modelName)
	if err != nil {
		return nil, fmt.Errorf("llm init failed: %w", err)
	}
	defer geminiSvc.Close()

	generated := 0
	now := time.Now()

	for _, e := range emails {
		if e.Status == "replied" || e.Status == "closed" || e.Status == "ghosted" {
			continue
		}
		daysSinceSent := int(now.Sub(e.SentAt).Hours() / 24)

		if e.Status == "awaiting_reply" && daysSinceSent >= e.Reminder1DelayDays {
			subject, body, genErr := geminiSvc.GenerateFollowUpEmail(
				ctx, modelName,
				e.RecruiterName, e.CompanyName, job.UserEmail, job.UserEmail,
				daysSinceSent, e.Subject, e.Body, 1,
			)
			if genErr != nil {
				continue
			}
			_, dbErr := repository.CreateReminderDraft(ctx, db, &repository.ReminderDraft{
				OutreachEmailID: e.ID,
				UserEmail:       job.UserEmail,
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

		if e.Status == "reminder_1_sent" && e.Reminder1SentAt != nil {
			daysSinceR1 := int(now.Sub(*e.Reminder1SentAt).Hours() / 24)
			if daysSinceR1 >= e.Reminder2DelayDays {
				subject, body, genErr := geminiSvc.GenerateFollowUpEmail(
					ctx, modelName,
					e.RecruiterName, e.CompanyName, job.UserEmail, job.UserEmail,
					daysSinceSent, e.Subject, e.Body, 2,
				)
				if genErr != nil {
					continue
				}
				_, dbErr := repository.CreateReminderDraft(ctx, db, &repository.ReminderDraft{
					OutreachEmailID: e.ID,
					UserEmail:       job.UserEmail,
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
	}

	return map[string]interface{}{
		"generated": generated,
	}, nil
}

func handleExtractTextRecruiters(ctx context.Context, db *sql.DB, cfg *config.Config, job models.AIJob, apiKey, modelName string, settings *models.UserAISettings) (map[string]interface{}, error) {
	var payload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}

	chunks := parser.ChunkText(payload.Text, 6000)

	llmSvc, err := llm.NewGeminiService(ctx, apiKey, modelName)
	if err != nil {
		return nil, fmt.Errorf("llm init failed: %w", err)
	}
	defer llmSvc.Close()

	var limiter *rate.Limiter
	if settings.RateLimitRequests > 0 && settings.RateLimitIntervalSeconds > 0 {
		limit := rate.Limit(float64(settings.RateLimitRequests) / float64(settings.RateLimitIntervalSeconds))
		limiter = rate.NewLimiter(limit, settings.RateLimitRequests)
	}

	var allRecruiters []models.Recruiter
	for _, chunk := range chunks {
		if limiter != nil {
			_ = limiter.Wait(ctx)
		}
		recruiters, err := llmSvc.ExtractRecruiters(ctx, chunk)
		if err != nil {
			slog.Warn("Chunk extraction failed", "error", err)
			continue
		}
		allRecruiters = append(allRecruiters, recruiters...)
	}

	return map[string]interface{}{
		"recruiters": allRecruiters,
	}, nil
}
