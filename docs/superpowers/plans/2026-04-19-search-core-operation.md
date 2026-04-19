# Search Core Operation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `/v1/search` as a first-class Bifrost core operation with Tavily, Brave, and Exa provider support.

**Architecture:** Search follows the existing `Rerank` and `OCR` operation shape: schemas define the request/response, `Bifrost.SearchRequest` calls `handleRequest`, `handleProviderRequest` dispatches to `Provider.Search`, and transport/logging/pricing/governance are wired by request type. Provider converters normalize each vendor response into `BifrostSearchResponse` and keep raw data in `ExtraFields` when raw-response config is enabled.

**Tech Stack:** Go 1.26 workspace, `fasthttp`, `sonic`, existing Bifrost provider utilities, existing logging/governance/model catalog plugins, JSON schema in `transports/config.schema.json`.

---

## File Structure

Create:
- `core/schemas/search.go`: Core search request, params, result, response, and usage types.
- `core/providers/tavily/`: Tavily provider implementation, request/response types, converters, errors, tests.
- `core/providers/brave/`: Brave provider implementation, request/response types, converters, errors, tests.
- `core/providers/exa/`: Exa provider implementation, request/response types, converters, errors, tests.

Modify:
- `core/schemas/bifrost.go`: Add provider constants, `SearchRequest`, request/response union fields, helper switch cases, extra fields population.
- `core/schemas/provider.go`: Add `Search` method to `Provider`.
- `core/bifrost.go`: Add imports, provider constructors, public `SearchRequest`, fallback copy, provider dispatch, request reset.
- Existing provider files under `core/providers/*/*.go`: Add unsupported `Search` stubs.
- `core/providers/utils/utils.go`: Ensure unsupported operation helper covers `SearchRequest` through existing generic path.
- `transports/bifrost-http/handlers/inference.go`: Add handler request type, route mapping, parser, route, and handler method.
- `plugins/logging/main.go`, `plugins/logging/operations.go`, `plugins/logging/utils.go`, `plugins/logging/pool.go`: Capture search params/query/output/usage using existing flexible log fields where possible.
- `framework/modelcatalog/*`: Add search pricing extraction and config fields.
- `plugins/governance/*`, `plugins/telemetry/*`: Ensure search cost calculation and operation labels flow through existing `CalculateCost`.
- `transports/config.schema.json`: Add providers, allowed request field, and pricing fields.
- `ui/lib/constants/config.ts`, `ui/lib/constants/logs.ts`, `ui/lib/constants/icons.tsx`: Add minimal provider display/config constants.
- `docs/docs.json` and `docs/providers/supported-providers/*.mdx`: Add docs only after code paths are stable.

---

### Task 1: Core Search Schemas

**Files:**
- Create: `core/schemas/search.go`
- Modify: `core/schemas/bifrost.go`
- Test: `core/schemas/search_test.go`

- [ ] **Step 1: Write schema tests**

Create `core/schemas/search_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./core/schemas -run 'TestBifrost(Search|RequestSearch|ResponseSearch)' -count=1
```

Expected: FAIL because `BifrostSearchRequest`, `SearchRequest`, `Tavily`, `Brave`, and `Exa` are undefined.

- [ ] **Step 3: Add search schema types**

Create `core/schemas/search.go`:

```go
package schemas

type BifrostSearchRequest struct {
	Provider       ModelProvider            `json:"provider"`
	Model          string                   `json:"model"`
	Query          string                   `json:"query"`
	Params         *BifrostSearchParameters `json:"params,omitempty"`
	Fallbacks      []Fallback               `json:"fallbacks,omitempty"`
	RawRequestBody []byte                   `json:"-"`
}

func (r *BifrostSearchRequest) GetRawRequestBody() []byte {
	return r.RawRequestBody
}

type BifrostSearchParameters struct {
	MaxResults        *int                   `json:"max_results,omitempty"`
	Country           *string                `json:"country,omitempty"`
	Language          *string                `json:"language,omitempty"`
	IncludeAnswer     *bool                  `json:"include_answer,omitempty"`
	IncludeRawContent *bool                  `json:"include_raw_content,omitempty"`
	ExtraParams       map[string]interface{} `json:"-"`
}

type BifrostSearchResult struct {
	Title       string   `json:"title"`
	URL         string   `json:"url"`
	Snippet     string   `json:"snippet,omitempty"`
	Content     *string  `json:"content,omitempty"`
	PublishedAt *string  `json:"published_at,omitempty"`
	Score       *float64 `json:"score,omitempty"`
	Source      *string  `json:"source,omitempty"`
}

type BifrostSearchUsage struct {
	Queries int      `json:"queries,omitempty"`
	Results int      `json:"results,omitempty"`
	Credits *float64 `json:"credits,omitempty"`
}

type BifrostSearchResponse struct {
	ID          string                     `json:"id,omitempty"`
	Object      string                     `json:"object"`
	Query       string                     `json:"query"`
	Results     []BifrostSearchResult      `json:"results"`
	Answer      *string                    `json:"answer,omitempty"`
	Model       string                     `json:"model,omitempty"`
	Usage       *BifrostSearchUsage        `json:"usage,omitempty"`
	ExtraFields BifrostResponseExtraFields `json:"extra_fields"`
}
```

- [ ] **Step 4: Add provider constants and union fields**

Modify `core/schemas/bifrost.go`:

```go
const (
	// existing providers...
	Tavily ModelProvider = "tavily"
	Brave  ModelProvider = "brave"
	Exa    ModelProvider = "exa_ai"
)
```

Add the three providers to `StandardProviders`.

Add request type:

```go
SearchRequest RequestType = "search"
```

Add fields:

```go
SearchRequest *BifrostSearchRequest
SearchResponse *BifrostSearchResponse
```

Add `SearchRequest` cases to:

```go
func (br *BifrostRequest) GetRequestFields() (provider ModelProvider, model string, fallbacks []Fallback)
func (br *BifrostRequest) SetProvider(provider ModelProvider)
func (br *BifrostRequest) SetModel(model string)
func (br *BifrostRequest) SetFallbacks(fallbacks []Fallback)
func (br *BifrostRequest) SetRawRequestBody(rawRequestBody []byte)
func (r *BifrostResponse) GetExtraFields() *BifrostResponseExtraFields
func (r *BifrostResponse) PopulateExtraFields(requestType RequestType, provider ModelProvider, originalModelRequested string, resolvedModelUsed string)
```

Use this shape in each switch:

```go
case br.SearchRequest != nil:
	return br.SearchRequest.Provider, br.SearchRequest.Model, br.SearchRequest.Fallbacks
```

```go
case r.SearchResponse != nil:
	return &r.SearchResponse.ExtraFields
```

