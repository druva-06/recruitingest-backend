package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/druva-06/recruitingest-backend/internal/models"
)

func CreateAIJob(ctx context.Context, db *sql.DB, userEmail, jobType string, payload json.RawMessage) (int64, error) {
	result, err := db.ExecContext(
		ctx,
		"INSERT INTO ai_jobs (user_email, job_type, payload, status) VALUES (?, ?, ?, 'pending')",
		userEmail, jobType, payload,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to create ai job: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("failed to get last insert id: %w", err)
	}
	return id, nil
}

func GetAIJobByID(ctx context.Context, db *sql.DB, id int64) (*models.AIJob, error) {
	var job models.AIJob
	var payloadBytes, resultBytes []byte

	err := db.QueryRowContext(
		ctx,
		"SELECT id, user_email, job_type, payload, status, result, error_message, created_at, updated_at FROM ai_jobs WHERE id = ?",
		id,
	).Scan(&job.ID, &job.UserEmail, &job.JobType, &payloadBytes, &job.Status, &resultBytes, &job.ErrorMessage, &job.CreatedAt, &job.UpdatedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("failed to get ai job: %w", err)
	}

	if len(payloadBytes) > 0 {
		job.Payload = json.RawMessage(payloadBytes)
	}
	if len(resultBytes) > 0 {
		job.Result = json.RawMessage(resultBytes)
	}

	return &job, nil
}

func UpdateAIJobStatus(ctx context.Context, db *sql.DB, id int64, status string, result json.RawMessage, errorMessage *string) error {
	var resultBytes []byte
	if result != nil {
		resultBytes = []byte(result)
	}
	_, err := db.ExecContext(
		ctx,
		"UPDATE ai_jobs SET status = ?, result = ?, error_message = ? WHERE id = ?",
		status, resultBytes, errorMessage, id,
	)
	if err != nil {
		return fmt.Errorf("failed to update ai job status: %w", err)
	}
	return nil
}

func GetPendingAIJobs(ctx context.Context, db *sql.DB) ([]models.AIJob, error) {
	rows, err := db.QueryContext(
		ctx,
		"SELECT id, user_email, job_type, payload, status, result, error_message, created_at, updated_at FROM ai_jobs WHERE status = 'pending' ORDER BY created_at ASC",
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get pending ai jobs: %w", err)
	}
	defer rows.Close()

	var jobs []models.AIJob
	for rows.Next() {
		var job models.AIJob
		var payloadBytes, resultBytes []byte
		if err := rows.Scan(&job.ID, &job.UserEmail, &job.JobType, &payloadBytes, &job.Status, &resultBytes, &job.ErrorMessage, &job.CreatedAt, &job.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan ai job: %w", err)
		}
		if len(payloadBytes) > 0 {
			job.Payload = json.RawMessage(payloadBytes)
		}
		if len(resultBytes) > 0 {
			job.Result = json.RawMessage(resultBytes)
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed during rows iteration: %w", err)
	}
	return jobs, nil
}
