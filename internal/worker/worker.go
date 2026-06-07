package worker

import (
	"context"
	"database/sql"
	"log"
	"path/filepath"
	"runtime/debug"

	"github.com/druva06/recruit-ingest/config"
	"github.com/druva06/recruit-ingest/internal/llm"
	"github.com/druva06/recruit-ingest/internal/models"
	"github.com/druva06/recruit-ingest/internal/parser"
	"github.com/druva06/recruit-ingest/internal/pdfparser"
	"github.com/druva06/recruit-ingest/internal/repository"
)

// ProcessPDFWorker handles the async extraction, chunking, LLM processing, and database persistence.
func ProcessPDFWorker(jobID, filePath string, cfg *config.Config, db *sql.DB) {
	// CRITICAL PRODUCTION GUARDRAIL: Recover from panics to keep main server online.
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[CRITICAL] Panic in worker for Job %s: %v\nStack Trace:\n%s\n", jobID, r, debug.Stack())
		}
	}()

	log.Printf("[Worker] Starting processing for Job %s. File: %s\n", jobID, filePath)

	// 1. Extract Text
	text, err := pdfparser.ExtractText(filePath)
	if err != nil {
		log.Printf("[Worker] Error extracting text for Job %s: %v\n", jobID, err)
		return
	}
	log.Printf("[Worker] Successfully extracted %d characters for Job %s\n", len(text), jobID)

	// 2. Intelligent Chunking
	chunks := parser.ChunkText(text, 6000)
	log.Printf("[Worker] Chunked text into %d parts for Job %s\n", len(chunks), jobID)

	// 3. LLM Integration
	ctx := context.Background()
	llmSvc, err := llm.NewGeminiService(ctx, cfg.GeminiAPIKey, cfg.GeminiModel)
	if err != nil {
		log.Printf("[Worker] Failed to initialize LLM service for Job %s: %v\n", jobID, err)
		return
	}
	defer llmSvc.Close()

	var allRecruiters []models.Recruiter
	for i, chunk := range chunks {
		log.Printf("[Worker] Job %s processing chunk %d/%d...\n", jobID, i+1, len(chunks))
		
		recruiters, err := llmSvc.ExtractRecruiters(ctx, chunk)
		if err != nil {
			log.Printf("[Worker] Warning: Job %s failed to extract from chunk %d: %v\n", jobID, i+1, err)
			continue // Log and continue to next chunk; don't halt the entire file
		}
		
		allRecruiters = append(allRecruiters, recruiters...)
	}

	// 4. Database Persistence
	if len(allRecruiters) > 0 {
		sourceFileName := filepath.Base(filePath)
		insertedCount, err := repository.BulkInsertRecruiters(ctx, db, allRecruiters, sourceFileName)
		if err != nil {
			log.Printf("[Worker] Job %s failed during database bulk insert: %v\n", jobID, err)
			return
		}
		
		log.Printf("[Worker] Job %s Successfully processed %s. Extracted %d total contacts, inserted %d new unique records.\n", 
			jobID, sourceFileName, len(allRecruiters), insertedCount)
	} else {
		log.Printf("[Worker] Job %s completed processing but no valid recruiter contacts were extracted.\n", jobID)
	}
}
