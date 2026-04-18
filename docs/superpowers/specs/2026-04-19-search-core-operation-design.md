# Search Core Operation Design

## Summary

Add `/v1/search` as a first-class Bifrost core operation. The operation follows the existing `Rerank` and `OCR` pattern so it goes through `handleRequest` and inherits provider queues, key selection, fallback routing, plugin hooks, governance, tracing, logging, and pricing.

The MVP supports Tavily, Brave, and Exa. Perplexity is intentionally out of scope because it behaves more like an LLM-backed answer engine than a pure search provider.

## Goals

- Add a normalized search API at `POST /v1/search`.
- Implement core request/response schemas for search.
- Add `Search` to the provider interface and wire dispatch through the existing provider queue.
- Support best-effort fallback across Tavily, Brave, and Exa for portable request fields.
- Add logging, pricing, governance, and config schema support.
- Keep provider-specific details out of the normalized response unless raw responses are enabled.

## Non-Goals

- No streaming search endpoint in the MVP.
- No Perplexity search support in the MVP.
- No web-search augmentation plugin for chat/responses in the MVP.
- No strict parameter enforcement in the MVP.
- No full rollout to every search provider listed in the long-term provider set.

## Branch

Implementation work should happen on:

```bash
feat/search-core-operation
```

The branch starts from the current clean workspace. The design spec is committed before implementation work starts.

## API

The primary route is:

```http
POST /v1/search
```

Example request:

```json
{
  "provider": "tavily",
  "model": "default",
  "query": "latest AI gateway benchmarks",
  "params": {
    "max_results": 10,
    "country": "US",
    "language": "en",
    "include_answer": true,
    "include_raw_content": false,
    "extra_params": {}
  },
  "fallbacks": [
    { "provider": "brave", "model": "default" }
  ]
}
```

Example response:

```json
{
  "id": "search_...",
  "object": "search",
  "query": "latest AI gateway benchmarks",
  "results": [
    {
      "title": "...",
      "url": "...",
      "snippet": "...",
      "content": "...",
      "published_at": "...",
      "score": 0.92,
      "source": "tavily"
    }
  ],
  "answer": "...",
  "usage": {
    "queries": 1,
    "results": 10,
    "credits": 1
  },
  "extra_fields": {}
}
```

Raw provider responses are stored in `extra_fields.raw_response` when the existing provider config requests raw responses.

## Schemas

Add the following core schema types:

- `BifrostSearchRequest`
- `BifrostSearchParameters`
- `BifrostSearchResponse`
- `BifrostSearchResult`
- `BifrostSearchUsage`

The request contains:

- `Provider schemas.ModelProvider`
- `Model string`, optional for search providers; default to `"default"` when empty so existing provider/model metadata paths remain populated
- `Query string`
- `Params *BifrostSearchParameters`
- `Fallbacks []schemas.Fallback`
- `RawRequestBody []byte`

The normalized result contains:

- `Title string`
- `URL string`
- `Snippet string`
- `Content *string`
- `PublishedAt *string`
- `Score *float64`
- `Source *string`

The usage type is separate from token usage:

- `Queries int`
- `Results int`
- `Credits *float64`

## Core Wiring

Add `SearchRequest` to `schemas.RequestType`.

Add `SearchRequest *BifrostSearchRequest` to `schemas.BifrostRequest`.

Add `SearchResponse *BifrostSearchResponse` to `schemas.BifrostResponse`.

Update request helpers on `BifrostRequest`:

- get provider/model/fallback fields
- set provider
- set model
- set fallbacks
- set raw request body

Update response helpers:

- `GetExtraFields`
- `PopulateExtraFields`

Update core request flow:

- add public `Bifrost.SearchRequest(ctx, req)`
- validate non-nil request
- validate non-empty query
- call `handleRequest`
- return `response.SearchResponse`

Update internal request lifecycle:

- fallback request copy
- `handleProviderRequest` dispatch
- `resetBifrostRequest`

## Provider Interface

