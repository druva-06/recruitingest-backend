# RecruitIngest: Architecture & Step-by-Step Implementation

This document provides a detailed breakdown of the codebase, explaining the architectural decisions and step-by-step implementation of the Asynchronous PDF Ingestion Engine.

## Phase 1: Database Setup & Pure Go Text Extraction
**Goal:** Prove we can extract text natively in Go and connect to the database.

*   **PDF Extraction (`internal/pdfparser/extractor.go`):** 
    We utilized `github.com/ledongthuc/pdf`, a pure Go library, to read PDFs without relying on external system dependencies (like Cgo or Python/pdftotext). The `ExtractText` function loops over every page, pulling plain text, and concatenating it into a single large string.
*   **Database Strictness:** We designed the MySQL schema with a `UNIQUE KEY` on the `recruiter_email` column. This shifts the deduplication burden from the Go backend to the database engine, ensuring absolute data integrity.

## Phase 2: Production-Grade Async Routing & Chunking
**Goal:** Build a non-blocking API that immediately frees up the client while work happens in the background.

*   **Config Engine (`config/config.go`):** 
    Utilized `godotenv` to manage secure credentials. The app performs a hard validation at startup—if the database DSN or Gemini API key is missing, it crashes *before* booting the server.
*   **HTTP Handler (`internal/api/handler.go`):**
    *   **Security:** Enforced a strict 20MB limit using `http.MaxBytesReader` to prevent memory exhaustion attacks.
    *   **Validation:** Ensures only `.pdf` files are processed.
    *   **Storage:** Saves the file to a local `/tmp/recruitingest/uploads` directory using a timestamped UUID to avoid filename collisions.
    *   **Async Response:** Immediately fires `go ProcessPDFWorker(...)` and returns a `202 Accepted` to the client.
*   **Intelligent Chunking (`internal/parser/chunker.go`):**
    To bypass Gemini's output token limits, the text is sliced into ~6,000-character chunks. Instead of blind slicing (which could cut an email address in half), the algorithm scans backward for a `\n` or whitespace to safely split chunks along natural boundaries.
*   **Worker Guardrails (`internal/worker/worker.go`):**
    The background Goroutine is wrapped in a `defer ... recover()` block. If a panic occurs during processing, it is caught and logged, guaranteeing the main HTTP web server will not crash.

## Phase 3: Structured LLM Extraction
**Goal:** Map unpredictable PDF text to a predictable JSON array.

*   **Strict Schema (`internal/models/recruiter.go`):** 
    Created a single `Recruiter` struct used for JSON unmarshaling and database insertions.
*   **Gemini Service (`internal/llm/gemini.go`):**
    *   **Structured Output:** Leveraged Gemini 1.5 Flash's `ResponseSchema` capability. We programmatically defined the output to strictly be a JSON Array of Objects (Name, Title, Email, Company). This prevents the LLM from outputting conversational filler like "Here is your data:".
    *   **Resiliency (Retries & Backoff):** Network calls fail. The API call is wrapped in a loop. If Gemini returns a 429 (Rate Limit) or 503 (Unavailable), the system waits (2s, 4s, 8s) and retries before giving up.
    *   **Timeout:** Applied a 60-second `context.WithTimeout` per chunk to ensure the background worker doesn't hang indefinitely if the Google API stops responding.

## Phase 4: Bulk Database Persistence
**Goal:** Write thousands of extracted rows efficiently without duplicating data.

*   **Batching (`internal/repository/db.go`):** 
    Sending 5,000 records in a single query can crash MySQL due to `max_allowed_packet` limits. The `BulkInsertRecruiters` function groups records into exact batches of 500.
*   **Dynamic SQL & INSERT IGNORE:**
    Constructs a massive `INSERT IGNORE INTO recruiters (...) VALUES (?,?,?), (?,?,?)...` statement dynamically. The `IGNORE` keyword tells MySQL to silently drop rows where the email already exists, satisfying our deduplication requirement.
*   **Transactions:**
    Wrapped the batching loop in an explicit `db.BeginTx()` transaction. If *any* batch fails, `tx.Rollback()` drops the incomplete data. If all succeed, `tx.Commit()` seals it. 
*   **Row Tracking:**
    The system reads `RowsAffected()` from the driver. Even if 1,000 recruiters were found in the PDF, if 900 were duplicates, it accurately logs: `"Extracted 1000 contacts, inserted 100 new unique records."`