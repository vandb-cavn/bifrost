package bifrost

import (
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
			Model:    "",
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
		if err.ExtraFields.OriginalModelRequested != "" {
			t.Fatalf("original model = %q", err.ExtraFields.OriginalModelRequested)
		}
		if err.ExtraFields.ResolvedModelUsed != "" {
			t.Fatalf("resolved model = %q", err.ExtraFields.ResolvedModelUsed)
		}
	})
}
