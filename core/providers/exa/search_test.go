// Package exa implements the Exa provider and its utility functions.
package exa

import (
	"testing"

	schemas "github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
)

func TestToExaSearchRequest(t *testing.T) {
	t.Parallel()

	maxResults := 3
	includeRaw := true
	country := "US"
	req := ToExaSearchRequest(&schemas.BifrostSearchRequest{
		Query: "bifrost gateway",
		Params: &schemas.BifrostSearchParameters{
			MaxResults:        &maxResults,
			Country:           &country,
			IncludeRawContent: &includeRaw,
			ExtraParams: map[string]interface{}{
				"type":         "neural",
				"numResults":   20,
				"userLocation": "CA",
				"stream":       true,
				"passthrough":  "ok",
			},
		},
	})

	if req == nil {
		t.Fatal("expected non-nil request")
	}
	if req.Query != "bifrost gateway" || req.NumResults == nil || *req.NumResults != 3 {
		t.Fatalf("request = %+v", req)
	}
	if req.UserLocation == nil || *req.UserLocation != "US" {
		t.Fatalf("userLocation = %+v", req.UserLocation)
	}
	if req.Type == nil || *req.Type != "neural" {
		t.Fatalf("type = %+v", req.Type)
	}
	if req.NumResults == nil || *req.NumResults != 3 {
		t.Fatalf("numResults = %+v", req.NumResults)
	}
	if req.Contents == nil || req.Contents.Text == nil || !*req.Contents.Text {
		t.Fatalf("contents = %+v", req.Contents)
	}
	if req.Stream != nil {
		t.Fatalf("stream = %+v", req.Stream)
	}
	if _, ok := req.ExtraParams["numResults"]; ok {
		t.Fatal("expected numResults to be removed from extra params")
	}
	if _, ok := req.ExtraParams["userLocation"]; ok {
		t.Fatal("expected userLocation to be removed from extra params")
	}
	if _, ok := req.ExtraParams["stream"]; ok {
		t.Fatal("expected stream to be removed from extra params")
	}
	if passthrough, ok := req.ExtraParams["passthrough"]; !ok || passthrough != "ok" {
		t.Fatalf("passthrough = %#v, want ok", passthrough)
	}

	fallback := ToExaSearchRequest(&schemas.BifrostSearchRequest{
		Query: "fallback",
		Params: &schemas.BifrostSearchParameters{
			ExtraParams: map[string]interface{}{
				"numResults":   11,
				"userLocation": "GB",
				"stream":       true,
			},
		},
	})
	if fallback == nil {
		t.Fatal("expected non-nil fallback request")
	}
	if fallback.NumResults == nil || *fallback.NumResults != 11 {
		t.Fatalf("fallback numResults = %+v", fallback.NumResults)
	}
	if fallback.UserLocation == nil || *fallback.UserLocation != "GB" {
		t.Fatalf("fallback userLocation = %+v", fallback.UserLocation)
	}
	if fallback.Stream != nil {
		t.Fatalf("fallback stream = %+v", fallback.Stream)
	}
}

func TestExaSearchResponseToBifrost(t *testing.T) {
	t.Parallel()

	score := 0.77
	text := "full content"
	published := "2026-04-01T00:00:00.000Z"
	highlight := "snippet"
	resp := (&ExaSearchResponse{
		Results: []ExaResult{{
			Title:         "Bifrost",
			URL:           "https://example.com",
			Text:          &text,
			Highlights:    []string{highlight},
			Score:         &score,
			PublishedDate: &published,
		}},
		CostDollars: &ExaCostDollars{
			Total: schemas.Ptr(1.75),
		},
	}).ToBifrostSearchResponse("default", "bifrost gateway", true)

	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp.Object != "search" || resp.Query != "bifrost gateway" {
		t.Fatalf("response = %+v", resp)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("results = %+v", resp.Results)
	}
	if resp.Results[0].Snippet != highlight {
		t.Fatalf("snippet = %q, want %q", resp.Results[0].Snippet, highlight)
	}
	if resp.Results[0].Content == nil || *resp.Results[0].Content != text {
		t.Fatalf("content = %+v", resp.Results[0].Content)
	}
	if resp.Results[0].PublishedAt == nil || *resp.Results[0].PublishedAt != published {
		t.Fatalf("published_at = %+v", resp.Results[0].PublishedAt)
	}
	if resp.Results[0].Source == nil || *resp.Results[0].Source != "example.com" {
		t.Fatalf("source = %+v", resp.Results[0].Source)
	}
	if resp.Usage == nil || resp.Usage.Queries != 1 || resp.Usage.Results != 1 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
	if resp.Usage.Credits == nil || *resp.Usage.Credits != 1.75 {
		t.Fatalf("credits = %+v", resp.Usage.Credits)
	}
	if resp.Results[0].Score == nil || *resp.Results[0].Score != score {
		t.Fatalf("score = %+v", resp.Results[0].Score)
	}
}

func TestExaErrorConverter(t *testing.T) {
	t.Parallel()

	resp := &fasthttp.Response{}
	resp.SetStatusCode(400)
	resp.SetBody([]byte(`{"requestId":"abc123","error":"invalid request body"}`))

	bifrostErr := ExaErrorConverter(resp, schemas.SearchRequest, schemas.Exa, "default")
	if bifrostErr == nil {
		t.Fatal("expected non-nil error")
	}
	if bifrostErr.Error == nil || bifrostErr.Error.Message != "invalid request body" {
		t.Fatalf("error = %+v", bifrostErr.Error)
	}
	if bifrostErr.ExtraFields.RequestType != schemas.SearchRequest {
		t.Fatalf("request_type = %q", bifrostErr.ExtraFields.RequestType)
	}
	if bifrostErr.ExtraFields.Provider != schemas.Exa {
		t.Fatalf("provider = %q", bifrostErr.ExtraFields.Provider)
	}
}
