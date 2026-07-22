package jobscout

import (
	"time"
)

// JobScoutConfig stores per-user Apify + Gemini configuration for Job Scout
type JobScoutConfig struct {
	ID                   int       `json:"id"`
	UserEmail            string    `json:"user_email"`
	ApifyAPIKey          string    `json:"apify_api_key"`
	ApifyActorID         string    `json:"apify_actor_id"`
	GeminiAPIKey         string    `json:"gemini_api_key"`
	GeminiModel          string    `json:"gemini_model"`
	DefaultScrapeLimit   int       `json:"default_scrape_limit"`
	ScrapeCompany        bool      `json:"scrape_company"`
	ScoreRateMode        string    `json:"score_rate_mode"`
	ScoreRateValue       int       `json:"score_rate_value"`
	ScoreIntervalSeconds int       `json:"score_interval_seconds"`
	TopNResults          int       `json:"top_n_results"`
	UserSkillsExperience string    `json:"user_skills_experience"`
	ScoringPrompt        string    `json:"scoring_prompt"`
	DefaultLinkedInURL   string    `json:"default_linkedin_url"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

// JobScoutRun tracks each scraping + scoring run
type JobScoutRun struct {
	ID           int        `json:"id"`
	UserEmail    string     `json:"user_email"`
	ApifyRunID   *string    `json:"apify_run_id"`
	LinkedInURL  string     `json:"linkedin_url"`
	ScrapeLimit  int        `json:"scrape_limit"`
	Status       string     `json:"status"` // pending -> scraping -> scoring -> completed -> failed
	TotalScraped int        `json:"total_scraped"`
	TotalScored  int        `json:"total_scored"`
	ErrorMessage *string    `json:"error_message"`
	CreatedAt    time.Time  `json:"created_at"`
	StartedAt    *time.Time `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at"`
}

// JobScoutJob is a single job listing with fit scores
type JobScoutJob struct {
	ID              int        `json:"id"`
	RunID           int        `json:"run_id"`
	UserEmail       string     `json:"user_email"`
	Title           string     `json:"title"`
	Company         string     `json:"company"`
	Location        string     `json:"location"`
	Description     string     `json:"description"`
	LinkedInURL     string     `json:"linkedin_url"`
	Salary          string     `json:"salary"`
	JobType         string     `json:"job_type"`
	ExperienceLevel string     `json:"experience_level"`
	PostedAt        string     `json:"posted_at"`
	ApplicantCount  string     `json:"applicant_count"`
	CompanyURL      string     `json:"company_url"`
	CompanyLogo     string     `json:"company_logo"`
	ScoreStatus     string     `json:"score_status"` // pending, scoring, scored, failed
	FitScore        *float64   `json:"fit_score"`
	FitReasoning    *string    `json:"fit_reasoning"`
	MatchingSkills  *string    `json:"matching_skills"` // JSON array string
	MissingSkills   *string    `json:"missing_skills"`  // JSON array string
	ScoredAt        *time.Time `json:"scored_at"`
	CreatedAt       time.Time  `json:"created_at"`

	// Optional rank field for results
	Rank int `json:"rank,omitempty"`
}
