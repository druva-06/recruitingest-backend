package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Session represents a single authenticated user session stored in MySQL.
type Session struct {
	SessionID    string
	Email        string
	Name         string
	Picture      string
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
}

// CreateSession persists a new session row.
func CreateSession(ctx context.Context, db *sql.DB, s *Session) error {
	const q = `
		INSERT INTO sessions
			(session_id, email, name, picture, access_token, refresh_token, expires_at)
		VALUES
			(?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			email         = VALUES(email),
			name          = VALUES(name),
			picture       = VALUES(picture),
			access_token  = VALUES(access_token),
			refresh_token = VALUES(refresh_token),
			expires_at    = VALUES(expires_at)
	`
	_, err := db.ExecContext(ctx, q,
		s.SessionID,
		s.Email,
		s.Name,
		s.Picture,
		s.AccessToken,
		s.RefreshToken,
		s.ExpiresAt.UTC().Format("2006-01-02 15:04:05"),
	)
	return err
}

// GetSession retrieves a session that has not yet expired.
// Returns nil, nil when the session does not exist or is expired.
func GetSession(ctx context.Context, db *sql.DB, sessionID string) (*Session, error) {
	const q = `
		SELECT session_id, email, name, picture, access_token, refresh_token, expires_at
		FROM   sessions
		WHERE  session_id = ? AND expires_at > NOW()
	`
	row := db.QueryRowContext(ctx, q, sessionID)

	var s Session
	var expiresStr string
	err := row.Scan(
		&s.SessionID,
		&s.Email,
		&s.Name,
		&s.Picture,
		&s.AccessToken,
		&s.RefreshToken,
		&expiresStr,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("GetSession: %w", err)
	}
	s.ExpiresAt, _ = time.Parse("2006-01-02 15:04:05", expiresStr)
	return &s, nil
}

// DeleteSession removes a session (logout).
func DeleteSession(ctx context.Context, db *sql.DB, sessionID string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM sessions WHERE session_id = ?`, sessionID)
	return err
}

// PurgeExpiredSessions deletes all rows past their expiry. Safe to call on a ticker.
func PurgeExpiredSessions(ctx context.Context, db *sql.DB) (int64, error) {
	res, err := db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= NOW()`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
