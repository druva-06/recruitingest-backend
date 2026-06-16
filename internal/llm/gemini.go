package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
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
		"Extract recruiter contact details from the provided unstructured text. Identify names, titles (e.g. HR, Recruiter), emails, and company names. If a company name isn't explicitly stated but can be confidently inferred from the email domain (e.g., winsoftech.com -> Winsoftech), include it. If a field is missing, return an empty string. Only return valid, well-formatted data.",
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

		slog.Warn("API Error", "attempt", i+1, "maxRetries", maxRetries, "error", err)

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
Job Title: {{job_title}}
Job URL: {{job_url}}
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

// DefaultReferralPromptTemplate is the default prompt for referral requests.
const DefaultReferralPromptTemplate = `You are a professional outreach assistant. Write a personalized cold outreach email from a job applicant to a contact asking for a referral.

The output MUST be a valid JSON object matching this schema:
{
  "subject": "Email subject line",
  "body": "Email body in HTML"
}

[INPUT DETAILS]
Contact Name: {{recruiter_name}}
Company Name: {{company_name}}
Job Title: {{job_title}}
Job URL: {{job_url}}
Job Description:
{{job_description}}
Applicant Name: {{applicant_name}}
Applicant Email: {{applicant_email}}
Resume Raw Content:
{{resume_content}}
Google Drive Resume Link: {{drive_link}}

[INSTRUCTIONS]
1. Write a professional subject line.
2. In the body (HTML format using <p>, <strong>, <ul>, <li>, and <a>), politely ask for a referral for the provided job role. Highlight 2-3 specific skills/projects from the Resume Raw Content that match the Job Description. Use <strong> to highlight key metrics or tech.
3. Make sure to use the Google Drive Resume Link in a clean anchor tag '<a href="{{drive_link}}" style="color: #176b4a; font-weight: bold; text-decoration: underline;">view my complete resume on Google Drive</a>' in the body.
4. Keep the email concise and polite.
5. Output ONLY the raw JSON object. Do not include markdown code block wrappers (like triple backticks) or any conversational text outside the JSON.`

// ExtractJobDetails extracts the job title and URL from the pasted job description.
func (s *GeminiService) ExtractJobDetails(ctx context.Context, modelName, jobDesc string) (string, string, error) {
	model := s.client.GenerativeModel(modelName)
	model.ResponseMIMEType = "application/json"
	model.ResponseSchema = &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"job_title": {Type: genai.TypeString},
			"job_url":   {Type: genai.TypeString},
		},
	}
	model.SystemInstruction = genai.NewUserContent(genai.Text("Extract the Job Title and Job URL from the provided job description text. If not found, return empty strings."))

	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	resp, err := model.GenerateContent(callCtx, genai.Text(jobDesc))
	if err != nil {
		return "", "", fmt.Errorf("extraction failed: %w", err)
	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return "", "", nil
	}

	part := resp.Candidates[0].Content.Parts[0]
	txt, ok := part.(genai.Text)
	if !ok {
		return "", "", nil
	}

	var res struct {
		JobTitle string `json:"job_title"`
		JobUrl   string `json:"job_url"`
	}
	
	cleanTxt := strings.TrimSpace(string(txt))
	if strings.HasPrefix(cleanTxt, "```json") {
		cleanTxt = strings.TrimPrefix(cleanTxt, "```json")
		cleanTxt = strings.TrimSuffix(cleanTxt, "```")
		cleanTxt = strings.TrimSpace(cleanTxt)
	}

	if err := json.Unmarshal([]byte(cleanTxt), &res); err != nil {
		slog.Error("Failed to unmarshal job details", "error", err, "raw_text", txt)
		return "", "", nil
	}
	slog.Info("Extracted job details", "title", res.JobTitle, "url", res.JobUrl)
	return res.JobTitle, res.JobUrl, nil
}

// GenerateEmailContent uses Gemini to generate a personalized email outreach.
func (s *GeminiService) GenerateEmailContent(ctx context.Context, modelName, jobDesc, jobTitle, jobUrl, companyName, recruiterName, userName, userEmail, resumeText, driveLink, customPrompt, pitchType string) (string, string, error) {
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

	var finalPrompt string
	if customPrompt == "" {
		if pitchType == "referral" {
			finalPrompt = DefaultReferralPromptTemplate
		} else {
			finalPrompt = DefaultPromptTemplate
		}
	} else {
		finalPrompt = fmt.Sprintf(`[SYSTEM INSTRUCTIONS / USER CUSTOM PROMPT]
%s

[INPUT DETAILS - CONTEXT FOR EMAIL]
Recruiter Name: %s
Company Name: %s
Job Title: %s
Job URL: %s
Job Description:
%s

Applicant Name: %s
Applicant Email: %s
Resume Raw Content:
%s
Google Drive Resume Link: %s

[OUTPUT FORMAT INSTRUCTION]
The output MUST be a valid JSON object matching this schema:
{
  "subject": "Email subject line",
  "body": "Email body in HTML"
}
Output ONLY the raw JSON object. Do not include markdown code block wrappers.`, customPrompt, recruiterName, companyName, jobTitle, jobUrl, jobDesc, userName, userEmail, resumeText, driveLink)
	}

	// Apply template replacements in case the custom prompt includes tags
	replacements := map[string]string{
		"{{recruiter_name}}":  recruiterName,
		"{{contact_name}}":    recruiterName,
		"{{company_name}}":    companyName,
		"{{job_title}}":       jobTitle,
		"{{job_url}}":         jobUrl,
		"{{job_description}}": jobDesc,
		"{{applicant_name}}":  userName,
		"{{applicant_email}}": userEmail,
		"{{resume_content}}":  resumeText,
		"{{drive_link}}":      driveLink,
	}

	for k, v := range replacements {
		finalPrompt = strings.ReplaceAll(finalPrompt, k, v)
	}

	callCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	resp, err := model.GenerateContent(callCtx, genai.Text(finalPrompt))
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

	cleanTxt := strings.TrimSpace(string(txt))
	if strings.HasPrefix(cleanTxt, "```json") {
		cleanTxt = strings.TrimPrefix(cleanTxt, "```json")
		cleanTxt = strings.TrimSuffix(cleanTxt, "```")
		cleanTxt = strings.TrimSpace(cleanTxt)
	}

	if err := json.Unmarshal([]byte(cleanTxt), &emailResult); err != nil {
		return "", "", fmt.Errorf("failed to parse email JSON: %w\nPayload: %s", err, txt)
	}

	return emailResult.Subject, emailResult.Body, nil
}

