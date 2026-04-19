// Package brave implements the Brave provider and its utility functions.
package brave

import (
	"testing"

	schemas "github.com/maximhq/bifrost/core/schemas"
)

func TestToBraveSearchQuery(t *testing.T) {
	t.Parallel()

	count := 5
	country := "US"
	language := "en"
	req := &schemas.BifrostSearchRequest{
		Query: "bifrost gateway",
		Params: &schemas.BifrostSearchParameters{
			MaxResults: &count,
			Country:    &country,
			Language:   &language,
		},
	}

	query := ToBraveSearchQuery(req)
	if got := query.Get("q"); got != "bifrost gateway" {
		t.Fatalf("q = %q, want bifrost gateway", got)
	}
	if got := query.Get("count"); got != "5" {
		t.Fatalf("count = %q, want 5", got)
	}
	if got := query.Get("country"); got != "US" {
		t.Fatalf("country = %q, want US", got)
	}
	if got := query.Get("search_lang"); got != "en" {
		t.Fatalf("search_lang = %q, want en", got)
	}
}

func TestToBraveSearchQueryPrefersStructuredParams(t *testing.T) {
	t.Parallel()

	count := 5
	country := "US"
	language := "en"
	req := &schemas.BifrostSearchRequest{
		Query: "bifrost gateway",
		Params: &schemas.BifrostSearchParameters{
			MaxResults: &count,
			Country:    &country,
			Language:   &language,
			ExtraParams: map[string]interface{}{
				"count":       11,
				"country":     "FR",
				"search_lang": "fr",
				"language":    "de",
			},
		},
	}

	query := ToBraveSearchQuery(req)
	if got := query.Get("count"); got != "5" {
		t.Fatalf("count = %q, want 5", got)
	}
	if got := query.Get("country"); got != "US" {
		t.Fatalf("country = %q, want US", got)
	}
	if got := query.Get("search_lang"); got != "en" {
		t.Fatalf("search_lang = %q, want en", got)
	}
}

func TestBraveSearchResponseToBifrost(t *testing.T) {
	t.Parallel()

	resp := (&BraveSearchResponse{
		Web: &BraveWebResults{
			Results: []BraveResult{{
				Title:       "Bifrost",
				URL:         "https://example.com",
				Description: "snippet",
				Age:         "2026-04-01",
			}},
		},
	}).ToBifrostSearchResponse("default", "bifrost gateway")

	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp.Object != "search" || resp.Query != "bifrost gateway" {
		t.Fatalf("response = %+v", resp)
	}
	if len(resp.Results) != 1 || resp.Results[0].Snippet != "snippet" {
		t.Fatalf("results = %+v", resp.Results)
	}
	if resp.Results[0].PublishedAt == nil || *resp.Results[0].PublishedAt != "2026-04-01" {
		t.Fatalf("published_at = %v, want 2026-04-01", resp.Results[0].PublishedAt)
	}
	if resp.Results[0].Source == nil || *resp.Results[0].Source != "brave" {
		t.Fatalf("source = %v, want brave", resp.Results[0].Source)
	}
	if resp.Usage == nil || resp.Usage.Queries != 1 || resp.Usage.Results != 1 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}
