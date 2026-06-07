package main

import (
	"database/sql"
	"log"
	"net/http"

	"github.com/druva06/recruit-ingest/config"
	"github.com/druva06/recruit-ingest/internal/api"
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

	// 3. Setup HTTP Infrastructure
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/upload", api.NewUploadHandler(cfg, db))

	// 4. Start Server
	log.Printf("Server successfully started. Listening on port %s...\n", cfg.ServerPort)
	if err := http.ListenAndServe(cfg.ServerPort, mux); err != nil {
		log.Fatalf("[CRITICAL] Server failed to start: %v", err)
	}
}
