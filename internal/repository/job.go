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
	FileName        string `json:"file_name"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

// CreateJob initializes a new job entry in the database.
func CreateJob(ctx context.Context, db *sql.DB, jobID, userEmail, fileName string) error {
	_, err := db.ExecContext(ctx, "INSERT INTO jobs (id, user_email, file_name, status) VALUES (?, ?, ?, 'pending')", jobID, userEmail, fileName)
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
	err := db.QueryRowContext(ctx, "SELECT id, status, total_chunks, processed_chunks, COALESCE(file_name, ''), created_at, updated_at FROM jobs WHERE id = ?", jobID).
		Scan(&job.JobID, &job.Status, &job.TotalChunks, &job.ProcessedChunks, &job.FileName, &job.CreatedAt, &job.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &job, nil
}

// GetRecentJobs retrieves the latest 3 jobs for a given user email.
func GetRecentJobs(ctx context.Context, db *sql.DB, email string) ([]JobStatus, error) {
	const q = `
		SELECT id, status, total_chunks, processed_chunks, COALESCE(file_name, ''), created_at, updated_at
		FROM jobs
		WHERE user_email = ?
		ORDER BY created_at DESC
		LIMIT 3
	`
	rows, err := db.QueryContext(ctx, q, email)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []JobStatus
	for rows.Next() {
		var job JobStatus
		err := rows.Scan(&job.JobID, &job.Status, &job.TotalChunks, &job.ProcessedChunks, &job.FileName, &job.CreatedAt, &job.UpdatedAt)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}