// GenerateFollowUpEmail generates a polite follow-up reminder email using Gemini.
func (s *GeminiService) GenerateFollowUpEmail(
	ctx context.Context,
	modelName string,
	recruiterName, companyName, userEmail, userName string,
	daysSinceSent int,
	originalSubject, originalBody string,
	reminderNumber int,
) (string, string, error) {
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

	ordinal := "first"
	if reminderNumber == 2 {
		ordinal = "second"
	}

	prompt := fmt.Sprintf(`You are a professional career assistant helping a job seeker follow up with a recruiter.

Write the %s polite follow-up email to a recruiter who has not replied after %d days.

Recruiter Name: %s
Company: %s
Applicant Name: %s
Applicant Email: %s
Original Email Subject: %s

Original Email (for context, DO NOT copy verbatim):
%s

Instructions:
1. Subject: Prefix with "Re: " followed by the original subject.
2. Body: Write in HTML using <p> tags. Be brief (2-3 short paragraphs). Open with a short polite check-in referencing the original email. Re-state key value briefly. End with a clear, low-pressure call to action.
3. Keep a warm, professional, non-pushy tone. This is a %s follow-up reminder.
4. Output ONLY the raw JSON object with keys "subject" and "body". No markdown wrappers.`,
		ordinal, daysSinceSent,
		recruiterName, companyName, userName, userEmail,
		originalSubject, originalBody, ordinal)

	callCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	resp, err := model.GenerateContent(callCtx, genai.Text(prompt))
	if err != nil {
		return "", "", fmt.Errorf("GenerateFollowUpEmail: %w", err)
	}
	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return "", "", fmt.Errorf("no content returned from LLM for follow-up")
	}
	txt, ok := resp.Candidates[0].Content.Parts[0].(genai.Text)
	if !ok {
		return "", "", fmt.Errorf("unexpected response part type for follow-up")
	}
	var result struct {
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}
	if err := json.Unmarshal([]byte(txt), &result); err != nil {
		return "", "", fmt.Errorf("failed to parse follow-up JSON: %w\nPayload: %s", err, txt)
	}
	return result.Subject, result.Body, nil
}

// EnhanceEmailContent takes an existing draft and an enhancement instruction and uses Gemini to refine it.
func (s *GeminiService) EnhanceEmailContent(
	ctx context.Context,
	modelName string,
	currentSubject, currentBody, instruction string,
) (string, string, error) {
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

	prompt := fmt.Sprintf(`You are an elite copywriter and technical editor. Your task is to enhance an existing email draft based on specific user instructions.

Original Email Subject: %s
Original Email Body (HTML):
%s

USER ENHANCEMENT INSTRUCTION:
"%s"

You must rewrite the subject and/or body to satisfy the user's enhancement instruction. 
Ensure the output remains in valid HTML format (using <p> tags for paragraphs) and maintains any important links or placeholders. Do not add markdown code blocks. Output only the JSON.`, currentSubject, currentBody, instruction)

	callCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	resp, err := model.GenerateContent(callCtx, genai.Text(prompt))
	if err != nil {
		return "", "", fmt.Errorf("EnhanceEmailContent failed: %w", err)
	}
	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return "", "", fmt.Errorf("no content returned from LLM for enhancement")
	}
	txt, ok := resp.Candidates[0].Content.Parts[0].(genai.Text)
	if !ok {
		return "", "", fmt.Errorf("unexpected response part type for enhancement")
	}

	cleanTxt := strings.TrimSpace(string(txt))
	if strings.HasPrefix(cleanTxt, "```json") {
		cleanTxt = strings.TrimPrefix(cleanTxt, "```json")
		cleanTxt = strings.TrimSuffix(cleanTxt, "```")
		cleanTxt = strings.TrimSpace(cleanTxt)
	}

	var result struct {
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}
	if err := json.Unmarshal([]byte(cleanTxt), &result); err != nil {
		return "", "", fmt.Errorf("failed to parse enhanced JSON: %w\nPayload: %s", err, txt)
	}
	return result.Subject, result.Body, nil
}
