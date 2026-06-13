package repository

import (
	"context"
	"database/sql"
)

// ReminderSettings holds the per-user global follow-up delay preferences.
type ReminderSettings struct {
	Email              string `json:"email"`
	Reminder1DelayDays int    `json:"reminder1_delay_days"`
	Reminder2DelayDays int    `json:"reminder2_delay_days"`
}

// GetReminderSettings retrieves settings for a user, returning defaults if not set.
func GetReminderSettings(ctx context.Context, db *sql.DB, email string) (*ReminderSettings, error) {
	s := &ReminderSettings{
		Email:              email,
		Reminder1DelayDays: 5,
		Reminder2DelayDays: 10,
	}
	err := db.QueryRowContext(ctx,
		"SELECT reminder1_delay_days, reminder2_delay_days FROM user_reminder_settings WHERE email=?",
		email).Scan(&s.Reminder1DelayDays, &s.Reminder2DelayDays)
	if err == sql.ErrNoRows {
		return s, nil // return defaults
	}
	if err != nil {
		return s, err
	}
	return s, nil
}

// SaveReminderSettings upserts the settings for a user.
func SaveReminderSettings(ctx context.Context, db *sql.DB, s *ReminderSettings) error {
	const q = `
		INSERT INTO user_reminder_settings (email, reminder1_delay_days, reminder2_delay_days)
		VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE
			reminder1_delay_days = VALUES(reminder1_delay_days),
			reminder2_delay_days = VALUES(reminder2_delay_days)
	`
	_, err := db.ExecContext(ctx, q, s.Email, s.Reminder1DelayDays, s.Reminder2DelayDays)
	return err
}
