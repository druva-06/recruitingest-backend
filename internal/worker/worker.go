package worker

import (
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
	"runtime/debug"

	"github.com/druva-06/recruitingest-backend/config"
	"github.com/druva-06/recruitingest-backend/internal/llm"
	"github.com/druva-06/recruitingest-backend/internal/models"
	"github.com/druva-06/recruitingest-backend/internal/parser"
	"github.com/druva-06/recruitingest-backend/internal/pdfparser"
	"github.com/druva-06/recruitingest-backend/internal/repository"
	"golang.org/x/time/rate"
)

// ProcessPDFWorker handles the async extraction, chunking, LLM processing, and database persistence.
func ProcessPDFWorker(jobID, filePath string, apiKey, modelName string, rateLimitRequests, rateLimitInterval int, cfg *config.Config, db *sql.DB) {
	// CRITICAL PRODUCTION GUARDRAIL: Recover from panics to keep main server online.
	defer func() {
		if r := recover(); r != nil {
			slog.Error("Panic in worker for Job", "jobID", jobID, "panic", r, "stack", string(debug.Stack()))
			_ = repository.UpdateJobStatus(context.Background(), db, jobID, "failed")
		}
	}()

	ctx := context.Background()
	_ = repository.UpdateJobStatus(ctx, db, jobID, "processing")
	slog.Info("Starting processing for Job", "jobID", jobID, "filePath", filePath)

	// 1. Extract Text
	text, err := pdfparser.ExtractText(filePath)
	if err != nil {
		slog.Error("Error extracting text for Job", "jobID", jobID, "error", err)
		_ = repository.UpdateJobStatus(ctx, db, jobID, "failed")
		return
	}
	slog.Info("Successfully extracted text", "length", len(text), "jobID", jobID)

	// 2. Intelligent Chunking
	chunks := parser.ChunkText(text, 6000)
	slog.Info("Chunked text", "chunks", len(chunks), "jobID", jobID)

	// Record total chunks in the DB tracker
	_ = repository.SetJobTotalChunks(ctx, db, jobID, len(chunks))

	// 3. LLM Integration
	llmSvc, err := llm.NewGeminiService(ctx, apiKey, modelName)
	if err != nil {
		slog.Error("Failed to initialize LLM service for Job", "jobID", jobID, "error", err)
		_ = repository.UpdateJobStatus(ctx, db, jobID, "failed")
		return
	}
	defer llmSvc.Close()

	// Initialize rate limiter if configured
	var limiter *rate.Limiter
	if rateLimitRequests > 0 && rateLimitInterval > 0 {
		limit := rate.Limit(float64(rateLimitRequests) / float64(rateLimitInterval))
		limiter = rate.NewLimiter(limit, rateLimitRequests)
		slog.Info("Job using rate limiter", "jobID", jobID, "requests", rateLimitRequests, "intervalSeconds", rateLimitInterval, "limitPerSec", float64(limit))
	}

	var allRecruiters []models.Recruiter
	for i, chunk := range chunks {
		slog.Info("Job processing chunk", "jobID", jobID, "chunkIndex", i+1, "totalChunks", len(chunks))

		// Wait for rate limiter if active
		if limiter != nil {
			slog.Info("Job waiting for rate limiter token", "jobID", jobID)
			if err := limiter.Wait(ctx); err != nil {
				slog.Error("Job rate limiter error", "jobID", jobID, "error", err)
			}
		}

		recruiters, err := llmSvc.ExtractRecruiters(ctx, chunk)
		if err != nil {
			slog.Warn("Job failed to extract from chunk", "jobID", jobID, "chunkIndex", i+1, "error", err)
			continue // Log and continue to next chunk; don't halt the entire file
		}

		allRecruiters = append(allRecruiters, recruiters...)

		// Increment the processed chunks count after successful extraction
		_ = repository.IncrementJobProgress(ctx, db, jobID)
	}

	// 4. Database Persistence
	if len(allRecruiters) > 0 {
		sourceFileName := filepath.Base(filePath)
		insertedCount, err := repository.BulkInsertRecruiters(ctx, db, allRecruiters, sourceFileName)
		if err != nil {
			slog.Error("Job failed during database bulk insert", "jobID", jobID, "error", err)
			_ = repository.UpdateJobStatus(ctx, db, jobID, "failed")
			return
		}

		slog.Info("Job successfully processed file", "jobID", jobID, "fileName", sourceFileName, "extractedCount", len(allRecruiters), "insertedCount", insertedCount)
	} else {
		slog.Info("Job completed processing but no valid recruiter contacts were extracted", "jobID", jobID)
	}

	// 5. Final Success Status
	_ = repository.UpdateJobStatus(ctx, db, jobID, "completed")
}
