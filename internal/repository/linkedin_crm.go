package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/druva-06/recruitingest-backend/internal/models"
)

// CreateJobPosting creates a new job posting for tracking
func CreateJobPosting(ctx context.Context, db *sql.DB, userEmail, companyName, roleTitle, jobURL string) (int, error) {
	res, err := db.ExecContext(ctx, "INSERT INTO job_postings (user_email, company_name, role_title, job_url) VALUES (?, ?, ?, ?)", userEmail, companyName, roleTitle, jobURL)
	if err != nil {
		return 0, fmt.Errorf("failed to create job posting: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("failed to get last insert id: %w", err)
	}
	return int(id), nil
}

// GetJobPostings retrieves all job postings for a user
func GetJobPostings(ctx context.Context, db *sql.DB, userEmail string) ([]models.JobPosting, error) {
	rows, err := db.QueryContext(ctx, "SELECT id, user_email, company_name, role_title, COALESCE(job_url, '') as job_url, created_at FROM job_postings WHERE user_email = ? ORDER BY created_at DESC", userEmail)
	if err != nil {
		return nil, fmt.Errorf("failed to get job postings: %w", err)
	}
	defer rows.Close()

	var postings []models.JobPosting
	for rows.Next() {
		var p models.JobPosting
		if err := rows.Scan(&p.ID, &p.UserEmail, &p.CompanyName, &p.RoleTitle, &p.JobURL, &p.CreatedAt); err != nil {
			return nil, err
		}
		postings = append(postings, p)
	}
	return postings, nil
}

// LogOutreach records a new outreach attempt
func LogOutreach(ctx context.Context, db *sql.DB, userEmail string, jobPostingID int, linkedinURL, profileName, currentCompany, currentRole string) (int, error) {
	// Start transaction
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// 1. Ensure profile exists or update it
	var profileID int
	err = tx.QueryRowContext(ctx, "SELECT id FROM linkedin_profiles WHERE user_email = ? AND linkedin_url = ?", userEmail, linkedinURL).Scan(&profileID)
	if err != nil {
		if err == sql.ErrNoRows {
			// Create profile
			res, err := tx.ExecContext(ctx, "INSERT INTO linkedin_profiles (user_email, linkedin_url, profile_name, current_company, current_role) VALUES (?, ?, ?, ?, ?)", userEmail, linkedinURL, profileName, currentCompany, currentRole)
			if err != nil {
				return 0, fmt.Errorf("failed to create profile: %w", err)
			}
			id, _ := res.LastInsertId()
			profileID = int(id)
		} else {
			return 0, fmt.Errorf("failed to check profile: %w", err)
		}
	} else {
		// Update existing profile's current details if they've changed
		_, err = tx.ExecContext(ctx, "UPDATE linkedin_profiles SET profile_name = ?, current_company = ?, current_role = ? WHERE id = ?", profileName, currentCompany, currentRole, profileID)
		if err != nil {
			return 0, fmt.Errorf("failed to update profile: %w", err)
		}
	}

	// 2. Insert or Get Referral Request
	var requestID int
	err = tx.QueryRowContext(ctx, "SELECT id FROM referral_requests WHERE user_email = ? AND linkedin_profile_id = ? AND job_posting_id = ?", userEmail, profileID, jobPostingID).Scan(&requestID)
	if err == sql.ErrNoRows {
		res, err := tx.ExecContext(ctx, "INSERT INTO referral_requests (user_email, linkedin_profile_id, job_posting_id, status) VALUES (?, ?, ?, 'Pending')", userEmail, profileID, jobPostingID)
		if err != nil {
			return 0, fmt.Errorf("failed to insert referral request: %w", err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return 0, fmt.Errorf("failed to get request id: %w", err)
		}
		requestID = int(id)
	} else if err != nil {
		return 0, fmt.Errorf("failed to select referral request: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return requestID, nil
}

// UpdateReferralStatus updates the status of a specific referral request
func UpdateReferralStatus(ctx context.Context, db *sql.DB, requestID int, userEmail, status string) error {
	_, err := db.ExecContext(ctx, "UPDATE referral_requests SET status = ? WHERE id = ? AND user_email = ?", status, requestID, userEmail)
	if err != nil {
		return fmt.Errorf("failed to update referral status: %w", err)
	}
	return nil
}

// BatchUpdateReferralStatusByURL updates multiple profiles statuses simultaneously for a user
func BatchUpdateReferralStatusByURL(ctx context.Context, db *sql.DB, userEmail string, urls []string, oldStatus, newStatus string) (int, error) {
	if len(urls) == 0 {
		return 0, nil
	}

	// Dynamic IN clause building
	args := []interface{}{newStatus, userEmail, oldStatus}
	query := "UPDATE referral_requests r JOIN linkedin_profiles p ON r.linkedin_profile_id = p.id SET r.status = ? WHERE r.user_email = ? AND r.status = ? AND p.linkedin_url IN ("
	
	for i, url := range urls {
		query += "?"
		args = append(args, url)
		if i < len(urls)-1 {
			query += ","
		}
	}
	query += ")"

	res, err := db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("failed to batch update status: %w", err)
	}
	affected, err := res.RowsAffected()
	return int(affected), err
}

// GetDashboardReferrals retrieves the grouped dashboard data
func GetDashboardReferrals(ctx context.Context, db *sql.DB, userEmail string) ([]models.DashboardReferral, error) {
	query := `
		SELECT 
			COALESCE(r.id, 0) as referral_id,
			j.id as job_posting_id,
			j.company_name,
			j.role_title,
			COALESCE(j.job_url, '') as job_url,
			COALESCE(p.id, 0) as profile_id,
			COALESCE(p.linkedin_url, '') as linkedin_url,
			COALESCE(p.profile_name, '') as profile_name,
			COALESCE(p.current_company, '') as current_company,
			COALESCE(p.current_role, '') as current_role,
			COALESCE(r.status, '') as status,
			COALESCE(r.updated_at, j.created_at) as updated_at,
			j.created_at as job_created_at
		FROM job_postings j
		LEFT JOIN referral_requests r ON j.id = r.job_posting_id AND r.user_email = ?
		LEFT JOIN linkedin_profiles p ON r.linkedin_profile_id = p.id
		WHERE j.user_email = ?
		ORDER BY j.created_at DESC, r.updated_at DESC
	`
	rows, err := db.QueryContext(ctx, query, userEmail, userEmail)
	if err != nil {
		return nil, fmt.Errorf("failed to query dashboard referrals: %w", err)
	}
	defer rows.Close()

	var results []models.DashboardReferral
	for rows.Next() {
		var r models.DashboardReferral
		if err := rows.Scan(&r.ReferralID, &r.JobPostingID, &r.CompanyName, &r.RoleTitle, &r.JobURL, &r.ProfileID, &r.LinkedInURL, &r.ProfileName, &r.CurrentCompany, &r.CurrentRole, &r.Status, &r.UpdatedAt, &r.JobCreatedAt); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, nil
}

// DeleteReferralRequest deletes a specific referral request
func DeleteReferralRequest(ctx context.Context, db *sql.DB, requestID int, userEmail string) error {
	res, err := db.ExecContext(ctx, "DELETE FROM referral_requests WHERE id = ? AND user_email = ?", requestID, userEmail)
	if err != nil {
		return fmt.Errorf("failed to delete referral request: %w", err)
	}
	
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	
	if affected == 0 {
		return fmt.Errorf("referral request not found or not owned by user")
	}
	return nil
}
