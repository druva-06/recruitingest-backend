package models

// Recruiter defines the strict schema for LLM structured output and database operations.
type Recruiter struct {
	Name    string `json:"recruiter_name"`
	Title   string `json:"recruiter_title"`
	Email   string `json:"recruiter_email"`
	Company string `json:"company_name"`
}

// RecruiterRecord represents a persisted recruiter returned by the API.
type RecruiterRecord struct {
	ID         int64  `json:"id"`
	Name       string `json:"recruiter_name"`
	Title      string `json:"recruiter_title"`
	Email      string `json:"recruiter_email"`
	Company     string `json:"company_name"`
	Location    string `json:"location"`
	LinkedinUrl string `json:"linkedin_url"`
	SourceFile  string `json:"source_file"`
	CreatedAt   string `json:"created_at"`
}
