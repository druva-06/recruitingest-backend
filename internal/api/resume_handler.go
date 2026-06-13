package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/druva-06/recruitingest-backend/internal/pdfparser"
	"github.com/druva-06/recruitingest-backend/internal/repository"
)

// NewResumeHandler manages the user's resume text extraction and Google Drive link.
func NewResumeHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slog.Info("Executing NewResumeHandler")
		session := SessionFromContext(r.Context())
		if session == nil {
			slog.Warn("Unauthorized access attempt")
			writeJSONError(w, http.StatusUnauthorized, "Not authenticated")
			return
		}
		slog.Debug("Authenticated session found", "email", session.Email)

		switch r.Method {
		case http.MethodGet:
			slog.Debug("Handling GET resume request")
			getResume(w, r, db, session.Email)
		case http.MethodPost:
			slog.Debug("Handling POST resume request")
			saveResume(w, r, db, session.Email)
		default:
			slog.Warn("Method not allowed on resume endpoint", "method", r.Method)
			writeJSONError(w, http.StatusMethodNotAllowed, "Only GET and POST methods are allowed")
		}
	}
}

func getResume(w http.ResponseWriter, r *http.Request, db *sql.DB, email string) {
	slog.Info("Retrieving user resume", "email", email)
	resume, err := repository.GetUserResume(r.Context(), db, email)
	if err != nil {
		slog.Error("Database query for user resume failed", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "Failed to retrieve resume info")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if resume == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"email":           email,
			"resume_filename": "",
			"drive_link":      "",
			"has_pdf":         false,
		})
		slog.Debug("No resume found for user, returning defaults")
		return
	}

	slog.Info("Successfully retrieved resume info", "filename", resume.ResumeFilename, "has_drive_link", resume.DriveLink != "")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"email":           resume.Email,
		"resume_filename": resume.ResumeFilename,
		"drive_link":      resume.DriveLink,
		"has_pdf":         len(resume.ResumeText) > 0, // Keeping has_pdf for frontend compatibility
	})
}

func saveResume(w http.ResponseWriter, r *http.Request, db *sql.DB, email string) {
	slog.Info("Saving user resume info", "email", email)
	// Parse multipart form up to 21MB
	err := r.ParseMultipartForm(21 << 20)
	if err != nil && err != http.ErrNotMultipart {
		slog.Warn("Failed to parse multipart form", "error", err)
		writeJSONError(w, http.StatusBadRequest, "Failed to parse form")
		return
	}

	driveLink := strings.TrimSpace(r.FormValue("drive_link"))
	var filename string
	var resumeText string

	file, header, err := r.FormFile("file")
	if err == nil {
		defer file.Close()
		filename = header.Filename
		slog.Debug("Resume file provided in request", "filename", filename)
		if !strings.HasSuffix(strings.ToLower(filename), ".pdf") {
			slog.Warn("Validation failed: file is not a PDF", "filename", filename)
			writeJSONError(w, http.StatusBadRequest, "Only PDF files are allowed")
			return
		}

		// Write to a temporary file
		tmpFile, err := os.CreateTemp("", "user-resume-*.pdf")
		if err != nil {
			slog.Error("Failed to create temporary file for resume", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "Failed to initialize resume parser")
			return
		}
		defer os.Remove(tmpFile.Name())
		defer tmpFile.Close()

		_, err = io.Copy(tmpFile, file)
		if err != nil {
			slog.Error("Failed to copy uploaded file to temporary file", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "Failed to write temp resume file")
			return
		}
		slog.Debug("Successfully saved temporary resume file", "temp_file", tmpFile.Name())

		// Extract text from the PDF file using the existing parser
		slog.Info("Extracting text from resume PDF")
		resumeText, err = pdfparser.ExtractText(tmpFile.Name())
		if err != nil {
			slog.Error("Failed to extract text from PDF", "error", err)
			writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to extract text from your resume PDF: %v", err))
			return
		}
		slog.Debug("Extracted text successfully", "text_length", len(resumeText))
	} else {
		slog.Debug("No file provided in request, checking for drive link", "drive_link", driveLink)
	}

	if filename == "" && driveLink == "" && resumeText == "" {
		slog.Warn("Validation failed: missing both file and drive link")
		writeJSONError(w, http.StatusBadRequest, "Provide either a PDF file or a Google Drive link")
		return
	}

	slog.Debug("Saving user resume to database")
	err = repository.SaveUserResume(r.Context(), db, email, filename, resumeText, driveLink)
	if err != nil {
		slog.Error("Failed to save resume into database", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "Failed to save resume info")
		return
	}
	slog.Info("Successfully saved user resume info", "filename", filename)

	getResume(w, r, db, email)
}
