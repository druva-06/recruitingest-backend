package config

import (
	"log"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

// Config holds the application configuration.
type Config struct {
	ServerPort         string
	DatabaseDSN        string
	GeminiAPIKey       string
	ProspeoAPIKey      string
	GeminiModel        string
	CORSAllowedOrigin  string
	GoogleClientID     string
	GoogleClientSecret string
	SessionSecret      string
	OAuthAllowedEmails []string
	OAuthCallbackURL   string
	FrontendURL        string
}

// Load initializes and validates the configuration from environment variables.
func Load() *Config {
	// Load .env if present; ignore error if file doesn't exist
	_ = godotenv.Load()

	port := os.Getenv("SERVER_PORT")
	if port == "" {
		port = ":8080"
	} else if port[0] != ':' {
		port = ":" + port
	}

	dbDSN := os.Getenv("DATABASE_DSN")
	if dbDSN == "" {
		log.Fatal("[CRITICAL] Missing required environment variable: DATABASE_DSN")
	}

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		log.Println("[INFO] GEMINI_API_KEY not set. API key must be provided by frontend request headers.")
	}

	prospeoKey := os.Getenv("PROSPEO_API_KEY")
	if prospeoKey == "" {
		log.Println("[INFO] PROSPEO_API_KEY not set. API key must be provided by frontend request headers.")
	}

	model := os.Getenv("GEMINI_MODEL")
	if model == "" {
		model = "gemini-3.1-flash-lite"
	}

	allowedOrigin := os.Getenv("CORS_ALLOWED_ORIGIN")
	if allowedOrigin == "" {
		allowedOrigin = "http://localhost:5173"
	}

	googleClientID := os.Getenv("GOOGLE_CLIENT_ID")
	if googleClientID == "" {
		log.Println("[WARN] GOOGLE_CLIENT_ID not set — Google OAuth will not function.")
	}

	googleClientSecret := os.Getenv("GOOGLE_CLIENT_SECRET")
	if googleClientSecret == "" {
		log.Println("[WARN] GOOGLE_CLIENT_SECRET not set — Google OAuth will not function.")
	}

	sessionSecret := os.Getenv("SESSION_SECRET")
	if sessionSecret == "" {
		log.Fatal("[CRITICAL] Missing required environment variable: SESSION_SECRET (run: openssl rand -hex 32)")
	}

	callbackURL := os.Getenv("OAUTH_CALLBACK_URL")
	if callbackURL == "" {
		callbackURL = "http://localhost:8080/api/v1/auth/callback"
	}

	allowedEmailsRaw := os.Getenv("OAUTH_ALLOWED_EMAILS")
	var allowedEmails []string
	for _, e := range strings.Split(allowedEmailsRaw, ",") {
		e = strings.TrimSpace(strings.ToLower(e))
		if e != "" {
			allowedEmails = append(allowedEmails, e)
		}
	}
	if len(allowedEmails) == 0 {
		log.Println("[WARN] OAUTH_ALLOWED_EMAILS not set — no users will be allowed to log in.")
	}

	frontendURL := os.Getenv("FRONTEND_URL")
	if frontendURL == "" {
		frontendURL = "http://localhost:5173" // Vite dev server default
	}

	return &Config{
		ServerPort:         port,
		DatabaseDSN:        dbDSN,
		GeminiAPIKey:       apiKey,
		ProspeoAPIKey:      prospeoKey,
		GeminiModel:        model,
		CORSAllowedOrigin:  allowedOrigin,
		GoogleClientID:     googleClientID,
		GoogleClientSecret: googleClientSecret,
		SessionSecret:      sessionSecret,
		OAuthAllowedEmails: allowedEmails,
		OAuthCallbackURL:   callbackURL,
		FrontendURL:        frontendURL,
	}
}
