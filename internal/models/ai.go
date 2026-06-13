package models

import (
	"encoding/json"
	"time"
)

type UserAISettings struct {
	UserEmail                string `json:"user_email"`
	GeminiAPIKey             string `json:"gemini_api_key"`
	GeminiModel              string `json:"gemini_model"`
	RateLimitRequests        int    `json:"rate_limit_requests"`
	RateLimitIntervalSeconds int    `json:"rate_limit_interval_seconds"`
}

type AIJob struct {
	ID           int             `json:"id"`
	UserEmail    string          `json:"user_email"`
	JobType      string          `json:"job_type"`
	Payload      json.RawMessage `json:"payload"`
	Status       string          `json:"status"`
	Result       json.RawMessage `json:"result,omitempty"`
	ErrorMessage *string         `json:"error_message,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}
