package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/druva-06/recruitingest-backend/internal/models"
)

// BulkInsertRecruiters inserts a slice of Recruiters into the database in batches.
// It uses INSERT IGNORE to silently drop duplicate emails.
func BulkInsertRecruiters(ctx context.Context, db *sql.DB, recruiters []models.Recruiter, sourceFileName string) (int64, error) {
	if len(recruiters) == 0 {
		return 0, nil
	}

	// Begin the transaction
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}

	// Defer rollback. If tx.Commit() succeeds, this Rollback will safely do nothing.
	defer tx.Rollback()

	batchSize := 500
	var totalInserted int64

	for i := 0; i < len(recruiters); i += batchSize {
		end := i + batchSize
		if end > len(recruiters) {
			end = len(recruiters)
		}

		batch := recruiters[i:end]

		// Dynamically construct the query with placeholders to prevent SQL injection
		valueStrings := make([]string, 0, len(batch))
		valueArgs := make([]interface{}, 0, len(batch)*4)

		for _, r := range batch {
			valueStrings = append(valueStrings, "(?, ?, ?, ?, ?)")
			valueArgs = append(valueArgs, r.Name, r.Title, r.Email, r.Company, sourceFileName)
		}

		stmt := fmt.Sprintf(
			"INSERT IGNORE INTO recruiters (recruiter_name, recruiter_title, recruiter_email, company_name, source_file) VALUES %s",
			strings.Join(valueStrings, ","),
		)

		res, err := tx.ExecContext(ctx, stmt, valueArgs...)
		if err != nil {
			return 0, fmt.Errorf("failed to execute batch insert: %w", err)
		}

		// Track successfully inserted new rows
		rowsAffected, err := res.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("failed to get rows affected: %w", err)
		}

		totalInserted += rowsAffected
	}

	// Commit the transaction
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return totalInserted, nil
}

// CreateRecruiter inserts a manually entered recruiter.
func CreateRecruiter(ctx context.Context, db *sql.DB, recruiter models.Recruiter) (*models.RecruiterRecord, error) {
	result, err := db.ExecContext(
		ctx,
		"INSERT INTO recruiters (recruiter_name, recruiter_title, recruiter_email, company_name, source_file) VALUES (?, ?, ?, ?, ?)",
		recruiter.Name,
		recruiter.Title,
		recruiter.Email,
		recruiter.Company,
		"manual-entry",
	)
	if err != nil {
		return nil, fmt.Errorf("failed to insert recruiter: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("failed to get recruiter id: %w", err)
	}

	return GetRecruiterByID(ctx, db, id)
}

// GetRecruiterByID retrieves a single persisted recruiter.
func GetRecruiterByID(ctx context.Context, db *sql.DB, id int64) (*models.RecruiterRecord, error) {
	var recruiter models.RecruiterRecord
	err := db.QueryRowContext(
		ctx,
		"SELECT id, recruiter_name, COALESCE(recruiter_title, ''), recruiter_email, COALESCE(company_name, ''), COALESCE(location, ''), COALESCE(linkedin_url, ''), COALESCE(source_file, ''), created_at FROM recruiters WHERE id = ?",
		id,
	).Scan(
		&recruiter.ID,
		&recruiter.Name,
		&recruiter.Title,
		&recruiter.Email,
		&recruiter.Company,
		&recruiter.Location,
		&recruiter.LinkedinUrl,
		&recruiter.SourceFile,
		&recruiter.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &recruiter, nil
}

// SearchRecruiters finds recruiters using optional field filters and a general query.
func SearchRecruiters(ctx context.Context, db *sql.DB, query, company, email string, limit, offset int) ([]models.RecruiterRecord, int, error) {
	conditions := make([]string, 0, 3)
	args := make([]interface{}, 0, 8)

	if query != "" {
		like := "%" + query + "%"
		conditions = append(conditions, "(recruiter_name LIKE ? OR recruiter_title LIKE ? OR recruiter_email LIKE ? OR company_name LIKE ?)")
		args = append(args, like, like, like, like)
	}
	if company != "" {
		conditions = append(conditions, "company_name LIKE ?")
		args = append(args, "%"+company+"%")
	}
	if email != "" {
		conditions = append(conditions, "recruiter_email LIKE ?")
		args = append(args, "%"+email+"%")
	}

	// 1. Get the total matching count for pagination
	countStatement := "SELECT COUNT(*) FROM recruiters"
	if len(conditions) > 0 {
		countStatement += " WHERE " + strings.Join(conditions, " AND ")
	}

	var totalCount int
	err := db.QueryRowContext(ctx, countStatement, args...).Scan(&totalCount)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to count recruiters: %w", err)
	}

	// 2. Query the actual paginated records
	statement := "SELECT id, recruiter_name, COALESCE(recruiter_title, ''), recruiter_email, COALESCE(company_name, ''), COALESCE(location, ''), COALESCE(linkedin_url, ''), COALESCE(source_file, ''), created_at FROM recruiters"
	if len(conditions) > 0 {
		statement += " WHERE " + strings.Join(conditions, " AND ")
	}
	statement += " ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)

	rows, err := db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to search recruiters: %w", err)
	}
	defer rows.Close()

	recruiters := make([]models.RecruiterRecord, 0)
	for rows.Next() {
		var recruiter models.RecruiterRecord
		if err := rows.Scan(
			&recruiter.ID,
			&recruiter.Name,
			&recruiter.Title,
			&recruiter.Email,
			&recruiter.Company,
			&recruiter.Location,
			&recruiter.LinkedinUrl,
			&recruiter.SourceFile,
			&recruiter.CreatedAt,
		); err != nil {
			return nil, 0, fmt.Errorf("failed to scan recruiter: %w", err)
		}
		recruiters = append(recruiters, recruiter)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("failed while reading recruiters: %w", err)
	}
	return recruiters, totalCount, nil
}
