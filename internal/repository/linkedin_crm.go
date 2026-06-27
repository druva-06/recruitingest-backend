package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

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

// GetJobPostings retrieves all job postings for a user, optionally filtered and limited
func GetJobPostings(ctx context.Context, db *sql.DB, userEmail, searchQuery string, limit int) ([]models.JobPosting, error) {
	sqlQuery := `
		SELECT 
			j.id, j.user_email, j.company_name, j.role_title, COALESCE(j.job_url, '') as job_url, j.created_at,
			EXISTS(SELECT 1 FROM referral_requests r WHERE r.job_posting_id = j.id AND r.status = 'Referred') as has_referral
		FROM job_postings j 
		WHERE j.user_email = ?
	`
	args := []interface{}{userEmail}

	if searchQuery != "" {
		sqlQuery += " AND (company_name LIKE ? OR role_title LIKE ?)"
		likeQuery := "%" + searchQuery + "%"
		args = append(args, likeQuery, likeQuery)
	}

	sqlQuery += " ORDER BY created_at DESC"

	if limit > 0 {
		sqlQuery += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to get job postings: %w", err)
	}
	defer rows.Close()

	var postings []models.JobPosting
	for rows.Next() {
		var p models.JobPosting
		if err := rows.Scan(&p.ID, &p.UserEmail, &p.CompanyName, &p.RoleTitle, &p.JobURL, &p.CreatedAt, &p.HasReferral); err != nil {
			return nil, err
		}
		postings = append(postings, p)
	}
	return postings, nil
}