```go
case r.SearchResponse != nil:
	r.SearchResponse.ExtraFields.RequestType = requestType
	r.SearchResponse.ExtraFields.Provider = provider
	r.SearchResponse.ExtraFields.OriginalModelRequested = originalModelRequested
	r.SearchResponse.ExtraFields.ResolvedModelUsed = resolvedModel
```

- [ ] **Step 5: Run schema tests**

Run:

```bash
go test ./core/schemas -run 'TestBifrost(Search|RequestSearch|ResponseSearch)' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add core/schemas/search.go core/schemas/search_test.go core/schemas/bifrost.go
git commit -m "feat(search): add core search schemas"
```

---

### Task 2: Core Request Flow and Provider Interface

**Files:**
- Modify: `core/schemas/provider.go`
- Modify: `core/bifrost.go`
- Test: `core/bifrost_search_test.go`

- [ ] **Step 1: Write core validation and reset tests**

Create `core/bifrost_search_test.go`:

```go
package bifrost

import (
	"context"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

func TestSearchRequestValidation(t *testing.T) {
	client := &Bifrost{ctx: schemas.NewBifrostContext(context.Background(), 0)}

	_, err := client.SearchRequest(client.ctx, nil)
	if err == nil || err.ExtraFields.RequestType != schemas.SearchRequest {
		t.Fatalf("nil request error = %+v", err)
	}

	_, err = client.SearchRequest(client.ctx, &schemas.BifrostSearchRequest{
		Provider: schemas.Tavily,
		Model:    "default",
		Query:    "   ",
	})
	if err == nil || !strings.Contains(err.Error.Message, "query not provided") {
		t.Fatalf("empty query error = %+v", err)
	}
}

func TestResetBifrostRequestClearsSearch(t *testing.T) {
	req := &schemas.BifrostRequest{
		RequestType: schemas.SearchRequest,
		SearchRequest: &schemas.BifrostSearchRequest{
			Provider: schemas.Tavily,
			Model:    "default",
			Query:    "bifrost",
		},
	}

	resetBifrostRequest(req)

	if req.RequestType != "" {
		t.Fatalf("request type = %q", req.RequestType)
	}
	if req.SearchRequest != nil {
		t.Fatalf("search request was not cleared")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./core -run 'Test(SearchRequestValidation|ResetBifrostRequestClearsSearch)' -count=1
```

Expected: FAIL because `Bifrost.SearchRequest` and reset support are not implemented.

- [ ] **Step 3: Add provider interface method**

Modify `core/schemas/provider.go` near `Rerank` and `OCR`:

```go
// Search performs a web search request.
Search(ctx *BifrostContext, key Key, request *BifrostSearchRequest) (*BifrostSearchResponse, *BifrostError)
```

- [ ] **Step 4: Add public core method**

Modify `core/bifrost.go` near `RerankRequest`:

```go
// SearchRequest sends a search request to the specified provider.
func (bifrost *Bifrost) SearchRequest(ctx *schemas.BifrostContext, req *schemas.BifrostSearchRequest) (*schemas.BifrostSearchResponse, *schemas.BifrostError) {
	if req == nil {
		return nil, &schemas.BifrostError{
			IsBifrostError: false,
			Error: &schemas.ErrorField{
				Message: "search request is nil",
			},
			ExtraFields: schemas.BifrostErrorExtraFields{
				RequestType: schemas.SearchRequest,
			},
		}
	}
	if strings.TrimSpace(req.Query) == "" {
		return nil, &schemas.BifrostError{
			IsBifrostError: false,
			Error: &schemas.ErrorField{
				Message: "query not provided for search request",
			},
			ExtraFields: schemas.BifrostErrorExtraFields{
				RequestType:            schemas.SearchRequest,
				Provider:               req.Provider,
				OriginalModelRequested: req.Model,
				ResolvedModelUsed:      req.Model,
			},
		}
	}
	if req.Model == "" {
		req.Model = "default"
	}

	bifrostReq := bifrost.getBifrostRequest()
	bifrostReq.RequestType = schemas.SearchRequest
	bifrostReq.SearchRequest = req

	response, err := bifrost.handleRequest(ctx, bifrostReq)
	if err != nil {
		return nil, err
	}
	return response.SearchResponse, nil
}
```

- [ ] **Step 5: Wire internal request lifecycle**

Modify `core/bifrost.go`:

In fallback copy:

```go
if req.SearchRequest != nil {
	tmp := *req.SearchRequest
	if req.SearchRequest.Params != nil {
		paramsCopy := *req.SearchRequest.Params
		if req.SearchRequest.Params.ExtraParams != nil {
			paramsCopy.ExtraParams = maps.Clone(req.SearchRequest.Params.ExtraParams)
		}
		tmp.Params = &paramsCopy
	}
	fallbackReq.SearchRequest = &tmp
}
```

Add `maps` import if the file does not already import it.

In `handleProviderRequest`:

```go
case schemas.SearchRequest:
	searchResponse, bifrostError := provider.Search(req.Context, key, req.BifrostRequest.SearchRequest)
	if bifrostError != nil {
		return nil, bifrostError
	}
	response.SearchResponse = searchResponse
```

In `resetBifrostRequest`:

```go
req.SearchRequest = nil
```

- [ ] **Step 6: Run tests and capture compile errors for provider stubs**

Run:

```bash
go test ./core -run 'Test(SearchRequestValidation|ResetBifrostRequestClearsSearch)' -count=1
```

Expected: FAIL with compile errors listing providers that do not implement `Search`. Continue with Task 3.

- [ ] **Step 7: Commit after Task 3 completes**

Do not commit this task until provider stubs are added and `go test ./core` compiles.

---

### Task 3: Unsupported Search Stubs for Existing Providers

**Files:**
- Modify: every existing built-in provider implementation under `core/providers/*/*.go`
- Test: `go test ./core -run 'Test(SearchRequestValidation|ResetBifrostRequestClearsSearch)'`

- [ ] **Step 1: Add unsupported stubs**

For every provider except Tavily, Brave, and Exa, add this method near the existing `Rerank`/`OCR` methods, replacing receiver type and provider key with the local provider type:

```go
func (provider *OpenAIProvider) Search(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostSearchRequest) (*schemas.BifrostSearchResponse, *schemas.BifrostError) {
	return nil, providerUtils.NewUnsupportedOperationError(schemas.SearchRequest, provider.GetProviderKey())
}
```

Apply the same signature and body in these provider files using their existing receiver names:

