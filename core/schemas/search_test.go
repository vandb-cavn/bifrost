package schemas

import "testing"

func TestBifrostSearchRequestGetRawRequestBody(t *testing.T) {
	req := &BifrostSearchRequest{RawRequestBody: []byte(`{"query":"bifrost"}`)}
	if got := string(req.GetRawRequestBody()); got != `{"query":"bifrost"}` {
		t.Fatalf("GetRawRequestBody() = %q", got)
	}
}

func TestBifrostRequestSearchFields(t *testing.T) {
	req := &BifrostRequest{
		RequestType: SearchRequest,
		SearchRequest: &BifrostSearchRequest{
			Provider: Tavily,
			Model:    "default",
			Query:    "bifrost search",
			Fallbacks: []Fallback{{
				Provider: Brave,
				Model:    "default",
			}},
		},
	}

	provider, model, fallbacks := req.GetRequestFields()
	if provider != Tavily || model != "default" || len(fallbacks) != 1 || fallbacks[0].Provider != Brave {
		t.Fatalf("GetRequestFields() = (%q, %q, %+v)", provider, model, fallbacks)
	}

	req.SetProvider(Exa)
	req.SetModel("exa-default")
	req.SetFallbacks(nil)
	req.SetRawRequestBody([]byte(`{"provider":"exa_ai"}`))

	if req.SearchRequest.Provider != Exa {
		t.Fatalf("provider = %q", req.SearchRequest.Provider)
	}
	if req.SearchRequest.Model != "exa-default" {
		t.Fatalf("model = %q", req.SearchRequest.Model)
	}
	if len(req.SearchRequest.Fallbacks) != 0 {
		t.Fatalf("fallbacks = %+v", req.SearchRequest.Fallbacks)
	}
	if string(req.SearchRequest.RawRequestBody) != `{"provider":"exa_ai"}` {
		t.Fatalf("raw body = %q", req.SearchRequest.RawRequestBody)
	}
}

func TestBifrostResponseSearchExtraFields(t *testing.T) {
	resp := &BifrostResponse{
		SearchResponse: &BifrostSearchResponse{},
	}
	resp.PopulateExtraFields(SearchRequest, Tavily, "default", "resolved")

	extra := resp.GetExtraFields()
	if extra.RequestType != SearchRequest {
		t.Fatalf("request type = %q", extra.RequestType)
	}
	if extra.Provider != Tavily {
		t.Fatalf("provider = %q", extra.Provider)
	}
	if extra.OriginalModelRequested != "default" || extra.ResolvedModelUsed != "resolved" {
		t.Fatalf("model fields = %+v", extra)
	}
}

func TestAllowedRequestsSearchOperation(t *testing.T) {
	allowed := &AllowedRequests{Search: true}

	if !allowed.IsOperationAllowed(SearchRequest) {
		t.Fatal("expected search requests to be allowed")
	}
}
