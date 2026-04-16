package guardrails

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type azureClient struct {
	endpoint          string
	apiKey            string
	severityThreshold int
	httpClient        *http.Client
}

func newAzureClient(cfg map[string]interface{}, hc *http.Client) (*azureClient, error) {
	endpoint, err := strField(cfg, "endpoint")
	if err != nil {
		return nil, err
	}
	apiKey, err := strField(cfg, "api_key")
	if err != nil {
		return nil, err
	}
	threshold := intFieldOr(cfg, "severity_threshold", 4)
	return &azureClient{endpoint: endpoint, apiKey: apiKey, severityThreshold: threshold, httpClient: hc}, nil
}

func (c *azureClient) Evaluate(ctx context.Context, content string) (bool, string, error) {
	payload := map[string]interface{}{
		"text":       content,
		"categories": []string{"Hate", "Violence", "Sexual", "SelfHarm"},
	}
	body, _ := json.Marshal(payload)

	url := fmt.Sprintf("%s/contentsafety/text:analyze?api-version=2023-10-01", c.endpoint)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return false, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Ocp-Apim-Subscription-Key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, "", fmt.Errorf("azure request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, "", fmt.Errorf("azure returned HTTP %d", resp.StatusCode)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, "", err
	}

	var result struct {
		CategoriesAnalysis []struct {
			Category string `json:"category"`
			Severity int    `json:"severity"`
		} `json:"categoriesAnalysis"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return false, "", fmt.Errorf("azure response parse error: %w", err)
	}

	for _, cat := range result.CategoriesAnalysis {
		if cat.Severity >= c.severityThreshold {
			return true, fmt.Sprintf("Azure Content Safety: %s severity %d ≥ threshold %d",
				cat.Category, cat.Severity, c.severityThreshold), nil
		}
	}
	return false, "", nil
}
