package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/druva06/recruit-ingest/internal/models"
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