- `core/providers/openai/openai.go`
- `core/providers/anthropic/anthropic.go`
- `core/providers/azure/azure.go`
- `core/providers/bedrock/bedrock.go`
- `core/providers/cerebras/cerebras.go`
- `core/providers/cohere/cohere.go`
- `core/providers/elevenlabs/elevenlabs.go`
- `core/providers/fireworks/fireworks.go`
- `core/providers/gemini/gemini.go`
- `core/providers/groq/groq.go`
- `core/providers/huggingface/huggingface.go`
- `core/providers/mistral/mistral.go`
- `core/providers/nebius/nebius.go`
- `core/providers/ollama/ollama.go`
- `core/providers/openrouter/openrouter.go`
- `core/providers/parasail/parasail.go`
- `core/providers/perplexity/perplexity.go`
- `core/providers/replicate/replicate.go`
- `core/providers/runway/runway.go`
- `core/providers/sgl/sgl.go`
- `core/providers/vertex/vertex.go`
- `core/providers/vllm/vllm.go`
- `core/providers/xai/xai.go`

- [ ] **Step 2: Run core tests**

Run:

```bash
go test ./core -run 'Test(SearchRequestValidation|ResetBifrostRequestClearsSearch)' -count=1
```

Expected: PASS.

- [ ] **Step 3: Commit Task 2 and Task 3 together**

```bash
git add core/bifrost.go core/schemas/provider.go core/bifrost_search_test.go core/providers
git commit -m "feat(search): wire core search request flow"
```

---

### Task 4: Provider Registration for Tavily, Brave, and Exa

**Files:**
- Create: `core/providers/tavily/tavily.go`
- Create: `core/providers/brave/brave.go`
- Create: `core/providers/exa/exa.go`
- Modify: `core/bifrost.go`
- Test: `core/bifrost_test.go`

- [ ] **Step 1: Write provider creation test**

Add to `core/bifrost_test.go`:

```go
func TestCreateBaseProviderSearchProviders(t *testing.T) {
	client := &Bifrost{logger: NewDefaultLogger(schemas.LogLevelError)}
	providers := []schemas.ModelProvider{schemas.Tavily, schemas.Brave, schemas.Exa}

	for _, providerKey := range providers {
		t.Run(string(providerKey), func(t *testing.T) {
			provider, err := client.createBaseProvider(providerKey, &schemas.ProviderConfig{})
			if err != nil {
				t.Fatalf("createBaseProvider(%q) error = %v", providerKey, err)
			}
			if provider.GetProviderKey() != providerKey {
				t.Fatalf("provider key = %q", provider.GetProviderKey())
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
go test ./core -run TestCreateBaseProviderSearchProviders -count=1
```

Expected: FAIL because provider constructors/imports do not exist.

- [ ] **Step 3: Add minimal provider constructors**

Create `core/providers/tavily/tavily.go`:

```go
package tavily

import (
	"strings"
	"time"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
)

type TavilyProvider struct {
	logger              schemas.Logger
	client              *fasthttp.Client
	networkConfig       schemas.NetworkConfig
	sendBackRawRequest  bool
	sendBackRawResponse bool
}

func NewTavilyProvider(config *schemas.ProviderConfig, logger schemas.Logger) *TavilyProvider {
	config.CheckAndSetDefaults()
	requestTimeout := time.Second * time.Duration(config.NetworkConfig.DefaultRequestTimeoutInSeconds)
	client := &fasthttp.Client{
		ReadTimeout:         requestTimeout,
		WriteTimeout:        requestTimeout,
		MaxConnsPerHost:     config.NetworkConfig.MaxConnsPerHost,
		MaxIdleConnDuration: 30 * time.Second,
		MaxConnWaitTimeout:  requestTimeout,
		MaxConnDuration:     time.Second * time.Duration(schemas.DefaultMaxConnDurationInSeconds),
		ConnPoolStrategy:    fasthttp.FIFO,
	}
	client = providerUtils.ConfigureProxy(client, config.ProxyConfig, logger)
	client = providerUtils.ConfigureDialer(client)
	client = providerUtils.ConfigureTLS(client, config.NetworkConfig, logger)
	if config.NetworkConfig.BaseURL == "" {
		config.NetworkConfig.BaseURL = "https://api.tavily.com"
	}
	config.NetworkConfig.BaseURL = strings.TrimRight(config.NetworkConfig.BaseURL, "/")
	return &TavilyProvider{
		logger:              logger,
		client:              client,
		networkConfig:       config.NetworkConfig,
		sendBackRawRequest:  config.SendBackRawRequest,
		sendBackRawResponse: config.SendBackRawResponse,
	}
}

func (provider *TavilyProvider) GetProviderKey() schemas.ModelProvider {
	return schemas.Tavily
}
```

Create equivalent constructors in `core/providers/brave/brave.go` and `core/providers/exa/exa.go`, using:

```go
type BraveProvider struct { /* same fields */ }
func NewBraveProvider(config *schemas.ProviderConfig, logger schemas.Logger) *BraveProvider
func (provider *BraveProvider) GetProviderKey() schemas.ModelProvider { return schemas.Brave }
```

Default Brave base URL:

```go
"https://api.search.brave.com"
```

Create:

```go
type ExaProvider struct { /* same fields */ }
func NewExaProvider(config *schemas.ProviderConfig, logger schemas.Logger) *ExaProvider
func (provider *ExaProvider) GetProviderKey() schemas.ModelProvider { return schemas.Exa }
```

Default Exa base URL:

```go
"https://api.exa.ai"
```

- [ ] **Step 4: Add unsupported methods to new providers**

In each new provider file, implement all `schemas.Provider` methods except `Search` using unsupported errors. Use existing provider files as the method list source. Each method body should be:

```go
return nil, providerUtils.NewUnsupportedOperationError(schemas.ChatCompletionRequest, provider.GetProviderKey())
```

Use the matching request type per method. For streaming methods, return `(nil, error)`.

- [ ] **Step 5: Wire constructors**

Modify `core/bifrost.go` imports:

```go
"github.com/maximhq/bifrost/core/providers/brave"
"github.com/maximhq/bifrost/core/providers/exa"
"github.com/maximhq/bifrost/core/providers/tavily"
```

Add to `createBaseProvider`:

```go
case schemas.Tavily:
	return tavily.NewTavilyProvider(config, bifrost.logger), nil
case schemas.Brave:
	return brave.NewBraveProvider(config, bifrost.logger), nil
case schemas.Exa:
	return exa.NewExaProvider(config, bifrost.logger), nil
```

- [ ] **Step 6: Run provider creation test**

Run:

```bash
go test ./core -run TestCreateBaseProviderSearchProviders -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add core/bifrost.go core/bifrost_test.go core/providers/tavily core/providers/brave core/providers/exa
git commit -m "feat(search): register search providers"
```

---

### Task 5: Tavily Search Implementation

**Status:** Completed
**Commit:** `09bf20612` `fix(search): align tavily search mapping`

