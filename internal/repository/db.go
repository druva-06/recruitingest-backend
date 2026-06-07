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
