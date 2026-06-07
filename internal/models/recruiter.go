package models

// Recruiter defines the strict schema for LLM structured output and database operations.
type Recruiter struct {
	Name    string `json:"recruiter_name"`
	Title   string `json:"recruiter_title"`
	Email   string `json:"recruiter_email"`
	Company string `json:"company_name"`
}
