package main

import (
	"database/sql"
	"log"
	"net/http"

	"github.com/druva-06/recruitingest-backend/config"
	"github.com/druva-06/recruitingest-backend/internal/api"
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

	// 3. Setup HTTP Infrastructure (using Go 1.22+ method matching)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/upload", api.NewUploadHandler(cfg, db))
	mux.HandleFunc("GET /api/v1/jobs/{job_id}", api.NewJobStatusHandler(db))

	// 4. Start Server
	log.Printf("Server successfully started. Listening on port %s...\n", cfg.ServerPort)
	if err := http.ListenAndServe(cfg.ServerPort, api.WithCORS(mux, cfg.CORSAllowedOrigin)); err != nil {
		log.Fatalf("[CRITICAL] Server failed to start: %v", err)
	}
}
