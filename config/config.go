package config

import (
	"log"
	"os"

	"github.com/joho/godotenv"
)

// Config holds the application configuration.
type Config struct {
	ServerPort        string
	DatabaseDSN       string
	GeminiAPIKey      string
	GeminiModel       string
	CORSAllowedOrigin string
}

// Load initializes and validates the configuration from environment variables.
func Load() *Config {
	// Load .env if present; ignore error if file doesn't exist
	_ = godotenv.Load()

	port := os.Getenv("SERVER_PORT")
	if port == "" {
		port = ":8080"
	} else if port[0] != ':' {
		// Ensure the port always starts with a colon (e.g. "8080" becomes ":8080")
		port = ":" + port
	}

	dbDSN := os.Getenv("DATABASE_DSN")
	if dbDSN == "" {
		log.Fatal("[CRITICAL] Missing required environment variable: DATABASE_DSN")
	}

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		log.Println("[INFO] GEMINI_API_KEY environment variable is not set. API key must be provided by frontend request headers.")
	}

	model := os.Getenv("GEMINI_MODEL")
	if model == "" {
		model = "gemini-3.5-flash" // default fallback
	}

	allowedOrigin := os.Getenv("CORS_ALLOWED_ORIGIN")
	if allowedOrigin == "" {
		allowedOrigin = "http://localhost:5173"
	}

	return &Config{
		ServerPort:        port,
		DatabaseDSN:       dbDSN,
		GeminiAPIKey:      apiKey,
		GeminiModel:       model,
		CORSAllowedOrigin: allowedOrigin,
	}
}