Add this method to `schemas.Provider`:

```go
Search(ctx *BifrostContext, key Key, request *BifrostSearchRequest) (*BifrostSearchResponse, *BifrostError)
```

Tavily, Brave, and Exa implement the method. All other providers return `providerUtils.NewUnsupportedOperationError(schemas.SearchRequest, provider.GetProviderKey())`.

This intentionally follows the existing monolithic provider interface pattern. A separate capability interface would reduce stub churn, but it would introduce a second operation model that does not match the current repository design.

## Providers

MVP providers:

- `tavily`
- `brave`
- `exa_ai`

Each provider should have:

- provider constructor and config integration
- provider-specific request/response structs
- pure converter functions
- error conversion using existing provider utility patterns
- unit tests for request conversion and response normalization

Longer-term providers:

- `parallel_ai`
- `google_pse`
- `dataforseo`
- `firecrawl`
- `searxng`
- `linkup`
- `duckduckgo`
- `searchapi`
- `serper`

## Fallback Semantics

Fallback is best-effort by default.

Portable fields are preserved where possible:

- `query`
- `max_results`
- `country`
- `language`
- `include_answer`
- `include_raw_content`

Provider-specific values live under `params.extra_params`. Provider converters consume only whitelisted keys they support. Unsupported provider-specific params are ignored during fallback instead of failing the request.

Strict param enforcement is out of scope for the MVP. It can be added later as `params.strict_params` if needed.

## Governance

Add `Search bool` to `AllowedRequests` and wire it through `IsOperationAllowed`.

This allows existing governance and provider configuration paths to allow or deny search using the same operation gating model as `Rerank` and `OCR`.

## Logging

Update the logging plugin to understand search requests and responses:

- capture request params from `BifrostSearchRequest.Params`
- include query in extracted request content where appropriate
- extract usage from `BifrostSearchResponse.Usage`
- persist cost when pricing calculates one

Log storage schema changes should be avoided unless existing flexible fields cannot represent the search request and response. The MVP should reuse existing request/response JSON fields.

## Pricing

Add search cost support to the model catalog pricing path.

Initial cost modes:

- cost per request
- cost per result
- cost per credit

The pricing implementation should use `BifrostSearchUsage` instead of `BifrostLLMUsage`.

The pricing schema and config schema should add explicit search pricing fields for per-request, per-result, and per-credit costs. Do not overload token pricing fields for search.

## HTTP Transport

Add `/v1/search` to the inference handler:

- request type path mapping
- route registration
- request parsing and validation
- response serialization

The MVP should not add `/search` as a bare alias. Keeping `/v1/search` first matches the current public inference API style.

## Config and UI Surface

Update `transports/config.schema.json` for:

- new providers
- provider config support
- `AllowedRequests.Search`
- search pricing fields

Update UI constants only where required for provider configuration and display names. Do not add a full Search UI page in the MVP.

## Tests

Minimum targeted coverage:

- schema validation for missing query
- `BifrostRequest` helper behavior for search
- fallback copy includes search requests
- `AllowedRequests.Search` gating
- HTTP `/v1/search` request parsing
- provider converter tests for Tavily, Brave, and Exa
- unsupported operation stubs compile for all existing providers
- logging extracts search params and usage
- pricing calculates cost from search usage

Verification should start with targeted Go tests in the affected modules, then broaden to module-level `go test ./...` where feasible.

## Risks

The main risk is missing an integration point because Bifrost uses explicit switch cases for request and response handling. The implementation plan should include a checklist of every `RequestType`, `BifrostRequest`, `BifrostResponse`, `AllowedRequests`, logging, pricing, and HTTP mapping location touched by `Rerank` or `OCR`.

Provider fallback is intentionally best-effort. Documentation must avoid promising semantic equivalence across providers for provider-specific params.

Provider-specific cost reporting may be inconsistent. The normalized usage model should support request/result/credit costs without forcing search into token usage.

## Open Decisions

No open decisions remain for the MVP design.
