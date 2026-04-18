package bifrost

import (
	"context"
	"strings"
	"testing"

	schemas "github.com/maximhq/bifrost/core/schemas"
)

func TestSearchRequestValidation(t *testing.T) {
	bifrost := &Bifrost{}

	t.Run("NilRequest", func(t *testing.T) {
		resp, err := bifrost.SearchRequest(nil, nil)
		if resp != nil {
			t.Fatalf("resp = %#v, want nil", resp)
		}
		if err == nil {
			t.Fatal("expected error")
		}
		if err.Error == nil || err.Error.Message != "search request is nil" {
			t.Fatalf("error = %+v", err.Error)
		}
		if err.ExtraFields.RequestType != schemas.SearchRequest {
			t.Fatalf("request type = %q", err.ExtraFields.RequestType)
		}
	})

	t.Run("BlankQuery", func(t *testing.T) {
		req := &schemas.BifrostSearchRequest{
			Provider: schemas.Tavily,
			Model:    "   ",
			Query:    "   ",
		}

		resp, err := bifrost.SearchRequest(nil, req)
		if resp != nil {
			t.Fatalf("resp = %#v, want nil", resp)
		}
		if err == nil {
			t.Fatal("expected error")
		}
		if err.Error == nil || !strings.Contains(err.Error.Message, "query not provided") {
			t.Fatalf("error = %+v", err.Error)
		}
		if err.ExtraFields.RequestType != schemas.SearchRequest {
			t.Fatalf("request type = %q", err.ExtraFields.RequestType)
		}
		if err.ExtraFields.Provider != schemas.Tavily {
			t.Fatalf("provider = %q", err.ExtraFields.Provider)
		}
		if err.ExtraFields.OriginalModelRequested != "   " {
			t.Fatalf("original model = %q", err.ExtraFields.OriginalModelRequested)
		}
		if err.ExtraFields.ResolvedModelUsed != "default" {
			t.Fatalf("resolved model = %q", err.ExtraFields.ResolvedModelUsed)
		}
	})

	t.Run("WhitespaceModelDefaultsToDefault", func(t *testing.T) {
		account := NewMockAccount()
		account.AddProvider(schemas.OpenAI, 1, 1)

		bifrost, err := Init(context.Background(), schemas.BifrostConfig{
			Account: account,
			Logger:  NewDefaultLogger(schemas.LogLevelError),
		})
		if err != nil {
			t.Fatalf("init bifrost: %v", err)
		}
		defer bifrost.Shutdown()

		req := &schemas.BifrostSearchRequest{
			Provider: schemas.OpenAI,
			Model:    "   ",
			Query:    "bifrost search",
		}

		// OpenAI and the current providers still return unsupported Search; this test only
		// verifies req.Model is normalized to "default" before dispatch reaches the provider.
		resp, searchErr := bifrost.SearchRequest(nil, req)
		if resp != nil {
			t.Fatalf("resp = %#v, want nil", resp)
		}
		if searchErr == nil {
			t.Fatal("expected unsupported search error after model normalization")
		}
		if searchErr.Error == nil || searchErr.Error.Message == "" {
			t.Fatalf("error = %+v", searchErr.Error)
		}
		if searchErr.ExtraFields.RequestType != schemas.SearchRequest {
			t.Fatalf("request type = %q", searchErr.ExtraFields.RequestType)
		}
		if searchErr.ExtraFields.Provider != schemas.OpenAI {
			t.Fatalf("provider = %q", searchErr.ExtraFields.Provider)
		}
		if searchErr.ExtraFields.OriginalModelRequested != "default" {
			t.Fatalf("original model = %q", searchErr.ExtraFields.OriginalModelRequested)
		}
		if searchErr.ExtraFields.ResolvedModelUsed != "default" {
			t.Fatalf("resolved model = %q", searchErr.ExtraFields.ResolvedModelUsed)
		}
		if req.Model != "default" {
			t.Fatalf("request model = %q", req.Model)
		}
	})
}

func TestPrepareFallbackRequestClonesSearchRequestParams(t *testing.T) {
	account := NewMockAccount()
	account.AddProvider(schemas.OpenAI, 1, 1)

	bifrost := &Bifrost{
		account: account,
		logger:  NewDefaultLogger(schemas.LogLevelError),
	}

	originalExtraParams := map[string]interface{}{
		"query": "bifrost",
		"limit": 5,
	}
	req := &schemas.BifrostRequest{
		RequestType: schemas.SearchRequest,
		SearchRequest: &schemas.BifrostSearchRequest{
			Provider: schemas.Tavily,
			Model:    "primary-model",
			Query:    "bifrost search",
			Params: &schemas.BifrostSearchParameters{
				MaxResults:    schemas.Ptr(5),
				ExtraParams:   originalExtraParams,
				IncludeAnswer: schemas.Ptr(true),
			},
		},
	}

	fallback := schemas.Fallback{
		Provider: schemas.OpenAI,
		Model:    "fallback-model",
	}

	fallbackReq := bifrost.prepareFallbackRequest(req, fallback)
	if fallbackReq == nil {
		t.Fatal("expected fallback request")
	}
	if fallbackReq.SearchRequest == nil {
		t.Fatal("expected search request on fallback")
	}
	if fallbackReq.SearchRequest.Provider != fallback.Provider {
		t.Fatalf("fallback provider = %q, want %q", fallbackReq.SearchRequest.Provider, fallback.Provider)
	}
	if fallbackReq.SearchRequest.Model != fallback.Model {
		t.Fatalf("fallback model = %q, want %q", fallbackReq.SearchRequest.Model, fallback.Model)
	}
	if fallbackReq.SearchRequest.Params == nil {
		t.Fatal("expected fallback search params")
	}
	if fallbackReq.SearchRequest.Params == req.SearchRequest.Params {
		t.Fatal("params pointer was reused")
	}
	if fallbackReq.SearchRequest.Params.ExtraParams == nil {
		t.Fatal("expected fallback extra params")
	}
	fallbackReq.SearchRequest.Params.ExtraParams["query"] = "mutated"
	fallbackReq.SearchRequest.Params.ExtraParams["new"] = true

	if got := req.SearchRequest.Provider; got != schemas.Tavily {
		t.Fatalf("original provider = %q, want %q", got, schemas.Tavily)
	}
	if got := req.SearchRequest.Model; got != "primary-model" {
		t.Fatalf("original model = %q, want %q", got, "primary-model")
	}
	if got := req.SearchRequest.Params.ExtraParams["query"]; got != "bifrost" {
		t.Fatalf("original extra param query = %#v, want %q", got, "bifrost")
	}
	if got := req.SearchRequest.Params.ExtraParams["limit"]; got != 5 {
		t.Fatalf("original extra param limit = %#v, want %d", got, 5)
	}
	if _, ok := req.SearchRequest.Params.ExtraParams["new"]; ok {
		t.Fatal("original extra params map was mutated")
	}
}