**Files:**
- Create: `core/providers/tavily/types.go`
- Create: `core/providers/tavily/search.go`
- Create: `core/providers/tavily/errors.go`
- Create: `core/providers/tavily/search_test.go`
- Modify: `core/providers/tavily/tavily.go`

- [x] **Step 1: Write converter tests**

Create `core/providers/tavily/search_test.go`:

```go
package tavily

import (
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

func TestToTavilySearchRequest(t *testing.T) {
	maxResults := 7
	includeAnswer := true
	includeRaw := true
	req := ToTavilySearchRequest(&schemas.BifrostSearchRequest{
		Query: "bifrost gateway",
		Params: &schemas.BifrostSearchParameters{
			MaxResults:        &maxResults,
			IncludeAnswer:     &includeAnswer,
			IncludeRawContent: &includeRaw,
			ExtraParams: map[string]interface{}{
				"search_depth": "advanced",
			},
		},
	})

	if req.Query != "bifrost gateway" || req.MaxResults == nil || *req.MaxResults != 7 {
		t.Fatalf("request = %+v", req)
	}
	if req.SearchDepth == nil || *req.SearchDepth != "advanced" {
		t.Fatalf("search depth = %+v", req.SearchDepth)
	}
	if req.IncludeAnswer == nil || !*req.IncludeAnswer {
		t.Fatalf("include answer = %+v", req.IncludeAnswer)
	}
	if req.IncludeRawContent == nil || !*req.IncludeRawContent {
		t.Fatalf("include raw = %+v", req.IncludeRawContent)
	}
}

func TestTavilySearchResponseToBifrost(t *testing.T) {
	score := 0.98
	raw := &TavilySearchResponse{
		Query:  "bifrost gateway",
		Answer: "Bifrost is an AI gateway.",
		Results: []TavilyResult{{
			Title:      "Bifrost",
			URL:        "https://example.com",
			Content:    "snippet",
			RawContent: "full content",
			Score:      &score,
		}},
	}

	resp := raw.ToBifrostSearchResponse("default", true)
	if resp.Object != "search" || resp.Query != "bifrost gateway" {
		t.Fatalf("response = %+v", resp)
	}
	if resp.Answer == nil || *resp.Answer != "Bifrost is an AI gateway." {
		t.Fatalf("answer = %+v", resp.Answer)
	}
	if len(resp.Results) != 1 || resp.Results[0].URL != "https://example.com" {
		t.Fatalf("results = %+v", resp.Results)
	}
	if resp.Results[0].Content == nil || *resp.Results[0].Content != "full content" {
		t.Fatalf("content = %+v", resp.Results[0].Content)
	}
	if resp.Usage == nil || resp.Usage.Queries != 1 || resp.Usage.Results != 1 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}
```

- [x] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./core/providers/tavily -run 'Test(ToTavily|TavilySearchResponse)' -count=1
```

Expected: FAIL because converter types/functions do not exist.

- [x] **Step 3: Implement Tavily types and converters**

Create `core/providers/tavily/types.go`:

```go
package tavily

type TavilySearchRequest struct {
	Query             string                 `json:"query"`
	SearchDepth       *string                `json:"search_depth,omitempty"`
	MaxResults        *int                   `json:"max_results,omitempty"`
	IncludeAnswer     *bool                  `json:"include_answer,omitempty"`
	IncludeRawContent *bool                  `json:"include_raw_content,omitempty"`
	IncludeDomains    []string               `json:"include_domains,omitempty"`
	ExcludeDomains    []string               `json:"exclude_domains,omitempty"`
	ExtraParams       map[string]interface{} `json:"-"`
}

func (r *TavilySearchRequest) GetExtraParams() map[string]interface{} {
	return r.ExtraParams
}

type TavilySearchResponse struct {
	Query   string         `json:"query"`
	Answer string         `json:"answer,omitempty"`
	Results []TavilyResult `json:"results"`
}

type TavilyResult struct {
	Title      string   `json:"title"`
	URL        string   `json:"url"`
	Content    string   `json:"content,omitempty"`
	RawContent string   `json:"raw_content,omitempty"`
	Score      *float64 `json:"score,omitempty"`
}
```

Create `core/providers/tavily/search.go` with:

```go
package tavily

import (
	"fmt"
	"time"

	"github.com/bytedance/sonic"
	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
)

func ToTavilySearchRequest(req *schemas.BifrostSearchRequest) *TavilySearchRequest {
	out := &TavilySearchRequest{Query: req.Query}
	if req.Params == nil {
		return out
	}
	out.MaxResults = req.Params.MaxResults
	out.IncludeAnswer = req.Params.IncludeAnswer
	out.IncludeRawContent = req.Params.IncludeRawContent
	out.ExtraParams = req.Params.ExtraParams
	if depth, ok := req.Params.ExtraParams["search_depth"].(string); ok {
		out.SearchDepth = &depth
	}
	if includeDomains, ok := req.Params.ExtraParams["include_domains"].([]string); ok {
		out.IncludeDomains = includeDomains
	}
	if excludeDomains, ok := req.Params.ExtraParams["exclude_domains"].([]string); ok {
		out.ExcludeDomains = excludeDomains
	}
	return out
}

func (resp *TavilySearchResponse) ToBifrostSearchResponse(model string, includeRawContent bool) *schemas.BifrostSearchResponse {
	out := &schemas.BifrostSearchResponse{
		Object:  "search",
		Query:   resp.Query,
		Model:   model,
		Results: make([]schemas.BifrostSearchResult, 0, len(resp.Results)),
		Usage: &schemas.BifrostSearchUsage{
			Queries: 1,
			Results: len(resp.Results),
		},
	}
	if resp.Answer != "" {
		out.Answer = &resp.Answer
	}
	source := string(schemas.Tavily)
	for _, result := range resp.Results {
		item := schemas.BifrostSearchResult{
			Title:   result.Title,
			URL:     result.URL,
			Snippet: result.Content,
			Score:   result.Score,
			Source:  &source,
		}
		if includeRawContent && result.RawContent != "" {
			item.Content = &result.RawContent
		}
		out.Results = append(out.Results, item)
	}
	return out
}

