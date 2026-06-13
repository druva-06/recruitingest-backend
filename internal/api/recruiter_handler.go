package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/mail"
	"strconv"
	"strings"

	"github.com/druva-06/recruitingest-backend/internal/models"
	"github.com/druva-06/recruitingest-backend/internal/repository"
	"github.com/go-sql-driver/mysql"
)

type recruiterListResponse struct {
	Recruiters []models.RecruiterRecord `json:"recruiters"`
	Total      int                      `json:"total"`
	Page       int                      `json:"page"`
	Limit      int                      `json:"limit"`
}

// NewRecruiterHandler supports manual creation and flexible recruiter search.
func NewRecruiterHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			slog.Debug("Handling GET recruiter request")
			searchRecruiters(w, r, db)
		case http.MethodPost:
			slog.Debug("Handling POST recruiter request")
			createRecruiter(w, r, db)
		default:
			slog.Warn("Method not allowed on recruiter endpoint", "method", r.Method)
			writeJSONError(w, http.StatusMethodNotAllowed, "Only GET and POST methods are allowed")
		}
	}
}

func searchRecruiters(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	slog.Info("Executing searchRecruiters handler")
	limit := 50
	if requestedLimit, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && requestedLimit > 0 {
		if requestedLimit > 100 {
			requestedLimit = 100
		}
		limit = requestedLimit
	}

	page := 1
	if requestedPage, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && requestedPage > 0 {
		page = requestedPage
	}

	offset := (page - 1) * limit
	slog.Debug("Parsed pagination params", "limit", limit, "page", page, "offset", offset)

	recruiters, total, err := repository.SearchRecruiters(
		r.Context(),
		db,
		strings.TrimSpace(r.URL.Query().Get("q")),
		strings.TrimSpace(r.URL.Query().Get("company")),
		strings.TrimSpace(r.URL.Query().Get("email")),
		limit,
		offset,
	)
	if err != nil {
		slog.Error("Database query for recruiters failed", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "Failed to search recruiters")
		return
	}
	slog.Info("Successfully searched recruiters", "count", len(recruiters), "total", total)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(recruiterListResponse{
		Recruiters: recruiters,
		Total:      total,
		Page:       page,
		Limit:      limit,
	})
}

func createRecruiter(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	slog.Info("Executing createRecruiter handler")
	var recruiter models.Recruiter
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&recruiter); err != nil {
		slog.Warn("Failed to decode recruiter payload", "error", err)
		writeJSONError(w, http.StatusBadRequest, "Invalid recruiter payload")
		return
	}
	slog.Debug("Decoded request payload", "recruiter_email", recruiter.Email)

	recruiter.Name = strings.TrimSpace(recruiter.Name)
	recruiter.Title = strings.TrimSpace(recruiter.Title)
	recruiter.Email = strings.ToLower(strings.TrimSpace(recruiter.Email))
	recruiter.Company = strings.TrimSpace(recruiter.Company)

	if recruiter.Name == "" || recruiter.Email == "" {
		slog.Warn("Validation failed: missing name or email")
		writeJSONError(w, http.StatusBadRequest, "Recruiter name and email are required")
		return
	}
	if _, err := mail.ParseAddress(recruiter.Email); err != nil {
		slog.Warn("Validation failed: invalid email format", "email", recruiter.Email)
		writeJSONError(w, http.StatusBadRequest, "Enter a valid recruiter email")
		return
	}

	slog.Debug("Validating and inserting recruiter into database")
	created, err := repository.CreateRecruiter(r.Context(), db, recruiter)
	if err != nil {
		var mysqlErr *mysql.MySQLError
		if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
			slog.Warn("Conflict: recruiter already exists", "email", recruiter.Email)
			writeJSONError(w, http.StatusConflict, "A recruiter with this email already exists")
			return
		}
		slog.Error("Database insert for recruiter failed", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "Failed to create recruiter")
		return
	}
	slog.Info("Successfully created recruiter record", "recruiter_id", created.ID)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(created)
}

// NewExtractTextHandler handles POST /api/v1/extract-recruiters
func NewExtractTextHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slog.Info("Executing NewExtractTextHandler")
		if r.Method != http.MethodPost {
			slog.Warn("Method not allowed", "method", r.Method)
			writeJSONError(w, http.StatusMethodNotAllowed, "Only POST method is allowed")
			return
		}

		session := SessionFromContext(r.Context())
		if session == nil {
			slog.Warn("Unauthorized access attempt")
			writeJSONError(w, http.StatusUnauthorized, "Not authenticated")
			return
		}
		slog.Debug("Authenticated session found", "email", session.Email)

		var payload struct {
			Text string `json:"text"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		if err := decoder.Decode(&payload); err != nil {
			slog.Warn("Failed to decode text extraction payload", "error", err)
			writeJSONError(w, http.StatusBadRequest, "Invalid JSON payload")
			return
		}

		if strings.TrimSpace(payload.Text) == "" {
			slog.Warn("Validation failed: text is empty")
			writeJSONError(w, http.StatusBadRequest, "Text cannot be empty")
			return
		}
		slog.Debug("Decoded request payload successfully")

		payloadBytes, _ := json.Marshal(map[string]string{
			"text": payload.Text,
		})

		jobID, err := repository.CreateAIJob(r.Context(), db, session.Email, "extract_text_recruiters", payloadBytes)
		if err != nil {
			slog.Error("Failed to create AI job for text extraction", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "Failed to create AI job")
			return
		}
		slog.Info("Successfully enqueued extraction AI job", "job_id", jobID)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "processing",
			"message": "Text accepted successfully. Extraction is executing in the background.",
			"job_id":  jobID,
		})
	}
}

// NewBulkRecruiterHandler handles POST /api/v1/recruiters/bulk
func NewBulkRecruiterHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slog.Info("Executing NewBulkRecruiterHandler")
		if r.Method != http.MethodPost {
			slog.Warn("Method not allowed", "method", r.Method)
			writeJSONError(w, http.StatusMethodNotAllowed, "Only POST method is allowed")
			return
		}

		var payload struct {
			Recruiters []models.Recruiter `json:"recruiters"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 5<<20))
		if err := decoder.Decode(&payload); err != nil {
			slog.Warn("Failed to decode bulk recruiter payload", "error", err)
			writeJSONError(w, http.StatusBadRequest, "Invalid payload")
			return
		}

		if len(payload.Recruiters) == 0 {
			slog.Warn("Validation failed: no recruiters in payload")
			writeJSONError(w, http.StatusBadRequest, "No recruiters provided")
			return
		}
		slog.Debug("Filtering out empty recruiters", "input_count", len(payload.Recruiters))

		// Ensure we don't have panics from empty fields
		var toInsert []models.Recruiter
		for _, rec := range payload.Recruiters {
			rec.Name = strings.TrimSpace(rec.Name)
			rec.Email = strings.ToLower(strings.TrimSpace(rec.Email))
			if rec.Name != "" && rec.Email != "" {
				toInsert = append(toInsert, rec)
			}
		}
		slog.Debug("Initiating bulk insert", "valid_count", len(toInsert))

		insertedCount, err := repository.BulkInsertRecruiters(r.Context(), db, toInsert, "ai-paste")
		if err != nil {
			slog.Error("Failed to bulk insert recruiters", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "Failed to bulk insert recruiters")
			return
		}
		slog.Info("Successfully inserted bulk recruiters", "inserted_count", insertedCount)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"inserted": insertedCount,
		})
	}
}
