package repository

import (
	"context"
	"database/sql"
)

// JobStatus represents the state of a background ingestion job.
type JobStatus struct {
	JobID           string `json:"job_id"`
	Status          string `json:"status"` // pending, processing, completed, failed
	TotalChunks     int    `json:"total_chunks"`
	ProcessedChunks int    `json:"processed_chunks"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

// CreateJob initializes a new job entry in the database.
func CreateJob(ctx context.Context, db *sql.DB, jobID string) error {
	_, err := db.ExecContext(ctx, "INSERT INTO jobs (id, status) VALUES (?, 'pending')", jobID)
	return err
}

// UpdateJobStatus updates the high-level status of the job.
func UpdateJobStatus(ctx context.Context, db *sql.DB, jobID, status string) error {
	_, err := db.ExecContext(ctx, "UPDATE jobs SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?", status, jobID)
	return err
}

// SetJobTotalChunks records how many chunks the PDF was split into.
func SetJobTotalChunks(ctx context.Context, db *sql.DB, jobID string, total int) error {
	_, err := db.ExecContext(ctx, "UPDATE jobs SET total_chunks = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?", total, jobID)
	return err
}

// IncrementJobProgress increments the processed chunk count by 1.
func IncrementJobProgress(ctx context.Context, db *sql.DB, jobID string) error {
	_, err := db.ExecContext(ctx, "UPDATE jobs SET processed_chunks = processed_chunks + 1, updated_at = CURRENT_TIMESTAMP WHERE id = ?", jobID)
	return err
}

// GetJobByID retrieves the current status of a job.
func GetJobByID(ctx context.Context, db *sql.DB, jobID string) (*JobStatus, error) {
	var job JobStatus
	err := db.QueryRowContext(ctx, "SELECT id, status, total_chunks, processed_chunks, created_at, updated_at FROM jobs WHERE id = ?", jobID).
		Scan(&job.JobID, &job.Status, &job.TotalChunks, &job.ProcessedChunks, &job.CreatedAt, &job.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &job, nil
}