func (provider *TavilyProvider) Search(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostSearchRequest) (*schemas.BifrostSearchResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.Tavily, nil, schemas.SearchRequest); err != nil {
		return nil, err
	}
	tavilyReq := ToTavilySearchRequest(request)
	body, err := sonic.Marshal(tavilyReq)
	if err != nil {
		return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderRequestMarshal, err)
	}

	fastReq := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(fastReq)
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)

	fastReq.SetRequestURI(provider.networkConfig.BaseURL + "/search")
	fastReq.Header.SetMethod(fasthttp.MethodPost)
	fastReq.Header.SetContentType("application/json")
	fastReq.Header.Set("Authorization", "Bearer "+key.Value.GetValue())
	fastReq.SetBody(body)

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, fastReq, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	if resp.StatusCode() < 200 || resp.StatusCode() >= 300 {
		return nil, TavilyErrorConverter(resp, schemas.SearchRequest, provider.GetProviderKey(), request.Model)
	}

	var tavilyResp TavilySearchResponse
	if err := sonic.Unmarshal(resp.Body(), &tavilyResp); err != nil {
		return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseUnmarshal, err)
	}

	includeRaw := request.Params != nil && request.Params.IncludeRawContent != nil && *request.Params.IncludeRawContent
	out := tavilyResp.ToBifrostSearchResponse(request.Model, includeRaw)
	out.ID = fmt.Sprintf("search_%d", time.Now().UnixNano())
	out.ExtraFields.Latency = latency.Milliseconds()
	if provider.sendBackRawRequest {
		out.ExtraFields.RawRequest = tavilyReq
	}
	if provider.sendBackRawResponse {
		out.ExtraFields.RawResponse = &tavilyResp
	}
	return out, nil
}
```

- [x] **Step 4: Add Tavily error converter**

Create `core/providers/tavily/errors.go`:

```go
package tavily

import (
	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
)

func TavilyErrorConverter(resp *fasthttp.Response, requestType schemas.RequestType, providerName schemas.ModelProvider, model string) *schemas.BifrostError {
	return providerUtils.HandleProviderAPIError(resp, &map[string]interface{}{})
}
```

- [x] **Step 5: Run Tavily tests**

Run:

```bash
go test ./core/providers/tavily -count=1
```

Expected: PASS.

- [x] **Step 6: Commit**

```bash
git add core/providers/tavily
git commit -m "feat(search): implement tavily provider"
```

Implemented behavior note:
- Final Tavily mapping uses `object = "search"`, supports structured `country`, and preserves extra Tavily-specific params.
- Validation passed with `go test ./core/providers/tavily -count=1` and `go test ./core/... -run '^$' -count=1`.

---

### Task 6: Brave Search Implementation

**Status:** Completed
**Commit:** `38dd0b8c0` `feat(search): implement brave provider`

**Files:**
- Create: `core/providers/brave/types.go`
- Create: `core/providers/brave/search.go`
- Create: `core/providers/brave/errors.go`
- Create: `core/providers/brave/search_test.go`

- [x] **Step 1: Write converter tests**

Create `core/providers/brave/search_test.go`:

```go
package brave

import (
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

func TestToBraveSearchQuery(t *testing.T) {
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
	if query.Get("q") != "bifrost gateway" || query.Get("count") != "5" || query.Get("country") != "US" || query.Get("search_lang") != "en" {
		t.Fatalf("query = %s", query.Encode())
	}
}

func TestBraveSearchResponseToBifrost(t *testing.T) {
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

	if resp.Object != "search" || resp.Query != "bifrost gateway" {
		t.Fatalf("response = %+v", resp)
	}
	if len(resp.Results) != 1 || resp.Results[0].Snippet != "snippet" {
		t.Fatalf("results = %+v", resp.Results)
	}
	if resp.Usage == nil || resp.Usage.Queries != 1 || resp.Usage.Results != 1 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}
```

- [x] **Step 2: Implement Brave types and converters**

Create `core/providers/brave/types.go`:

```go
package brave

type BraveSearchResponse struct {
	Web *BraveWebResults `json:"web,omitempty"`
}

type BraveWebResults struct {
	Results []BraveResult `json:"results"`
}

type BraveResult struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
	Age         string `json:"age,omitempty"`
}
```

Create `core/providers/brave/search.go`:

```go
package brave

import (
	"fmt"
	"time"
	"net/url"
	"strconv"

	"github.com/bytedance/sonic"
	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
)

func ToBraveSearchQuery(req *schemas.BifrostSearchRequest) url.Values {
	query := url.Values{}
	query.Set("q", req.Query)
	if req.Params == nil {
		return query
	}
	if req.Params.MaxResults != nil {
		query.Set("count", strconv.Itoa(*req.Params.MaxResults))
	}
	if req.Params.Country != nil {
		query.Set("country", *req.Params.Country)
	}
	if req.Params.Language != nil {
		query.Set("search_lang", *req.Params.Language)
	}
	return query
}

func (resp *BraveSearchResponse) ToBifrostSearchResponse(model string, query string) *schemas.BifrostSearchResponse {
	out := &schemas.BifrostSearchResponse{
		Object: "search",
		Query:  query,
		Model:  model,
		Usage: &schemas.BifrostSearchUsage{Queries: 1},
	}
	source := string(schemas.Brave)
	if resp.Web != nil {
		out.Results = make([]schemas.BifrostSearchResult, 0, len(resp.Web.Results))
		for _, result := range resp.Web.Results {
			item := schemas.BifrostSearchResult{
				Title:   result.Title,
				URL:     result.URL,
				Snippet: result.Description,
				Source:  &source,
			}
			if result.Age != "" {
				item.PublishedAt = &result.Age
			}
			out.Results = append(out.Results, item)
		}
		out.Usage.Results = len(resp.Web.Results)
	}
	return out
}

func (provider *BraveProvider) Search(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostSearchRequest) (*schemas.BifrostSearchResponse, *schemas.BifrostError) {
	query := ToBraveSearchQuery(request)
	fastReq := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(fastReq)
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)

	fastReq.SetRequestURI(provider.networkConfig.BaseURL + "/res/v1/web/search?" + query.Encode())
	fastReq.Header.SetMethod(fasthttp.MethodGet)
	fastReq.Header.Set("X-Subscription-Token", key.Value.GetValue())

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, fastReq, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	if resp.StatusCode() < 200 || resp.StatusCode() >= 300 {
		return nil, BraveErrorConverter(resp, schemas.SearchRequest, provider.GetProviderKey(), request.Model)
	}

	var braveResp BraveSearchResponse
	if err := sonic.Unmarshal(resp.Body(), &braveResp); err != nil {
		return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseUnmarshal, err)
	}
	out := braveResp.ToBifrostSearchResponse(request.Model, request.Query)
	out.ID = fmt.Sprintf("search_%d", time.Now().UnixNano())
	out.ExtraFields.Latency = latency.Milliseconds()
	if provider.sendBackRawRequest {
		out.ExtraFields.RawRequest = query
	}
	if provider.sendBackRawResponse {
		out.ExtraFields.RawResponse = &braveResp
	}
	return out, nil
}
```

Create `core/providers/brave/errors.go`:

```go
package brave

import (
	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
)

