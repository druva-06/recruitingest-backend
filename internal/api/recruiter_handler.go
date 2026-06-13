package api

import (
	"database/sql"
	"encoding/json"
	"errors"
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
			searchRecruiters(w, r, db)
		case http.MethodPost:
			createRecruiter(w, r, db)
		default:
			writeJSONError(w, http.StatusMethodNotAllowed, "Only GET and POST methods are allowed")
		}
	}
}

func searchRecruiters(w http.ResponseWriter, r *http.Request, db *sql.DB) {
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
		writeJSONError(w, http.StatusInternalServerError, "Failed to search recruiters")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(recruiterListResponse{
		Recruiters: recruiters,
		Total:      total,
		Page:       page,
		Limit:      limit,
	})
}

func createRecruiter(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	var recruiter models.Recruiter
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&recruiter); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid recruiter payload")
		return
	}

	recruiter.Name = strings.TrimSpace(recruiter.Name)
	recruiter.Title = strings.TrimSpace(recruiter.Title)
	recruiter.Email = strings.ToLower(strings.TrimSpace(recruiter.Email))
	recruiter.Company = strings.TrimSpace(recruiter.Company)

	if recruiter.Name == "" || recruiter.Email == "" {
		writeJSONError(w, http.StatusBadRequest, "Recruiter name and email are required")
		return
	}
	if _, err := mail.ParseAddress(recruiter.Email); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Enter a valid recruiter email")
		return
	}

	created, err := repository.CreateRecruiter(r.Context(), db, recruiter)
	if err != nil {
		var mysqlErr *mysql.MySQLError
		if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
			writeJSONError(w, http.StatusConflict, "A recruiter with this email already exists")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "Failed to create recruiter")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(created)
}

// NewExtractTextHandler handles POST /api/v1/extract-recruiters
func NewExtractTextHandler(db *sql.DB) http.HandlerFunc {
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

		var payload struct {
			Text string `json:"text"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		if err := decoder.Decode(&payload); err != nil {
			writeJSONError(w, http.StatusBadRequest, "Invalid JSON payload")
			return
		}

		if strings.TrimSpace(payload.Text) == "" {
			writeJSONError(w, http.StatusBadRequest, "Text cannot be empty")
			return
		}

		payloadBytes, _ := json.Marshal(map[string]string{
			"text": payload.Text,
		})

		jobID, err := repository.CreateAIJob(r.Context(), db, session.Email, "extract_text_recruiters", payloadBytes)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "Failed to create AI job")
			return
		}

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
		if r.Method != http.MethodPost {
			writeJSONError(w, http.StatusMethodNotAllowed, "Only POST method is allowed")
			return
		}

		var payload struct {
			Recruiters []models.Recruiter `json:"recruiters"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 5<<20))
		if err := decoder.Decode(&payload); err != nil {
			writeJSONError(w, http.StatusBadRequest, "Invalid payload")
			return
		}

		if len(payload.Recruiters) == 0 {
			writeJSONError(w, http.StatusBadRequest, "No recruiters provided")
			return
		}

		// Ensure we don't have panics from empty fields
		var toInsert []models.Recruiter
		for _, rec := range payload.Recruiters {
			rec.Name = strings.TrimSpace(rec.Name)
			rec.Email = strings.ToLower(strings.TrimSpace(rec.Email))
			if rec.Name != "" && rec.Email != "" {
				toInsert = append(toInsert, rec)
			}
		}

		insertedCount, err := repository.BulkInsertRecruiters(r.Context(), db, toInsert, "ai-paste")
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "Failed to bulk insert recruiters")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"inserted": insertedCount,
		})
	}
}
