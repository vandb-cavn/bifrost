package handlers

import (
	"testing"

	schemas "github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
)

func TestPrepareSearchRequest(t *testing.T) {
	var ctx fasthttp.RequestCtx
	ctx.Request.SetRequestURI("/v1/search")
	ctx.Request.Header.SetMethod(fasthttp.MethodPost)
	ctx.Request.SetBody([]byte(`{
		"model":"tavily/default",
		"query":"bifrost gateway",
		"max_results":5,
		"include_answer":true,
		"fallbacks":["brave/default"],
		"search_depth":"advanced"
	}`))

	_, bifrostReq, err := prepareSearchRequest(&ctx)
	if err != nil {
		t.Fatalf("prepareSearchRequest error = %v", err)
	}
	if bifrostReq.Provider != schemas.Tavily || bifrostReq.Model != "default" {
		t.Fatalf("provider/model = %q/%q", bifrostReq.Provider, bifrostReq.Model)
	}
	if bifrostReq.Query != "bifrost gateway" {
		t.Fatalf("query = %q", bifrostReq.Query)
	}
	if bifrostReq.Params == nil || bifrostReq.Params.MaxResults == nil || *bifrostReq.Params.MaxResults != 5 {
		t.Fatalf("params = %+v", bifrostReq.Params)
	}
	if bifrostReq.Params.IncludeAnswer == nil || !*bifrostReq.Params.IncludeAnswer {
		t.Fatalf("include answer = %+v", bifrostReq.Params.IncludeAnswer)
	}
	if bifrostReq.Params.ExtraParams["search_depth"] != "advanced" {
		t.Fatalf("extra params = %+v", bifrostReq.Params.ExtraParams)
	}
	if len(bifrostReq.Fallbacks) != 1 || bifrostReq.Fallbacks[0].Provider != schemas.Brave {
		t.Fatalf("fallbacks = %+v", bifrostReq.Fallbacks)
	}
}

func TestPrepareSearchRequestMissingQuery(t *testing.T) {
	var ctx fasthttp.RequestCtx
	ctx.Request.SetRequestURI("/v1/search")
	ctx.Request.Header.SetMethod(fasthttp.MethodPost)
	ctx.Request.SetBody([]byte(`{"model":"tavily/default"}`))

	_, _, err := prepareSearchRequest(&ctx)
	if err == nil || err.Error() != "query is required for search" {
		t.Fatalf("error = %v", err)
	}
}

func TestPrepareSearchRequestRejectsStream(t *testing.T) {
	var ctx fasthttp.RequestCtx
	ctx.Request.SetRequestURI("/v1/search")
	ctx.Request.Header.SetMethod(fasthttp.MethodPost)
	ctx.Request.SetBody([]byte(`{
		"model":"tavily/default",
		"query":"bifrost gateway",
		"stream":true
	}`))

	_, _, err := prepareSearchRequest(&ctx)
	if err == nil || err.Error() != "stream is not supported for search" {
		t.Fatalf("error = %v", err)
	}
}

func TestSearchPathMapping(t *testing.T) {
	if got := PathToTypeMapping["/v1/search"]; got != schemas.SearchRequest {
		t.Fatalf("path mapping = %q, want %q", got, schemas.SearchRequest)
	}
}