// LogOutreach records a new outreach attempt for a LinkedIn profile against a job posting
func LogOutreach(ctx context.Context, db *sql.DB, userEmail string, jobPostingID int, linkedinURL, profileName, currentCompany, currentRole string) (int, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// 1. Ensure the LinkedIn profile exists; create or update it
	var profileID int
	err = tx.QueryRowContext(ctx, "SELECT id FROM linkedin_profiles WHERE user_email = ? AND linkedin_url = ?", userEmail, linkedinURL).Scan(&profileID)
	if err != nil {
		if err == sql.ErrNoRows {
			// Create new profile with connection_status = 'Pending'
			res, err := tx.ExecContext(ctx,
				"INSERT INTO linkedin_profiles (user_email, linkedin_url, profile_name, current_company, current_role, connection_status) VALUES (?, ?, ?, ?, ?, 'Pending')",
				userEmail, linkedinURL, profileName, currentCompany, currentRole)
			if err != nil {
				return 0, fmt.Errorf("failed to create profile: %w", err)
			}
			id, _ := res.LastInsertId()
			profileID = int(id)
		} else {
			return 0, fmt.Errorf("failed to check profile: %w", err)
		}
	} else {
		// Update profile details (name/company/role may have changed)
		_, err = tx.ExecContext(ctx,
			"UPDATE linkedin_profiles SET profile_name = ?, current_company = ?, current_role = ? WHERE id = ?",
			profileName, currentCompany, currentRole, profileID)
		if err != nil {
			return 0, fmt.Errorf("failed to update profile: %w", err)
		}
	}

	// 2. Insert a new referral request or retrieve the existing one
	var requestID int
	err = tx.QueryRowContext(ctx,
		"SELECT id FROM referral_requests WHERE user_email = ? AND linkedin_profile_id = ? AND job_posting_id = ?",
		userEmail, profileID, jobPostingID).Scan(&requestID)
	if err == sql.ErrNoRows {
		res, err := tx.ExecContext(ctx,
			"INSERT INTO referral_requests (user_email, linkedin_profile_id, job_posting_id, status) VALUES (?, ?, ?, 'Logged')",
			userEmail, profileID, jobPostingID)
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

// UpdateReferralStatus updates the workflow status of a specific referral request
// Valid statuses: Logged, Messaged, Referred, Follow-Up
func UpdateReferralStatus(ctx context.Context, db *sql.DB, requestID int, userEmail, status string) error {
	_, err := db.ExecContext(ctx, "UPDATE referral_requests SET status = ? WHERE id = ? AND user_email = ?", status, requestID, userEmail)
	if err != nil {
		return fmt.Errorf("failed to update referral status: %w", err)
	}
	return nil
}

// UpdateProfileConnectionStatus updates the connection_status of linkedin_profiles for a given user
// Valid statuses: Pending, Connected
func UpdateProfileConnectionStatus(ctx context.Context, db *sql.DB, userEmail string, urls []string, newStatus string) (int, error) {
	if len(urls) == 0 {
		return 0, nil
	}

	// Build dynamic IN clause
	args := []interface{}{newStatus, userEmail}
	query := "UPDATE linkedin_profiles SET connection_status = ? WHERE user_email = ? AND linkedin_url IN ("
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
		return 0, fmt.Errorf("failed to batch update connection status: %w", err)
	}
	affected, err := res.RowsAffected()
	return int(affected), err
}

// UpdateSingleProfileConnectionStatus updates the connection_status of one linkedin_profile by ID.
// Only updates profiles that belong to the calling user (via any referral_request they own).
func UpdateSingleProfileConnectionStatus(ctx context.Context, db *sql.DB, profileID int, userEmail, newStatus string) error {
	// Verify the user owns a referral for this profile before updating
	res, err := db.ExecContext(ctx, `
		UPDATE linkedin_profiles p
		INNER JOIN referral_requests r ON r.linkedin_profile_id = p.id AND r.user_email = ?
		SET p.connection_status = ?
		WHERE p.id = ?
	`, userEmail, newStatus, profileID)
	if err != nil {
		return fmt.Errorf("failed to update profile connection status: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("profile not found or not owned by user")
	}
	return nil
}

// GetDashboardReferrals retrieves all referrals grouped by job posting for the dashboard
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
			COALESCE(p.connection_status, 'Pending') as connection_status,
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
		if err := rows.Scan(
			&r.ReferralID, &r.JobPostingID, &r.CompanyName, &r.RoleTitle, &r.JobURL,
			&r.ProfileID, &r.LinkedInURL, &r.ProfileName, &r.CurrentCompany, &r.CurrentRole,
			&r.ConnectionStatus, &r.Status, &r.UpdatedAt, &r.JobCreatedAt,
		); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, nil
}

// DeleteReferralRequest deletes a specific referral request owned by the user
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

// normalizeLinkedInURL strips query params and trailing slashes for consistent comparison
func normalizeLinkedInURL(rawURL string) string {
	// Strip query params
	if idx := strings.Index(rawURL, "?"); idx != -1 {
		rawURL = rawURL[:idx]
	}
	// Strip trailing slash
	return strings.TrimRight(rawURL, "/")
}

// GetProfileReferralsByURL fetches all referral_requests for a given LinkedIn profile URL.
// It normalizes both the input URL and the stored URL (strip query params + trailing slash)
// so minor URL format differences don't cause missed matches.
func GetProfileReferralsByURL(ctx context.Context, db *sql.DB, userEmail, linkedInURL string) ([]models.DashboardReferral, error) {
	cleanURL := normalizeLinkedInURL(linkedInURL)

	// We fetch all profiles whose stored URL normalizes to the same value.
	// TRIM(TRAILING '/' FROM ...) and SUBSTRING_INDEX handle this in MySQL.
	query := `
		SELECT
			r.id as referral_id,
			j.id as job_posting_id,
			j.company_name,
			j.role_title,
			COALESCE(j.job_url, '') as job_url,
			p.id as profile_id,
			p.linkedin_url,
			COALESCE(p.profile_name, '') as profile_name,
			COALESCE(p.current_company, '') as current_company,
			COALESCE(p.current_role, '') as current_role,
			COALESCE(p.connection_status, 'Pending') as connection_status,
			r.status,
			COALESCE(r.updated_at, r.created_at) as updated_at,
			j.created_at as job_created_at,
			EXISTS(SELECT 1 FROM referral_requests r2 WHERE r2.job_posting_id = j.id AND r2.status = 'Referred') as job_has_referral
		FROM referral_requests r
		JOIN linkedin_profiles p ON r.linkedin_profile_id = p.id
		JOIN job_postings j ON r.job_posting_id = j.id
		WHERE r.user_email = ?
		AND TRIM(TRAILING '/' FROM SUBSTRING_INDEX(p.linkedin_url, '?', 1)) = ?
		ORDER BY r.updated_at DESC
	`
	rows, err := db.QueryContext(ctx, query, userEmail, cleanURL)
	if err != nil {
		return nil, fmt.Errorf("failed to query profile referrals: %w", err)
	}
	defer rows.Close()

	var results []models.DashboardReferral
	for rows.Next() {
		var r models.DashboardReferral
		if err := rows.Scan(
			&r.ReferralID, &r.JobPostingID, &r.CompanyName, &r.RoleTitle, &r.JobURL,
			&r.ProfileID, &r.LinkedInURL, &r.ProfileName, &r.CurrentCompany, &r.CurrentRole,
			&r.ConnectionStatus, &r.Status, &r.UpdatedAt, &r.JobCreatedAt, &r.JobHasReferral,
		); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, nil
}
