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
	Count      int                      `json:"count"`
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

	recruiters, err := repository.SearchRecruiters(
		r.Context(),
		db,
		strings.TrimSpace(r.URL.Query().Get("q")),
		strings.TrimSpace(r.URL.Query().Get("company")),
		strings.TrimSpace(r.URL.Query().Get("email")),
		limit,
	)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to search recruiters")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(recruiterListResponse{Recruiters: recruiters, Count: len(recruiters)})
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
