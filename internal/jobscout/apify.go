package jobscout

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// ApifyClient makes REST calls to the Apify API
type ApifyClient struct {
	apiKey     string
	httpClient *http.Client
}

// ApifyRunInput is the payload sent to start the LinkedIn scraper actor
type ApifyRunInput struct {
	URLs          []string `json:"urls"`
	Count         int      `json:"count,omitempty"`
	ScrapeCompany bool     `json:"scrapeCompany"`
}

// ApifyRunResponse is the response from starting a run
type ApifyRunResponse struct {
	Data struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"data"`
}

// ApifyRunStatus is the response from checking run status
type ApifyRunStatus struct {
	Data struct {
		ID        string `json:"id"`
		Status    string `json:"status"` // READY, RUNNING, SUCCEEDED, FAILED, ABORTED, TIMED-OUT
		DatasetID string `json:"defaultDatasetId"`
	} `json:"data"`
}

// ApifyLinkedInJob is a single job listing from the dataset
type ApifyLinkedInJob struct {
	Title           string `json:"title"`
	Company         string `json:"companyName"`
	CompanyURL      string `json:"companyUrl"`
	CompanyLogo     string `json:"companyLogo"`
	Location        string `json:"location"`
	Description     string `json:"description"`
	DescriptionText string `json:"descriptionText"`
	URL             string `json:"url"`
	Link            string `json:"link"`
	ApplyURL        string `json:"applyUrl"`
	InputURL        string `json:"inputUrl"`
	Salary          string `json:"salary"`
	JobType         string `json:"contractType"`
	EmploymentType  string `json:"employmentType"`
	ExperienceLevel string `json:"experienceLevel"`
	SeniorityLevel  string `json:"seniorityLevel"`
	PostedAt        string `json:"postedAt"`
	ApplicantCount  string `json:"applicantsCount"`
}

func (j *ApifyLinkedInJob) GetURL() string {
	if j.Link != "" {
		return j.Link
	}
	if j.URL != "" {
		return j.URL
	}
	if j.ApplyURL != "" {
		return j.ApplyURL
	}
	return j.InputURL
}

func (j *ApifyLinkedInJob) GetDescription() string {
	if j.Description != "" {
		return j.Description
	}
	return j.DescriptionText
}

func (j *ApifyLinkedInJob) GetJobType() string {
	if j.EmploymentType != "" {
		return j.EmploymentType
	}
	return j.JobType
}

func (j *ApifyLinkedInJob) GetExperienceLevel() string {
	if j.SeniorityLevel != "" {
		return j.SeniorityLevel
	}
	return j.ExperienceLevel
}

func NewApifyClient(apiKey string) *ApifyClient {
	return &ApifyClient{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *ApifyClient) StartRun(actorID string, input ApifyRunInput) (string, error) {
	formattedActorID := strings.ReplaceAll(actorID, "/", "~")
	url := fmt.Sprintf("https://api.apify.com/v2/acts/%s/runs", formattedActorID)

	slog.Info("apify: starting actor run",
		"actor_id", formattedActorID,
		"url_count", len(input.URLs),
		"count", input.Count,
		"scrape_company", input.ScrapeCompany,
	)

	bodyBytes, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("failed to marshal input: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		slog.Error("apify: HTTP request to start run failed", "actor_id", formattedActorID, "error", err)
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		slog.Error("apify: start run returned error status",
			"actor_id", formattedActorID,
			"status_code", resp.StatusCode,
			"response_body", string(b),
		)
		return "", fmt.Errorf("apify error (status %d): %s", resp.StatusCode, string(b))
	}

	var runResp ApifyRunResponse
	if err := json.NewDecoder(resp.Body).Decode(&runResp); err != nil {
		slog.Error("apify: failed to decode start run response", "actor_id", formattedActorID, "error", err)
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	slog.Info("apify: actor run started successfully",
		"actor_id", formattedActorID,
		"apify_run_id", runResp.Data.ID,
		"initial_status", runResp.Data.Status,
	)
	return runResp.Data.ID, nil
}

func (c *ApifyClient) GetRunStatus(runID string) (*ApifyRunStatus, error) {
	url := fmt.Sprintf("https://api.apify.com/v2/actor-runs/%s", runID)

	slog.Debug("apify: polling run status", "apify_run_id", runID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		slog.Error("apify: HTTP request to get run status failed", "apify_run_id", runID, "error", err)
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		slog.Error("apify: get run status returned error status",
			"apify_run_id", runID,
			"status_code", resp.StatusCode,
			"response_body", string(b),
		)
		return nil, fmt.Errorf("apify error (status %d): %s", resp.StatusCode, string(b))
	}

	var statusResp ApifyRunStatus
	if err := json.NewDecoder(resp.Body).Decode(&statusResp); err != nil {
		slog.Error("apify: failed to decode run status response", "apify_run_id", runID, "error", err)
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	slog.Debug("apify: polled run status",
		"apify_run_id", runID,
		"status", statusResp.Data.Status,
		"dataset_id", statusResp.Data.DatasetID,
	)
	return &statusResp, nil
}

func (c *ApifyClient) GetDatasetItems(datasetID string) ([]ApifyLinkedInJob, error) {
	url := fmt.Sprintf("https://api.apify.com/v2/datasets/%s/items", datasetID)

	slog.Info("apify: fetching dataset items", "dataset_id", datasetID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		slog.Error("apify: HTTP request to get dataset items failed", "dataset_id", datasetID, "error", err)
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		slog.Error("apify: get dataset items returned error status",
			"dataset_id", datasetID,
			"status_code", resp.StatusCode,
			"response_body", string(b),
		)
		return nil, fmt.Errorf("apify error (status %d): %s", resp.StatusCode, string(b))
	}

	var items []ApifyLinkedInJob
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		slog.Error("apify: failed to decode dataset items", "dataset_id", datasetID, "error", err)
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	slog.Info("apify: dataset items fetched successfully", "dataset_id", datasetID, "item_count", len(items))
	return items, nil
}
