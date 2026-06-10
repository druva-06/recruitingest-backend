package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/druva-06/recruitingest-backend/internal/pdfparser"
	"github.com/druva-06/recruitingest-backend/internal/repository"
)

// NewResumeHandler manages the user's resume text extraction and Google Drive link.
func NewResumeHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := SessionFromContext(r.Context())
		if session == nil {
			writeJSONError(w, http.StatusUnauthorized, "Not authenticated")
			return
		}

		switch r.Method {
		case http.MethodGet:
			getResume(w, r, db, session.Email)
		case http.MethodPost:
			saveResume(w, r, db, session.Email)
		default:
			writeJSONError(w, http.StatusMethodNotAllowed, "Only GET and POST methods are allowed")
		}
	}
}

func getResume(w http.ResponseWriter, r *http.Request, db *sql.DB, email string) {
	resume, err := repository.GetUserResume(r.Context(), db, email)
	if err != nil {
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
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"email":           resume.Email,
		"resume_filename": resume.ResumeFilename,
		"drive_link":      resume.DriveLink,
		"has_pdf":         len(resume.ResumeText) > 0, // Keeping has_pdf for frontend compatibility
	})
}

func saveResume(w http.ResponseWriter, r *http.Request, db *sql.DB, email string) {
	// Parse multipart form up to 21MB
	err := r.ParseMultipartForm(21 << 20)
	if err != nil && err != http.ErrNotMultipart {
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
		if !strings.HasSuffix(strings.ToLower(filename), ".pdf") {
			writeJSONError(w, http.StatusBadRequest, "Only PDF files are allowed")
			return
		}

		// Write to a temporary file
		tmpFile, err := os.CreateTemp("", "user-resume-*.pdf")
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "Failed to initialize resume parser")
			return
		}
		defer os.Remove(tmpFile.Name())
		defer tmpFile.Close()

		_, err = io.Copy(tmpFile, file)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "Failed to write temp resume file")
			return
		}

		// Extract text from the PDF file using the existing parser
		resumeText, err = pdfparser.ExtractText(tmpFile.Name())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to extract text from your resume PDF: %v", err))
			return
		}
	}

	if filename == "" && driveLink == "" && resumeText == "" {
		writeJSONError(w, http.StatusBadRequest, "Provide either a PDF file or a Google Drive link")
		return
	}

	err = repository.SaveUserResume(r.Context(), db, email, filename, resumeText, driveLink)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to save resume info")
		return
	}

	getResume(w, r, db, email)
}
