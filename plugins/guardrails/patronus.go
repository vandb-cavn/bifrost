package guardrails

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type patronusClient struct {
	endpoint   string
	apiKey     string
	evaluator  string
	httpClient *http.Client
}

func newPatronusClient(cfg map[string]interface{}, hc *http.Client) (*patronusClient, error) {
	endpoint, err := strField(cfg, "endpoint")
	if err != nil {
		return nil, err
	}
	apiKey, err := strField(cfg, "api_key")
	if err != nil {
		return nil, err
	}
	evaluator, _ := cfg["evaluator"].(string)
	if evaluator == "" {
		evaluator = "lynx"
	}
	return &patronusClient{endpoint: endpoint, apiKey: apiKey, evaluator: evaluator, httpClient: hc}, nil
}

func (c *patronusClient) Evaluate(ctx context.Context, content string) (bool, string, error) {
	payload := map[string]interface{}{
		"evaluators":             []map[string]interface{}{{"evaluator": c.evaluator}},
		"evaluated_model_output": content,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/v1/evaluate", bytes.NewReader(body))
	if err != nil {
		return false, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, "", fmt.Errorf("patronus request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, "", fmt.Errorf("patronus returned HTTP %d", resp.StatusCode)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, "", err
	}

	var result struct {
		Results []struct {
			Pass   bool   `json:"pass"`
			Reason string `json:"reason"`
		} `json:"results"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return false, "", fmt.Errorf("patronus response parse error: %w", err)
	}

	for _, r := range result.Results {
		if !r.Pass {
			return true, fmt.Sprintf("Patronus AI evaluation failed: %s", r.Reason), nil
		}
	}
	return false, "", nil
}
