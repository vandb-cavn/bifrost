package guardrails

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type graySwanClient struct {
	endpoint       string
	apiKey         string
	scoreThreshold float64
	httpClient     *http.Client
}

func newGraySwanClient(cfg map[string]interface{}, hc *http.Client) (*graySwanClient, error) {
	endpoint, err := strField(cfg, "endpoint")
	if err != nil {
		return nil, err
	}
	apiKey, err := strField(cfg, "api_key")
	if err != nil {
		return nil, err
	}
	threshold := 0.5
	if v, ok := cfg["score_threshold"].(float64); ok {
		threshold = v
	}
	return &graySwanClient{endpoint: endpoint, apiKey: apiKey, scoreThreshold: threshold, httpClient: hc}, nil
}

func (c *graySwanClient) Evaluate(ctx context.Context, content string) (bool, string, error) {
	payload := map[string]interface{}{"text": content}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/evaluate", bytes.NewReader(body))
	if err != nil {
		return false, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, "", fmt.Errorf("grayswan request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, "", fmt.Errorf("grayswan returned HTTP %d", resp.StatusCode)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, "", err
	}

	var result struct {
		Score  float64 `json:"score"`
		Reason string  `json:"reason"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return false, "", fmt.Errorf("grayswan response parse error: %w", err)
	}

	if result.Score >= c.scoreThreshold {
		return true, fmt.Sprintf("GraySwan violation score %.2f ≥ threshold %.2f: %s",
			result.Score, c.scoreThreshold, result.Reason), nil
	}
	return false, "", nil
}