func BraveErrorConverter(resp *fasthttp.Response, requestType schemas.RequestType, providerName schemas.ModelProvider, model string) *schemas.BifrostError {
	return providerUtils.HandleProviderAPIError(resp, &map[string]interface{}{})
}
```

- [x] **Step 3: Run Brave tests**

Run:

```bash
go test ./core/providers/brave -count=1
```

Expected: PASS.

- [x] **Step 4: Commit**

```bash
git add core/providers/brave
git commit -m "feat(search): implement brave provider"
```

Implemented behavior note:
- Brave search uses `GET /res/v1/web/search`, maps `count/country/search_lang`, and prefers structured params over `ExtraParams`.
- Validation passed with `go test ./core/providers/brave -count=1` and `go test ./core/... -run '^$' -count=1`.

---

### Task 7: Exa Search Implementation

**Status:** Completed
**Commit:** `1370e642b` `feat(search): implement exa provider`

**Files:**
- Create: `core/providers/exa/types.go`
- Create: `core/providers/exa/search.go`
- Create: `core/providers/exa/errors.go`
- Create: `core/providers/exa/search_test.go`

- [x] **Step 1: Write converter tests**

Create `core/providers/exa/search_test.go`:

```go
package exa

import (
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

func TestToExaSearchRequest(t *testing.T) {
	maxResults := 3
	includeRaw := true
	req := ToExaSearchRequest(&schemas.BifrostSearchRequest{
		Query: "bifrost gateway",
		Params: &schemas.BifrostSearchParameters{
			MaxResults:        &maxResults,
			IncludeRawContent: &includeRaw,
			ExtraParams: map[string]interface{}{
				"type": "neural",
			},
		},
	})

	if req.Query != "bifrost gateway" || req.NumResults == nil || *req.NumResults != 3 {
		t.Fatalf("request = %+v", req)
	}
	if req.Type == nil || *req.Type != "neural" {
		t.Fatalf("type = %+v", req.Type)
	}
	if req.Contents == nil || req.Contents.Text == nil || !*req.Contents.Text {
		t.Fatalf("contents = %+v", req.Contents)
	}
}

func TestExaSearchResponseToBifrost(t *testing.T) {
	score := 0.77
	text := "full content"
	resp := (&ExaSearchResponse{
		Results: []ExaResult{{
			Title:     "Bifrost",
			URL:       "https://example.com",
			Text:      text,
			Score:     &score,
			Published: "2026-04-01",
		}},
	}).ToBifrostSearchResponse("default", "bifrost gateway")

	if resp.Object != "search" || resp.Query != "bifrost gateway" {
		t.Fatalf("response = %+v", resp)
	}
	if len(resp.Results) != 1 || resp.Results[0].Content == nil || *resp.Results[0].Content != text {
		t.Fatalf("results = %+v", resp.Results)
	}
	if resp.Usage == nil || resp.Usage.Results != 1 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}
```

- [x] **Step 2: Implement Exa types and converters**

Create `core/providers/exa/types.go`:

```go
package exa

type ExaSearchRequest struct {
	Query      string                 `json:"query"`
	Type       *string                `json:"type,omitempty"`
	NumResults *int                   `json:"numResults,omitempty"`
	Contents   *ExaContentsRequest    `json:"contents,omitempty"`
	ExtraParams map[string]interface{} `json:"-"`
}

func (r *ExaSearchRequest) GetExtraParams() map[string]interface{} {
	return r.ExtraParams
}

type ExaContentsRequest struct {
	Text *bool `json:"text,omitempty"`
}

type ExaSearchResponse struct {
	Results []ExaResult `json:"results"`
}

type ExaResult struct {
	Title     string   `json:"title"`
	URL       string   `json:"url"`
	Text      string   `json:"text,omitempty"`
	Score     *float64 `json:"score,omitempty"`
	Published string   `json:"publishedDate,omitempty"`
}
```

Create `core/providers/exa/search.go`:

```go
package exa

import (
	"fmt"
	"time"

	"github.com/bytedance/sonic"
	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
)

func ToExaSearchRequest(req *schemas.BifrostSearchRequest) *ExaSearchRequest {
	out := &ExaSearchRequest{Query: req.Query}
	if req.Params == nil {
		return out
	}
	out.NumResults = req.Params.MaxResults
	out.ExtraParams = req.Params.ExtraParams
	if searchType, ok := req.Params.ExtraParams["type"].(string); ok {
		out.Type = &searchType
	}
	if req.Params.IncludeRawContent != nil && *req.Params.IncludeRawContent {
		include := true
		out.Contents = &ExaContentsRequest{Text: &include}
	}
	return out
}

func (resp *ExaSearchResponse) ToBifrostSearchResponse(model string, query string) *schemas.BifrostSearchResponse {
	out := &schemas.BifrostSearchResponse{
		Object:  "search",
		Query:   query,
		Model:   model,
		Results: make([]schemas.BifrostSearchResult, 0, len(resp.Results)),
		Usage: &schemas.BifrostSearchUsage{
			Queries: 1,
			Results: len(resp.Results),
		},
	}
	source := string(schemas.Exa)
	for _, result := range resp.Results {
		item := schemas.BifrostSearchResult{
			Title:       result.Title,
			URL:         result.URL,
			Snippet:     result.Text,
			Score:       result.Score,
			Source:      &source,
		}
		if result.Text != "" {
			item.Content = &result.Text
		}
		if result.Published != "" {
			item.PublishedAt = &result.Published
		}
		out.Results = append(out.Results, item)
	}
	return out
}

func (provider *ExaProvider) Search(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostSearchRequest) (*schemas.BifrostSearchResponse, *schemas.BifrostError) {
	exaReq := ToExaSearchRequest(request)
	body, err := sonic.Marshal(exaReq)
	if err != nil {
		return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderRequestMarshal, err)
	}

	fastReq := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(fastReq)
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)

	fastReq.SetRequestURI(provider.networkConfig.BaseURL + "/search")
	fastReq.Header.SetMethod(fasthttp.MethodPost)
	fastReq.Header.SetContentType("application/json")
	fastReq.Header.Set("x-api-key", key.Value.GetValue())
	fastReq.SetBody(body)

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, fastReq, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	if resp.StatusCode() < 200 || resp.StatusCode() >= 300 {
		return nil, ExaErrorConverter(resp, schemas.SearchRequest, provider.GetProviderKey(), request.Model)
	}

	var exaResp ExaSearchResponse
	if err := sonic.Unmarshal(resp.Body(), &exaResp); err != nil {
		return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseUnmarshal, err)
	}
	out := exaResp.ToBifrostSearchResponse(request.Model, request.Query)
	out.ID = fmt.Sprintf("search_%d", time.Now().UnixNano())
	out.ExtraFields.Latency = latency.Milliseconds()
	if provider.sendBackRawRequest {
		out.ExtraFields.RawRequest = exaReq
	}
	if provider.sendBackRawResponse {
		out.ExtraFields.RawResponse = &exaResp
	}
	return out, nil
}
```

Create `core/providers/exa/errors.go`:

```go
package exa

