package config

import (
	"log/slog"
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
		slog.Error("Missing required environment variable", "variable", "DATABASE_DSN")
		os.Exit(1)
	}
	
	if !strings.Contains(dbDSN, "parseTime=true") {
		if strings.Contains(dbDSN, "?") {
			dbDSN += "&parseTime=true"
		} else {
			dbDSN += "?parseTime=true"
		}
	}

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		slog.Info("GEMINI_API_KEY not set. API key must be provided by frontend request headers.")
	}

	prospeoKey := os.Getenv("PROSPEO_API_KEY")
	if prospeoKey == "" {
		slog.Info("PROSPEO_API_KEY not set. API key must be provided by frontend request headers.")
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
		slog.Warn("GOOGLE_CLIENT_ID not set — Google OAuth will not function.")
	}

	googleClientSecret := os.Getenv("GOOGLE_CLIENT_SECRET")
	if googleClientSecret == "" {
		slog.Warn("GOOGLE_CLIENT_SECRET not set — Google OAuth will not function.")
	}

	sessionSecret := os.Getenv("SESSION_SECRET")
	if sessionSecret == "" {
		slog.Error("Missing required environment variable", "variable", "SESSION_SECRET")
		os.Exit(1)
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
		slog.Warn("OAUTH_ALLOWED_EMAILS not set — no users will be allowed to log in.")
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
