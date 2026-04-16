package guardrails

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type bedrockClient struct {
	endpoint    string
	guardrailID string
	version     string
	httpClient  *http.Client
}

func newBedrockClient(cfg map[string]interface{}, hc *http.Client) (*bedrockClient, error) {
	endpoint, err := strField(cfg, "endpoint")
	if err != nil {
		return nil, err
	}
	guardrailID, err := strField(cfg, "guardrail_id")
	if err != nil {
		return nil, err
	}
	version, _ := cfg["version"].(string)
	if version == "" {
		version = "DRAFT"
	}
	return &bedrockClient{endpoint: endpoint, guardrailID: guardrailID, version: version, httpClient: hc}, nil
}

func (c *bedrockClient) Evaluate(ctx context.Context, content string) (bool, string, error) {
	payload := map[string]interface{}{
		"source": "INPUT",
		"content": []map[string]interface{}{
			{"text": map[string]interface{}{"text": content}},
		},
	}
	body, _ := json.Marshal(payload)

	url := fmt.Sprintf("%s/guardrails/%s/apply?guardrailVersion=%s", c.endpoint, c.guardrailID, c.version)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return false, "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, "", fmt.Errorf("bedrock request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, "", fmt.Errorf("bedrock returned HTTP %d", resp.StatusCode)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, "", err
	}

	var result struct {
		Action  string `json:"action"`
		Outputs []struct {
			Text string `json:"text"`
		} `json:"outputs"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return false, "", fmt.Errorf("bedrock response parse error: %w", err)
	}

	if result.Action == "BLOCKED" {
		reason := "blocked by Bedrock guardrail"
		if len(result.Outputs) > 0 {
			reason = result.Outputs[0].Text
		}
		return true, reason, nil
	}
	return false, "", nil
}