import (
	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
)

func ExaErrorConverter(resp *fasthttp.Response, requestType schemas.RequestType, providerName schemas.ModelProvider, model string) *schemas.BifrostError {
	return providerUtils.HandleProviderAPIError(resp, &map[string]interface{}{})
}
```

- [x] **Step 3: Run Exa tests**

Run:

```bash
go test ./core/providers/exa -count=1
```

Expected: PASS.

- [x] **Step 4: Commit**

```bash
git add core/providers/exa
git commit -m "feat(search): implement exa provider"
```

Implemented behavior note:
- Exa search now supports `type`, `category`, `userLocation`, `numResults`, domain filters, `contents.text`, and request passthrough for remaining extra params.
- `IncludeRawContent` maps to `contents.text = true`; response mapping prefers highlights, then summary, then text.
- Structured params win over `ExtraParams` for `numResults` and `userLocation`; `stream` is rejected at the HTTP layer and `CostDollars.Total` maps to `usage.credits`.
- Validation passed with `go test ./core/providers/exa -count=1` and `go test ./core/... -run '^$' -count=1`.

---

### Task 8: HTTP `/v1/search` Transport

**Status:** Completed
**Commit:** `082f5a079` `feat(search): add http search endpoint`

**Files:**
- Modify: `transports/bifrost-http/handlers/inference.go`
- Test: `transports/bifrost-http/handlers/inference_search_test.go`

- [x] **Step 1: Write parser tests**

Create `transports/bifrost-http/handlers/inference_search_test.go`:

```go
package handlers

import (
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
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
```

- [x] **Step 2: Add handler request type and params map**

Modify `transports/bifrost-http/handlers/inference.go`:

```go
var searchParamsKnownFields = map[string]bool{
	"max_results":         true,
	"country":             true,
	"language":            true,
	"include_answer":      true,
	"include_raw_content": true,
}
```

Add handler request type:

```go
type SearchRequest struct {
	Query string `json:"query"`
	BifrostParams
	*schemas.BifrostSearchParameters
}
```

Add path mapping:

```go
"/v1/search": schemas.SearchRequest,
```

Add route:

```go
r.POST("/v1/search", lib.ChainMiddlewares(h.search, baseMiddlewares...))
```

- [x] **Step 3: Implement parser and handler**

Add parser near `prepareRerankRequest`:

```go
func prepareSearchRequest(ctx *fasthttp.RequestCtx) (*SearchRequest, *schemas.BifrostSearchRequest, error) {
	var req SearchRequest
	if err := schemas.Unmarshal(ctx.PostBody(), &req); err != nil {
		return nil, nil, fmt.Errorf("failed to parse search request: %v", err)
	}
	provider, modelName := schemas.ParseModelString(req.Model, "")
	if provider == "" {
		return nil, nil, fmt.Errorf("model should be in provider/model format")
	}
	if modelName == "" {
		modelName = "default"
	}
	fallbacks, err := parseFallbacks(req.Fallbacks)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse fallbacks: %v", err)
	}
	if strings.TrimSpace(req.Query) == "" {
		return nil, nil, fmt.Errorf("query is required for search")
	}
	if req.BifrostSearchParameters == nil {
		req.BifrostSearchParameters = &schemas.BifrostSearchParameters{}
	}
	if req.BifrostSearchParameters.MaxResults != nil && *req.BifrostSearchParameters.MaxResults < 1 {
		return nil, nil, fmt.Errorf("max_results must be at least 1")
	}
	extraParams, err := extractExtraParams(ctx.PostBody(), searchParamsKnownFields)
	if err == nil {
		if _, ok := extraParams["stream"]; ok {
			return nil, nil, fmt.Errorf("stream is not supported for search")
		}
		req.BifrostSearchParameters.ExtraParams = extraParams
	}
	bifrostReq := &schemas.BifrostSearchRequest{
		Provider:  schemas.ModelProvider(provider),
		Model:     modelName,
		Query:     req.Query,
		Params:    req.BifrostSearchParameters,
		Fallbacks: fallbacks,
	}
	return &req, bifrostReq, nil
}
```

Add handler:

```go
func (h *CompletionHandler) search(ctx *fasthttp.RequestCtx) {
	_, bifrostSearchReq, err := prepareSearchRequest(ctx)
	if err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, err.Error())
		return
	}
	bifrostCtx, cancel := lib.ConvertToBifrostContext(ctx, h.handlerStore.ShouldAllowDirectKeys(), h.config.GetHeaderMatcher(), h.config.GetMCPHeaderCombinedAllowlist())
	defer cancel()
	if bifrostCtx == nil {
		SendError(ctx, fasthttp.StatusBadRequest, "Failed to convert context")
		return
	}
	resp, bifrostErr := h.client.SearchRequest(bifrostCtx, bifrostSearchReq)
	if bifrostErr != nil {
		SendBifrostError(ctx, bifrostErr)
		return
	}
	SendJSON(ctx, fasthttp.StatusOK, resp)
}
```

- [x] **Step 4: Run transport parser tests**

Run:

```bash
go test ./transports/bifrost-http/handlers -run TestPrepareSearchRequest -count=1
```

Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add transports/bifrost-http/handlers/inference.go transports/bifrost-http/handlers/inference_search_test.go
git commit -m "feat(search): add http search endpoint"
```

Implemented behavior note:
- `/v1/search` now routes through the same Bifrost search pipeline as the other first-class operations.
- The HTTP body uses the existing `provider/model` fallback contract, with `search_depth` and other unknown fields preserved in `ExtraParams`.
- Validation passed with `go test ./transports/bifrost-http/handlers -run 'Test(PrepareSearchRequest|PrepareSearchRequestMissingQuery|SearchPathMapping)' -count=1` and `go test ./transports/bifrost-http/handlers -count=1`.
- `/v1/search` rejects `stream` in the request body so it cannot leak into provider payloads.

---

### Task 9: Governance, Logging, and Pricing

**Files:**
- Modify: `plugins/logging/main.go`
- Modify: `plugins/logging/operations.go`
- Modify: `plugins/logging/utils.go`
- Modify: `framework/configstore/tables/modelpricing.go`
- Modify: `framework/modelcatalog/pricing.go`
- Modify: `framework/modelcatalog/utils.go`
- Test: affected plugin/modelcatalog tests

- [x] **Step 1: Governance already in place**

`AllowedRequests.Search` and its coverage already exist in this branch, so no core schema change was needed.

