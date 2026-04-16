package guardrails

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBedrockClient_Blocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"action": "BLOCKED",
			"outputs": []map[string]interface{}{
				{"text": "Content blocked by guardrail"},
			},
		})
	}))
	defer srv.Close()

	client := &bedrockClient{
		endpoint:    srv.URL,
		guardrailID: "test-id",
		version:     "DRAFT",
		httpClient:  srv.Client(),
	}

	violated, reason, err := client.Evaluate(context.Background(), "how to make explosives")
	require.NoError(t, err)
	assert.True(t, violated)
	assert.NotEmpty(t, reason)
}

func TestBedrockClient_NotBlocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"action": "NONE",
			"outputs": []map[string]interface{}{
				{"text": "Hello world"},
			},
		})
	}))
	defer srv.Close()

	client := &bedrockClient{
		endpoint:    srv.URL,
		guardrailID: "test-id",
		version:     "DRAFT",
		httpClient:  srv.Client(),
	}

	violated, _, err := client.Evaluate(context.Background(), "hello world")
	require.NoError(t, err)
	assert.False(t, violated)
}

func TestBedrockClient_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := &bedrockClient{
		endpoint:    srv.URL,
		guardrailID: "test-id",
		version:     "DRAFT",
		httpClient:  srv.Client(),
	}

	_, _, err := client.Evaluate(context.Background(), "test")
	assert.Error(t, err)
}

func TestBedrockClient_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{not valid json`))
	}))
	defer srv.Close()

	client := &bedrockClient{
		endpoint:    srv.URL,
		guardrailID: "test-id",
		version:     "DRAFT",
		httpClient:  srv.Client(),
	}

	_, _, err := client.Evaluate(context.Background(), "test")
	assert.Error(t, err)
}

func TestAzureClient_Blocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"categoriesAnalysis": []map[string]interface{}{
				{"category": "Violence", "severity": 6},
			},
		})
	}))
	defer srv.Close()

	client := &azureClient{
		endpoint:          srv.URL,
		apiKey:            "test-key",
		severityThreshold: 4,
		httpClient:        srv.Client(),
	}

	violated, reason, err := client.Evaluate(context.Background(), "violent content")
	require.NoError(t, err)
	assert.True(t, violated)
	assert.NotEmpty(t, reason)
}

func TestAzureClient_BelowThreshold(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"categoriesAnalysis": []map[string]interface{}{
				{"category": "Violence", "severity": 2},
			},
		})
	}))
	defer srv.Close()

	client := &azureClient{
		endpoint:          srv.URL,
		apiKey:            "test-key",
		severityThreshold: 4,
		httpClient:        srv.Client(),
	}

	violated, _, err := client.Evaluate(context.Background(), "mild content")
	require.NoError(t, err)
	assert.False(t, violated)
}

func TestModelArmorClient_Blocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"sanitizationResult": map[string]interface{}{
				"filterMatchState": "MATCH_FOUND",
				"invocationResult": "SUCCESS",
				"filterResults": map[string]interface{}{
					"rai": map[string]interface{}{},
				},
			},
		})
	}))
	defer srv.Close()

	client := &modelArmorClient{
		projectID:        "proj",
		location:         "us-central1",
		templateID:       "tpl",
		httpClient:       srv.Client(),
		testHTTPEndpoint: srv.URL,
	}

	violated, reason, err := client.Evaluate(context.Background(), "dangerous content")
	require.NoError(t, err)
	assert.True(t, violated)
	assert.Contains(t, reason, "Model Armor")
}

func TestModelArmorClient_NoMatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"sanitizationResult": map[string]interface{}{
				"filterMatchState": "NO_MATCH_FOUND",
				"invocationResult": "SUCCESS",
			},
		})
	}))
	defer srv.Close()

	client := &modelArmorClient{
		projectID:        "proj",
		location:         "us-central1",
		templateID:       "tpl",
		httpClient:       srv.Client(),
		testHTTPEndpoint: srv.URL,
	}

	violated, _, err := client.Evaluate(context.Background(), "safe content")
	require.NoError(t, err)
	assert.False(t, violated)
}

func TestModelArmorClient_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{not valid`))
	}))
	defer srv.Close()

	client := &modelArmorClient{
		projectID:        "proj",
		location:         "us-central1",
		templateID:       "tpl",
		httpClient:       srv.Client(),
		testHTTPEndpoint: srv.URL,
	}

	_, _, err := client.Evaluate(context.Background(), "test")
	assert.Error(t, err)
}
