package main

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"time"

	"github.com/druva-06/recruitingest-backend/config"
	"github.com/druva-06/recruitingest-backend/internal/api"
	"github.com/druva-06/recruitingest-backend/internal/repository"
	_ "github.com/go-sql-driver/mysql"
)

func main() {
	// 1. Initialize Configuration Engine
	log.Println("Initializing configuration engine...")
	cfg := config.Load()
	log.Println("Configuration loaded successfully.")

	// 2. Initialize Database Connection
	log.Println("Initializing MySQL connection...")
	db, err := sql.Open("mysql", cfg.DatabaseDSN)
	if err != nil {
		log.Fatalf("[CRITICAL] Failed to open database: %v", err)
	}
	if err := db.Ping(); err != nil {
		log.Fatalf("[CRITICAL] Failed to ping database: %v", err)
	}
	defer db.Close()
	log.Println("Successfully connected to MySQL.")

	// 3. Start background session purge goroutine (runs every hour).
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			n, err := repository.PurgeExpiredSessions(context.Background(), db)
			if err != nil {
				log.Printf("[Session] Purge error: %v", err)
			} else if n > 0 {
				log.Printf("[Session] Purged %d expired session(s).", n)
			}
		}
	}()

	// 4. Setup HTTP Infrastructure
	mux := http.NewServeMux()

	// -- Auth routes (no auth required) --
	mux.HandleFunc("GET /api/v1/auth/login", api.NewLoginHandler(cfg))
	mux.HandleFunc("GET /api/v1/auth/callback", api.NewCallbackHandler(cfg, db))
	mux.HandleFunc("GET /api/v1/auth/me", api.NewMeHandler(db))
	mux.HandleFunc("POST /api/v1/auth/logout", api.NewLogoutHandler(db))

	// -- Protected routes (RequireAuth middleware) --
	auth := api.RequireAuth(db)
	mux.Handle("POST /api/v1/upload", auth(http.HandlerFunc(api.NewUploadHandler(cfg, db))))
	mux.Handle("GET /api/v1/jobs/recent", auth(http.HandlerFunc(api.NewRecentJobsHandler(db))))
	mux.Handle("GET /api/v1/jobs/{job_id}", auth(http.HandlerFunc(api.NewJobStatusHandler(db))))
	mux.Handle("/api/v1/recruiters", auth(http.HandlerFunc(api.NewRecruiterHandler(db))))
	mux.Handle("GET /api/v1/resume", auth(http.HandlerFunc(api.NewResumeHandler(db))))
	mux.Handle("POST /api/v1/resume", auth(http.HandlerFunc(api.NewResumeHandler(db))))
	mux.Handle("POST /api/v1/outreach/search-recruiters", auth(http.HandlerFunc(api.NewOutreachSearchHandler(db))))
	mux.Handle("POST /api/v1/outreach/prospeo-enrich", auth(http.HandlerFunc(api.NewProspeoEnrichHandler(cfg))))
	mux.Handle("POST /api/v1/outreach/generate-pitch", auth(http.HandlerFunc(api.NewGeneratePitchHandler(cfg, db))))
	mux.Handle("POST /api/v1/outreach/confirm-pitch", auth(http.HandlerFunc(api.NewConfirmPitchHandler(cfg, db))))
	mux.Handle("GET /api/v1/outreach/prompt", auth(http.HandlerFunc(api.NewPromptSettingsHandler(db))))
	mux.Handle("POST /api/v1/outreach/prompt", auth(http.HandlerFunc(api.NewPromptSettingsHandler(db))))

	// 5. Start Server
	log.Printf("Server listening on port %s...\n", cfg.ServerPort)
	if err := http.ListenAndServe(cfg.ServerPort, api.WithCORS(mux, cfg.CORSAllowedOrigin)); err != nil {
		log.Fatalf("[CRITICAL] Server failed to start: %v", err)
	}
}
