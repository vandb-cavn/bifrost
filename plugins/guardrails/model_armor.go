package guardrails

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const modelArmorScope = "https://www.googleapis.com/auth/cloud-platform"

type modelArmorClient struct {
	projectID  string
	location   string
	templateID string
	httpClient *http.Client
	// testHTTPEndpoint, if non-empty, replaces the full request URL (unit tests).
	testHTTPEndpoint string
}

func newModelArmorClient(cfg map[string]interface{}, _ *http.Client) (*modelArmorClient, error) {
	projectID, err := strField(cfg, "project_id")
	if err != nil {
		return nil, err
	}
	location, err := strField(cfg, "location")
	if err != nil {
		return nil, err
	}
	templateID, err := strField(cfg, "template_id")
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	var ts oauth2.TokenSource
	if credsB64, ok := cfg["credentials_json"].(string); ok && credsB64 != "" {
		credsJSON, err := base64.StdEncoding.DecodeString(credsB64)
		if err != nil {
			return nil, fmt.Errorf("model_armor: credentials_json base64 decode failed: %w", err)
		}
		creds, err := google.CredentialsFromJSON(ctx, credsJSON, modelArmorScope)
		if err != nil {
			return nil, fmt.Errorf("model_armor: credentials_json parse failed: %w", err)
		}
		ts = creds.TokenSource
	} else {
		var err error
		ts, err = google.DefaultTokenSource(ctx, modelArmorScope)
		if err != nil {
			return nil, fmt.Errorf("model_armor: ADC token source failed: %w", err)
		}
	}

	return &modelArmorClient{
		projectID:  projectID,
		location:   location,
		templateID: templateID,
		httpClient: oauth2.NewClient(ctx, ts),
	}, nil
}

func (c *modelArmorClient) Evaluate(ctx context.Context, content string) (bool, string, error) {
	return c.sanitize(ctx, content, "sanitizeUserPrompt")
}

// EvaluateResponse sanitizes a model response (output rules).
func (c *modelArmorClient) EvaluateResponse(ctx context.Context, content string) (bool, string, error) {
	return c.sanitize(ctx, content, "sanitizeModelResponse")
}

func (c *modelArmorClient) sanitize(ctx context.Context, content, method string) (bool, string, error) {
	var payload map[string]interface{}
	if method == "sanitizeUserPrompt" {
		payload = map[string]interface{}{
			"userPromptData": map[string]interface{}{"text": content},
		}
	} else {
		payload = map[string]interface{}{
			"modelResponseData": map[string]interface{}{"text": content},
		}
	}

	body, _ := json.Marshal(payload)
	postURL := fmt.Sprintf(
		"https://modelarmor.%s.rep.googleapis.com/v1/projects/%s/locations/%s/templates/%s:%s",
		c.location, c.projectID, c.location, c.templateID, method,
	)
	if c.testHTTPEndpoint != "" {
		postURL = c.testHTTPEndpoint
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, bytes.NewReader(body))
	if err != nil {
		return false, "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, "", fmt.Errorf("model_armor request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return false, "", fmt.Errorf("model_armor returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, "", err
	}

	var result struct {
		SanitizationResult struct {
			FilterMatchState string          `json:"filterMatchState"`
			FilterResults    json.RawMessage `json:"filterResults"`
		} `json:"sanitizationResult"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return false, "", fmt.Errorf("model_armor response parse error: %w", err)
	}

	if result.SanitizationResult.FilterMatchState == "MATCH_FOUND" {
		return true, "Google Cloud Model Armor violation — filterMatchState MATCH_FOUND", nil
	}
	return false, "", nil
}