- [x] **Step 2: Logging request params and content extraction**

- [x] `plugins/logging/main.go` now records `SearchRequest.Params`
- [x] `plugins/logging/utils.go` now adds the query to input history
- [x] `plugins/logging/operations.go` now maps the first search snippet to `OutputMessageParsed`

- [x] **Step 3: Add search pricing input**

Model catalog now understands `search` as a request mode, extracts `SearchResponse.Usage`, and computes search cost from `search_cost_per_request`, `search_cost_per_result`, and `search_cost_per_credit`. A dedicated migration now adds those 3 columns for existing databases.

- [x] **Step 4: Run targeted tests**

Run:

```bash
go test ./plugins/logging -run 'Test.*Search|TestAllowedRequestsSearch' -count=1
go test ./framework/modelcatalog -run 'Test.*Search|Test.*Cost' -count=1
```

Expected: PASS for added tests. Verified with focused package tests plus full package runs for both `plugins/logging` and `framework/modelcatalog`, plus a configstore migration test covering legacy `governance_model_pricing` schemas.

- [ ] **Step 5: Commit**

```bash
git add plugins/logging framework/configstore/tables framework/modelcatalog docs/superpowers/plans/2026-04-19-search-core-operation.md
git commit -m "feat(search): wire governance logging and pricing"
```

---

### Task 10: Config Schema, UI Constants, and Docs

**Files:**
- Modify: `transports/config.schema.json`
- Modify: `ui/lib/constants/config.ts`
- Modify: `ui/lib/constants/logs.ts`
- Modify: `ui/lib/constants/icons.tsx`
- Modify: `ui/lib/config/celFieldsRouting.ts`
- Modify: `ui/app/workspace/custom-pricing/overrides/pricingFieldSelector.tsx`
- Create: `docs/providers/supported-providers/tavily.mdx`
- Create: `docs/providers/supported-providers/brave.mdx`
- Create: `docs/providers/supported-providers/exa-ai.mdx`
- Modify: `docs/docs.json`

- [ ] **Step 1: Update JSON schema**

Update `transports/config.schema.json` provider enum/list locations to include:

```json
"tavily",
"brave",
"exa_ai"
```

Add allowed request field wherever `allowed_requests` is defined:

```json
"search": {
  "type": "boolean",
  "description": "Allow search requests"
}
```

Add pricing fields under pricing entry definitions:

```json
"search_cost_per_request": {
  "type": "number",
  "minimum": 0
},
"search_cost_per_result": {
  "type": "number",
  "minimum": 0
},
"search_cost_per_credit": {
  "type": "number",
  "minimum": 0
}
```

- [ ] **Step 2: Update UI provider constants**

Add provider display names and config metadata:

```ts
tavily: "Tavily",
brave: "Brave Search",
exa_ai: "Exa"
```

Use a neutral fallback icon if no existing branded icon pattern is available. Do not add a full Search UI page.

Update the routing and pricing builders to expose search-specific choices:
- add `search` to the CEL routing builder `request_type` values
- add a `search` pricing group so `search_cost_*` fields render under the Search category

- [ ] **Step 3: Add provider docs**

Each provider doc should include:

```mdx
---
title: Tavily
---

Tavily is supported for the `/v1/search` endpoint.

```json
{
  "model": "tavily/default",
  "query": "latest AI gateway benchmarks",
  "max_results": 10
}
```
```

Use provider-specific title/name in each file.

- [ ] **Step 4: Validate JSON schema**

Run:

```bash
npx ajv-cli validate -s transports/config.schema.json -d transports/config.schema.json
```

Expected: schema parses successfully. If `ajv-cli` is unavailable, run existing config schema tests:

```bash
go test ./transports/bifrost-http/lib -run TestConfig -count=1
```

- [ ] **Step 5: Commit**

```bash
git add transports/config.schema.json ui/lib/constants docs
git commit -m "docs(search): add search provider config and docs"
```

---

### Task 11: End-to-End Verification

**Files:**
- Modify: files changed by failed verification commands.
- Create: focused tests in the package that fails verification when the failure is caused by missing search coverage.

- [ ] **Step 1: Format Go code**

Run:

```bash
gofmt -w core/schemas/search.go core/schemas/search_test.go core/bifrost_search_test.go core/providers/tavily core/providers/brave core/providers/exa transports/bifrost-http/handlers/inference_search_test.go
```

Expected: command exits 0.

- [ ] **Step 2: Run focused Go tests**

Run:

```bash
go test ./core/schemas ./core/providers/tavily ./core/providers/brave ./core/providers/exa -count=1
```

Expected: PASS.

- [ ] **Step 3: Run core tests**

Run:

```bash
go test ./core -run 'Test(Search|CreateBaseProviderSearch|ResetBifrostRequestClearsSearch)' -count=1
```

Expected: PASS.

- [ ] **Step 4: Run transport tests**

Run:

```bash
go test ./transports/bifrost-http/handlers -run TestPrepareSearchRequest -count=1
```

Expected: PASS.

- [ ] **Step 5: Run broader affected module tests**

Run:

```bash
go test ./core/... ./framework/... ./plugins/logging/... ./plugins/telemetry/... ./transports/bifrost-http/... -count=1
```

Expected: PASS. If a package fails because it requires live provider credentials, record the exact package and error in the final implementation report, then run the unit tests for the packages changed in this plan.

- [ ] **Step 6: Manual HTTP smoke test with mock server**

Use `httptest` or a local mock provider endpoint in a Go test. Configure Tavily provider `network_config.base_url` to the mock server and assert that `POST /v1/search` returns normalized JSON with `object: "search"` and one result.

- [ ] **Step 7: Final commit if verification changes files**

```bash
git status --short
git add core framework plugins transports ui docs
git commit -m "test(search): verify search operation"
```

Expected: clean worktree after commit.

---

## Self-Review Notes

Spec coverage:
- Core operation: Tasks 1-4.
- Tavily/Brave/Exa provider implementations: Tasks 5-7.
- HTTP route: Task 8.
- Governance/logging/pricing: Task 9.
- Config/UI/docs: Task 10.
- Verification: Task 11.

Type consistency:
- Provider constant is `Exa` with wire value `"exa_ai"`.
- Request type is `schemas.SearchRequest` with wire value `"search"`.
- Public method is `Bifrost.SearchRequest`.
- Provider method is `Search(ctx *BifrostContext, key Key, request *BifrostSearchRequest)`.

Known implementation caution:
- Provider network calls in Tasks 5-7 must use `providerUtils.MakeRequestWithContext`, `providerUtils.HandleProviderResponse`, `providerUtils.HandleProviderAPIError`, and `providerUtils.NewBifrostOperationError` with the signatures shown in this plan.
