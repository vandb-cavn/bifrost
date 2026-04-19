package tavily

import (
	"testing"

	schemas "github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
)

func TestToTavilySearchRequest(t *testing.T) {
	t.Parallel()

	req := &schemas.BifrostSearchRequest{
		Query: "bifrost ai gateway",
		Params: &schemas.BifrostSearchParameters{
			MaxResults:        schemas.Ptr(7),
			IncludeAnswer:     schemas.Ptr(true),
			IncludeRawContent: schemas.Ptr(true),
			ExtraParams: map[string]interface{}{
				"search_depth":    "advanced",
				"include_domains": []any{"example.com", "docs.example.com"},
				"exclude_domains": []string{"bad.example.com"},
			},
		},
	}

	result := ToTavilySearchRequest(req)
	if result == nil {
		t.Fatal("expected non-nil request")
	}
	if result.Query != req.Query {
		t.Fatalf("query = %q, want %q", result.Query, req.Query)
	}
	if result.MaxResults == nil || *result.MaxResults != 7 {
		t.Fatalf("max_results = %v, want 7", result.MaxResults)
	}
	if result.IncludeAnswer == nil || !*result.IncludeAnswer {
		t.Fatal("expected include_answer to be true")
	}
	if result.IncludeRawContent == nil || !*result.IncludeRawContent {
		t.Fatal("expected include_raw_content to be true")
	}
	if result.SearchDepth == nil || *result.SearchDepth != "advanced" {
		t.Fatalf("search_depth = %v, want advanced", result.SearchDepth)
	}
	if len(result.IncludeDomains) != 2 || result.IncludeDomains[0] != "example.com" || result.IncludeDomains[1] != "docs.example.com" {
		t.Fatalf("include_domains = %#v", result.IncludeDomains)
	}
	if len(result.ExcludeDomains) != 1 || result.ExcludeDomains[0] != "bad.example.com" {
		t.Fatalf("exclude_domains = %#v", result.ExcludeDomains)
	}
	if _, ok := result.ExtraParams["search_depth"]; ok {
		t.Fatal("expected search_depth to be removed from extra params")
	}
	if _, ok := result.ExtraParams["include_domains"]; ok {
		t.Fatal("expected include_domains to be removed from extra params")
	}
	if _, ok := result.ExtraParams["exclude_domains"]; ok {
		t.Fatal("expected exclude_domains to be removed from extra params")
	}
}

func TestTavilySearchResponseToBifrostSearchResponse(t *testing.T) {
	t.Parallel()

	rawContent := "full raw page content"
	response := &TavilySearchResponse{
		Query:  "bifrost ai gateway",
		Answer: "Bifrost is an AI gateway.",
		Results: []TavilyResult{
			{
				Title:         "Bifrost",
				URL:           "https://example.com/docs/bifrost",
				Content:       "Bifrost is an AI gateway",
				RawContent:    &rawContent,
				Score:         schemas.Ptr(0.93),
				PublishedDate: schemas.Ptr("2025-04-18"),
			},
		},
		Usage: &TavilyUsage{
			Credits: schemas.Ptr(2.0),
		},
	}

	bifrostResp := response.ToBifrostSearchResponse("tavily-default", true)
	if bifrostResp == nil {
		t.Fatal("expected non-nil Bifrost response")
	}
	if bifrostResp.Object != "search_response" {
		t.Fatalf("object = %q, want search_response", bifrostResp.Object)
	}
	if bifrostResp.Query != response.Query {
		t.Fatalf("query = %q, want %q", bifrostResp.Query, response.Query)
	}
	if bifrostResp.Model != "tavily-default" {
		t.Fatalf("model = %q, want tavily-default", bifrostResp.Model)
	}
	if bifrostResp.Answer == nil || *bifrostResp.Answer != response.Answer {
		t.Fatalf("answer = %v, want %q", bifrostResp.Answer, response.Answer)
	}
	if len(bifrostResp.Results) != 1 {
		t.Fatalf("results len = %d, want 1", len(bifrostResp.Results))
	}
	first := bifrostResp.Results[0]
	if first.Title != "Bifrost" || first.URL != "https://example.com/docs/bifrost" {
		t.Fatalf("unexpected mapped result: %#v", first)
	}
	if first.Snippet != "Bifrost is an AI gateway" {
		t.Fatalf("snippet = %q, want mapped snippet", first.Snippet)
	}
	if first.Content == nil || *first.Content != rawContent {
		t.Fatalf("content = %v, want raw content", first.Content)
	}
	if first.PublishedAt == nil || *first.PublishedAt != "2025-04-18" {
		t.Fatalf("published_at = %v, want 2025-04-18", first.PublishedAt)
	}
	if first.Source == nil || *first.Source != "example.com" {
		t.Fatalf("source = %v, want example.com", first.Source)
	}
	if first.Score == nil || *first.Score != 0.93 {
		t.Fatalf("score = %v, want 0.93", first.Score)
	}
	if bifrostResp.Usage == nil || bifrostResp.Usage.Credits == nil || *bifrostResp.Usage.Credits != 2.0 {
		t.Fatalf("usage = %#v, want credits 2.0", bifrostResp.Usage)
	}
	if bifrostResp.Usage.Results != 1 || bifrostResp.Usage.Queries != 1 {
		t.Fatalf("usage counts = %#v, want 1 query and 1 result", bifrostResp.Usage)
	}

	plainResp := response.ToBifrostSearchResponse("tavily-default", false)
	if plainResp.Results[0].Content != nil {
		t.Fatalf("expected nil content when includeRawContent is false, got %#v", plainResp.Results[0].Content)
	}
}

func TestTavilyErrorConverter(t *testing.T) {
	t.Parallel()

	resp := &fasthttp.Response{}
	resp.SetStatusCode(401)
	resp.SetBody([]byte(`{"error":{"message":"invalid api key","code":"bad_key"}}`))

	bifrostErr := TavilyErrorConverter(resp, schemas.SearchRequest, schemas.Tavily, "default")
	if bifrostErr == nil {
		t.Fatal("expected non-nil error")
	}
	if bifrostErr.Error == nil {
		t.Fatal("expected error details")
	}
	if bifrostErr.Error.Message != "invalid api key" {
		t.Fatalf("message = %q, want invalid api key", bifrostErr.Error.Message)
	}
	if bifrostErr.Error.Code == nil || *bifrostErr.Error.Code != "bad_key" {
		t.Fatalf("code = %v, want bad_key", bifrostErr.Error.Code)
	}
	if bifrostErr.ExtraFields.RequestType != schemas.SearchRequest {
		t.Fatalf("request_type = %q, want search", bifrostErr.ExtraFields.RequestType)
	}
	if bifrostErr.ExtraFields.Provider != schemas.Tavily {
		t.Fatalf("provider = %q, want tavily", bifrostErr.ExtraFields.Provider)
	}
}
