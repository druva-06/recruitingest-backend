package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/druva-06/recruitingest-backend/internal/models"
	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
)

// GeminiService encapsulates the Google Gemini client and its configuration.
type GeminiService struct {
	client *genai.Client
	model  *genai.GenerativeModel
}

// NewGeminiService initializes the Gemini client with strict JSON schema settings.
func NewGeminiService(ctx context.Context, apiKey, modelName string) (*GeminiService, error) {
	client, err := genai.NewClient(ctx, option.WithAPIKey(apiKey))
	if err != nil {
		return nil, fmt.Errorf("failed to create gemini client: %w", err)
	}

	model := client.GenerativeModel(modelName)
	model.ResponseMIMEType = "application/json"

	// Define the strict schema: Array of Objects (Recruiter)
	model.ResponseSchema = &genai.Schema{
		Type: genai.TypeArray,
		Items: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"recruiter_name":  {Type: genai.TypeString},
				"recruiter_title": {Type: genai.TypeString},
				"recruiter_email": {Type: genai.TypeString},
				"company_name":    {Type: genai.TypeString},
			},
		},
	}

	model.SystemInstruction = genai.NewUserContent(genai.Text(
		"Extract recruiter contact details from the provided text. If a field is missing, return an empty string. Only return valid data.",
	))

	return &GeminiService{
		client: client,
		model:  model,
	}, nil
}

// Close gracefully closes the underlying genai client.
func (s *GeminiService) Close() {
	if s.client != nil {
		s.client.Close()
	}
}

// ExtractRecruiters processes a single text chunk with automatic retries and context timeouts.
func (s *GeminiService) ExtractRecruiters(ctx context.Context, textChunk string) ([]models.Recruiter, error) {
	var recruiters []models.Recruiter
	var resp *genai.GenerateContentResponse
	var err error

	maxRetries := 3
	for i := 0; i < maxRetries; i++ {
		// Enforce a 60-second timeout per API call
		callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		resp, err = s.model.GenerateContent(callCtx, genai.Text(textChunk))
		cancel()

		if err == nil {
			break
		}

		log.Printf("[Worker/LLM] API Error (attempt %d/%d): %v", i+1, maxRetries, err)

		// Wait and retry with exponential backoff (2s, 4s, 8s) if not the last attempt
		if i < maxRetries-1 {
			time.Sleep(time.Duration(1<<i) * 2 * time.Second)
		}
	}

	if err != nil {
		return nil, fmt.Errorf("failed to generate content after %d retries: %w", maxRetries, err)
	}

	// Safely extract the generated JSON text
	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("no content returned from LLM")
	}

	part := resp.Candidates[0].Content.Parts[0]
	txt, ok := part.(genai.Text)
	if !ok {
		return nil, fmt.Errorf("unexpected response part type")
	}

	// Parse JSON into the robust struct slice
	if err := json.Unmarshal([]byte(txt), &recruiters); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON: %w\nPayload received: %s", err, txt)
	}

	return recruiters, nil
}

// DefaultPromptTemplate is the highly optimized default outreach prompt using named placeholders.
const DefaultPromptTemplate = `You are a professional outreach assistant. Write a personalized cold outreach email from a job applicant to a recruiter.

The output MUST be a valid JSON object matching this schema:
{
  "subject": "Email subject line",
  "body": "Email body in HTML"
}

[INPUT DETAILS]
Recruiter Name: {{recruiter_name}}
Company Name: {{company_name}}
Job Description:
{{job_description}}
Applicant Name: {{applicant_name}}
Applicant Email: {{applicant_email}}
Resume Raw Content:
{{resume_content}}
Google Drive Resume Link: {{drive_link}}

[EXAMPLE OUTPUT FORMAT]
{
  "subject": "Go Engineer - Aligning with Google Role",
  "body": "<p>Hi John,</p><p>I am reaching out regarding the Go Engineer role at Google. With my background in backend systems, I am excited about the opportunity.</p><p>Here is why my background aligns with your requirements:</p><ul><li><strong>Go backend development:</strong> Developed robust APIs using Go for 3+ years, improving performance by 25%%.</li><li><strong>Database Optimization:</strong> Optimized MySQL schemas and queries to reduce latency.</li></ul><p>Please check my <a href=\"{{drive_link}}\" style=\"color: #176b4a; font-weight: bold; text-decoration: underline;\">view my complete resume on Google Drive</a>.</p><p>Are you open to a brief call this week to discuss alignment?</p><p>Thanks & Regards,<br>Alex Mercer<br>alex@email.com</p>"
}

[INSTRUCTIONS]
1. Write a professional subject line. Reference a key matching skill.
2. In the body (HTML format using <p>, <strong>, <ul>, <li>, and <a>), highlight 2-3 specific skills/projects from the Resume Raw Content that match the Job Description. Use <strong> to highlight key metrics or tech.
3. Make sure to use the Google Drive Resume Link in a clean anchor tag '<a href="{{drive_link}}" style="color: #176b4a; font-weight: bold; text-decoration: underline;">view my complete resume on Google Drive</a>' in the body.
4. Keep the email concise and call-to-action focused (encouraging a reply).
5. Output ONLY the raw JSON object. Do not include markdown code block wrappers (like triple backticks) or any conversational text outside the JSON.`

// GenerateEmailContent uses Gemini to generate a personalized email outreach.
func (s *GeminiService) GenerateEmailContent(ctx context.Context, modelName, jobDesc, companyName, recruiterName, userName, userEmail, resumeText, driveLink, customPrompt string) (string, string, error) {
	model := s.client.GenerativeModel(modelName)
	model.ResponseMIMEType = "application/json"
	model.ResponseSchema = &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"subject": {Type: genai.TypeString},
			"body":    {Type: genai.TypeString},
		},
		Required: []string{"subject", "body"},
	}

	template := customPrompt
	if template == "" {
		template = DefaultPromptTemplate
	}

	// Apply template replacements
	replacements := map[string]string{
		"{{recruiter_name}}":  recruiterName,
		"{{company_name}}":    companyName,
		"{{job_description}}": jobDesc,
		"{{applicant_name}}":  userName,
		"{{applicant_email}}": userEmail,
		"{{resume_content}}":  resumeText,
		"{{drive_link}}":      driveLink,
	}

	prompt := template
	for k, v := range replacements {
		prompt = strings.ReplaceAll(prompt, k, v)
	}

	callCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	resp, err := model.GenerateContent(callCtx, genai.Text(prompt))
	if err != nil {
		return "", "", fmt.Errorf("failed to generate email content: %w", err)
	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return "", "", fmt.Errorf("no content returned from LLM")
	}

	part := resp.Candidates[0].Content.Parts[0]
	txt, ok := part.(genai.Text)
	if !ok {
		return "", "", fmt.Errorf("unexpected response part type")
	}

	var emailResult struct {
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}

	if err := json.Unmarshal([]byte(txt), &emailResult); err != nil {
		return "", "", fmt.Errorf("failed to parse email JSON: %w\nPayload: %s", err, txt)
	}

	return emailResult.Subject, emailResult.Body, nil
}

