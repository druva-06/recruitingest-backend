package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/druva-06/recruitingest-backend/internal/models"
)

func GetUserAISettings(ctx context.Context, db *sql.DB, email string) (*models.UserAISettings, error) {
	var settings models.UserAISettings
	err := db.QueryRowContext(
		ctx,
		"SELECT user_email, COALESCE(gemini_api_key, ''), COALESCE(gemini_model, ''), rate_limit_requests, rate_limit_interval_seconds FROM user_ai_settings WHERE user_email = ?",
		email,
	).Scan(&settings.UserEmail, &settings.GeminiAPIKey, &settings.GeminiModel, &settings.RateLimitRequests, &settings.RateLimitIntervalSeconds)

	if err == sql.ErrNoRows {
		return nil, nil // Not found
	} else if err != nil {
		return nil, fmt.Errorf("failed to get AI settings: %w", err)
	}

	return &settings, nil
}

func UpsertUserAISettings(ctx context.Context, db *sql.DB, settings *models.UserAISettings) error {
	_, err := db.ExecContext(
		ctx,
		`INSERT INTO user_ai_settings (user_email, gemini_api_key, gemini_model, rate_limit_requests, rate_limit_interval_seconds) 
		VALUES (?, ?, ?, ?, ?) 
		ON DUPLICATE KEY UPDATE 
		gemini_api_key = VALUES(gemini_api_key), 
		gemini_model = VALUES(gemini_model), 
		rate_limit_requests = VALUES(rate_limit_requests), 
		rate_limit_interval_seconds = VALUES(rate_limit_interval_seconds)`,
		settings.UserEmail, settings.GeminiAPIKey, settings.GeminiModel, settings.RateLimitRequests, settings.RateLimitIntervalSeconds,
	)
	if err != nil {
		return fmt.Errorf("failed to upsert AI settings: %w", err)
	}
	return nil
}
