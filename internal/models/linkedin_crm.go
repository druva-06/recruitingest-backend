package models

type JobPosting struct {
	ID          int    `json:"id"`
	UserEmail   string `json:"user_email"`
	CompanyName string `json:"company_name"`
	RoleTitle   string `json:"role_title"`
	JobURL      string `json:"job_url"`
	CreatedAt   string `json:"created_at"`
	HasReferral bool   `json:"has_referral"`
}

type LinkedInProfile struct {
	ID               int    `json:"id"`
	UserEmail        string `json:"user_email"`
	LinkedInURL      string `json:"linkedin_url"`
	ProfileName      string `json:"profile_name"`
	CurrentCompany   string `json:"current_company"`
	CurrentRole      string `json:"current_role"`
	ConnectionStatus string `json:"connection_status"` // Pending, Connected
	CreatedAt        string `json:"created_at"`
}

type ReferralRequest struct {
	ID                int    `json:"id"`
	UserEmail         string `json:"user_email"`
	LinkedInProfileID int    `json:"linkedin_profile_id"`
	JobPostingID      int    `json:"job_posting_id"`
	Status            string `json:"status"` // Logged, Messaged, Referred, Follow-Up
	CreatedAt         string `json:"created_at"`
	UpdatedAt         string `json:"updated_at"`
}

// Joined representation for the dashboard
type DashboardReferral struct {
	ReferralID       int    `json:"referral_id"`
	JobPostingID     int    `json:"job_posting_id"`
	CompanyName      string `json:"company_name"`
	RoleTitle        string `json:"role_title"`
	JobURL           string `json:"job_url"`
	ProfileID        int    `json:"profile_id"`
	LinkedInURL      string `json:"linkedin_url"`
	ProfileName      string `json:"profile_name"`
	CurrentCompany   string `json:"current_company"`
	CurrentRole      string `json:"current_role"`
	ConnectionStatus string `json:"connection_status"` // Pending, Connected
	Status           string `json:"status"`            // Logged, Messaged, Referred, Follow-Up
	UpdatedAt        string `json:"updated_at"`
	JobCreatedAt     string `json:"job_created_at"`
	JobHasReferral   bool   `json:"job_has_referral"`
}
