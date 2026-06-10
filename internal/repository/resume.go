package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/druva-06/recruitingest-backend/internal/models"
)

// SaveUserResume saves or updates a user's resume PDF text and/or Google Drive link.
func SaveUserResume(ctx context.Context, db *sql.DB, email string, filename string, resumeText string, driveLink string) error {
	// Check if the record exists first to determine if we should preserve existing text/filename if not supplied.
	const checkQ = "SELECT resume_filename, COALESCE(resume_text, ''), drive_link FROM user_resumes WHERE email = ?"
	var existingFilename sql.NullString
	var existingText string
	var existingDriveLink sql.NullString

	row := db.QueryRowContext(ctx, checkQ, email)
	err := row.Scan(&existingFilename, &existingText, &existingDriveLink)

	if err == sql.ErrNoRows {
		// Insert new record
		const insertQ = `
			INSERT INTO user_resumes (email, resume_filename, resume_text, drive_link)
			VALUES (?, ?, ?, ?)
		`
		_, err = db.ExecContext(ctx, insertQ, email, filename, resumeText, driveLink)
		return err
	} else if err != nil {
		return fmt.Errorf("failed to check existing resume: %w", err)
	}

	// Record exists, update only fields that are provided
	finalFilename := existingFilename.String
	if filename != "" {
		finalFilename = filename
	}

	finalText := existingText
	if resumeText != "" {
		finalText = resumeText
	}

	finalDriveLink := existingDriveLink.String
	if driveLink != "" {
		finalDriveLink = driveLink
	}

	const updateQ = `
		UPDATE user_resumes
		SET resume_filename = ?, resume_text = ?, drive_link = ?, updated_at = NOW()
		WHERE email = ?
	`
	_, err = db.ExecContext(ctx, updateQ, finalFilename, finalText, finalDriveLink, email)
	return err
}

// GetUserResume retrieves the resume information for a user.
func GetUserResume(ctx context.Context, db *sql.DB, email string) (*models.UserResume, error) {
	const q = `
		SELECT email, COALESCE(resume_filename, ''), COALESCE(resume_text, ''), COALESCE(drive_link, ''), created_at, updated_at
		FROM user_resumes
		WHERE email = ?
	`
	row := db.QueryRowContext(ctx, q, email)

	var r models.UserResume
	var createdAtStr, updatedAtStr string
	err := row.Scan(&r.Email, &r.ResumeFilename, &r.ResumeText, &r.DriveLink, &createdAtStr, &updatedAtStr)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("GetUserResume: %w", err)
	}

	formats := []string{
		"2006-01-02 15:04:05",
		time.RFC3339,
		"2006-01-02T15:04:05Z",
	}

	for _, fmtStr := range formats {
		if t, parseErr := time.Parse(fmtStr, createdAtStr); parseErr == nil {
			r.CreatedAt = t
			break
		}
	}

	for _, fmtStr := range formats {
		if t, parseErr := time.Parse(fmtStr, updatedAtStr); parseErr == nil {
			r.UpdatedAt = t
			break
		}
	}

	return &r, nil
}
