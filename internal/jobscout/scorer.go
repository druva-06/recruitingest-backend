package jobscout

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
)

type FitScorer struct {
	client *genai.Client
	model  string
}

type FitScoreResult struct {
	FitScore       float64  `json:"fit_score"`       // 0-100
	Reasoning      string   `json:"reasoning"`       // 2-3 sentence explanation
	MatchingSkills []string `json:"matching_skills"` // Skills candidate has
	MissingSkills  []string `json:"missing_skills"`  // Skills candidate lacks
}

func NewFitScorer(ctx context.Context, apiKey, modelName string) (*FitScorer, error) {
	slog.Info("jobscout scorer: initializing Gemini client", "model", modelName)
	if apiKey == "" {
		slog.Error("jobscout scorer: Gemini API key is empty — scoring will fail")
		return nil, fmt.Errorf("gemini API key is empty")
	}
	client, err := genai.NewClient(ctx, option.WithAPIKey(apiKey))
	if err != nil {
		slog.Error("jobscout scorer: failed to create Gemini client", "model", modelName, "error", err)
		return nil, fmt.Errorf("failed to create genai client: %w", err)
	}
	slog.Info("jobscout scorer: Gemini client initialized successfully", "model", modelName)
	return &FitScorer{
		client: client,
		model:  modelName,
	}, nil
}

func (s *FitScorer) ScoreJob(ctx context.Context, job *JobScoutJob, resumeText string, customPrompt string) (*FitScoreResult, error) {
	usingCustomPrompt := customPrompt != ""
	promptTemplate := customPrompt
	if promptTemplate == "" {
		promptTemplate = `You are an expert career matching assistant. Evaluate how well this candidate 
matches the job based on skills, experience, and qualifications.

=== JOB POSTING ===
Title: {{TITLE}}
Company: {{COMPANY}}
Location: {{LOCATION}}
Description:
{{DESCRIPTION}}

=== CANDIDATE PROFILE/RESUME ===
{{CANDIDATE_INFO}}

=== INSTRUCTIONS ===
Return a JSON object:
{
  "fit_score": <number 0-100>,
  "reasoning": "<2-3 sentence explanation of the match quality>",
  "matching_skills": ["skill1", "skill2", ...],
  "missing_skills": ["skill1", "skill2", ...]
}

Scoring Guide:
- 90-100: Perfect match — all key requirements met
- 75-89: Strong match — most requirements met, minor gaps
- 50-74: Partial match — some relevant experience but notable gaps
- 25-49: Weak match — few overlapping skills
- 0-24: Poor match — very little relevance`
	}

	prompt := strings.ReplaceAll(promptTemplate, "{{TITLE}}", job.Title)
	prompt = strings.ReplaceAll(prompt, "{{COMPANY}}", job.Company)
	prompt = strings.ReplaceAll(prompt, "{{LOCATION}}", job.Location)
	prompt = strings.ReplaceAll(prompt, "{{DESCRIPTION}}", job.Description)
	prompt = strings.ReplaceAll(prompt, "{{CANDIDATE_INFO}}", resumeText)

	slog.Info("jobscout scorer: scoring job",
		"job_id", job.ID,
		"job_title", job.Title,
		"company", job.Company,
		"model", s.model,
		"using_custom_prompt", usingCustomPrompt,
		"candidate_info_len", len(resumeText),
	)

	model := s.client.GenerativeModel(s.model)
	model.ResponseMIMEType = "application/json"
	temp := float32(0.2)
	model.Temperature = &temp

	res, err := model.GenerateContent(ctx, genai.Text(prompt))
	if err != nil {
		slog.Error("jobscout scorer: Gemini GenerateContent failed",
			"job_id", job.ID,
			"job_title", job.Title,
			"model", s.model,
			"error", err,
		)
		return nil, fmt.Errorf("failed to generate content: %w", err)
	}

	if len(res.Candidates) == 0 || len(res.Candidates[0].Content.Parts) == 0 {
		slog.Error("jobscout scorer: Gemini returned empty response",
			"job_id", job.ID,
			"job_title", job.Title,
			"candidate_count", len(res.Candidates),
		)
		return nil, fmt.Errorf("empty response from gemini")
	}

	part := res.Candidates[0].Content.Parts[0]
	txt, ok := part.(genai.Text)
	if !ok {
		slog.Error("jobscout scorer: unexpected response part type from Gemini",
			"job_id", job.ID,
			"job_title", job.Title,
		)
		return nil, fmt.Errorf("unexpected part type from gemini")
	}
	text := string(txt)

	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimSuffix(text, "```")
	text = strings.TrimSpace(text)

	var result FitScoreResult
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		slog.Error("jobscout scorer: failed to parse JSON response from Gemini",
			"job_id", job.ID,
			"job_title", job.Title,
			"raw_response", text,
			"error", err,
		)
		return nil, fmt.Errorf("failed to parse JSON response: %w", err)
	}

	slog.Info("jobscout scorer: job scored successfully",
		"job_id", job.ID,
		"job_title", job.Title,
		"company", job.Company,
		"fit_score", result.FitScore,
		"matching_skills_count", len(result.MatchingSkills),
		"missing_skills_count", len(result.MissingSkills),
	)
	return &result, nil
}

func (s *FitScorer) Close() {
	// Not needed for genai.Client
}
