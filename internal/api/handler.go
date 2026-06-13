package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/druva-06/recruitingest-backend/config"
	"github.com/druva-06/recruitingest-backend/internal/repository"
)

const maxUploadSize = 20 << 20 // 20 MB

// UploadResponse maps to the 202 Accepted return payload.
type UploadResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	JobID   string `json:"job_id"`
}

// ErrorResponse wraps standard HTTP error messages.
type ErrorResponse struct {
	Error string `json:"error"`
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(ErrorResponse{Error: msg})
}

// NewUploadHandler returns the http.HandlerFunc with dependencies injected.
func NewUploadHandler(cfg *config.Config, db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Only allow POST
		if r.Method != http.MethodPost {
			writeJSONError(w, http.StatusMethodNotAllowed, "Only POST method is allowed")
			return
		}

		session := SessionFromContext(r.Context())
		if session == nil {
			writeJSONError(w, http.StatusUnauthorized, "Not authenticated")
			return
		}

		// 1. Memory limits (Max 20MB)
		r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
		if err := r.ParseMultipartForm(maxUploadSize); err != nil {
			writeJSONError(w, http.StatusBadRequest, "File too large. Maximum size is 20MB.")
			return
		}

		// 2. Retrieve 'file' from form-data
		file, header, err := r.FormFile("file")
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "Invalid request. Expected multipart form with 'file' field.")
			return
		}
		defer file.Close()

		// 3. Validate content type & extension
		contentType := header.Header.Get("Content-Type")
		ext := strings.ToLower(filepath.Ext(header.Filename))
		if contentType != "application/pdf" && ext != ".pdf" {
			writeJSONError(w, http.StatusUnsupportedMediaType, "Only PDF files are allowed")
			return
		}

		// 4. Generate unique filename and setup local storage
		uploadDir := "/tmp/recruitingest/uploads"
		if err := os.MkdirAll(uploadDir, 0755); err != nil {
			slog.Error("Failed to create upload directory", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "Internal server error")
			return
		}

		timestamp := time.Now().UnixNano()
		safeFilename := fmt.Sprintf("%d_%s", timestamp, header.Filename)
		destPath := filepath.Join(uploadDir, safeFilename)

		destFile, err := os.Create(destPath)
		if err != nil {
			slog.Error("Failed to create destination file", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "Internal server error")
			return
		}
		defer destFile.Close()

		if _, err := io.Copy(destFile, file); err != nil {
			slog.Error("Failed to write file payload", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "Internal server error")
			return
		}

		payloadBytes, _ := json.Marshal(map[string]string{
			"file_path": destPath,
			"file_name": header.Filename,
		})

		jobID, err := repository.CreateAIJob(r.Context(), db, session.Email, "parse_resume", payloadBytes)
		if err != nil {
			slog.Error("Failed to create AI job record", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "Internal server error while creating job tracker")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)

		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "processing",
			"message": "File accepted successfully. Extraction is executing in the background.",
			"job_id":  jobID,
		})
	}
}
