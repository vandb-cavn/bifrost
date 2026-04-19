// Package exa implements the Exa provider and its utility functions.
package exa

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	schemas "github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
)

// ToExaSearchRequest converts a Bifrost search request into an Exa search request.
func ToExaSearchRequest(req *schemas.BifrostSearchRequest) *ExaSearchRequest {
	if req == nil {
		return nil
	}

	result := &ExaSearchRequest{
		Query: req.Query,
	}
	if req.Params == nil {
		return result
	}

	result.NumResults = req.Params.MaxResults
	if req.Params.Country != nil && strings.TrimSpace(*req.Params.Country) != "" {
		result.UserLocation = req.Params.Country
	}

	if extra := copyExtraParams(req.Params.ExtraParams); len(extra) > 0 {
		if v, ok := extractStringPtr(extra, "type"); ok {
			result.Type = v
			delete(extra, "type")
		}
		if v, ok := extractStringPtr(extra, "category"); ok {
			result.Category = v
			delete(extra, "category")
		}
		if v, ok := extractStringPtr(extra, "userLocation", "user_location"); ok {
			if result.UserLocation == nil {
				result.UserLocation = v
			}
			delete(extra, "userLocation")
			delete(extra, "user_location")
		}
		if v, ok := extractIntPtr(extra, "numResults", "num_results"); ok {
			if result.NumResults == nil {
				result.NumResults = v
			}
			delete(extra, "numResults")
			delete(extra, "num_results")
		}
		if v, ok := extractStringSlice(extra, "additionalQueries", "additional_queries"); ok {
			result.AdditionalQueries = v
			delete(extra, "additionalQueries")
			delete(extra, "additional_queries")
		}
		if v, ok := extractStringSlice(extra, "includeDomains", "include_domains"); ok {
			result.IncludeDomains = v
			delete(extra, "includeDomains")
			delete(extra, "include_domains")
		}
		if v, ok := extractStringSlice(extra, "excludeDomains", "exclude_domains"); ok {
			result.ExcludeDomains = v
			delete(extra, "excludeDomains")
			delete(extra, "exclude_domains")
		}
		if v, ok := extractStringPtr(extra, "startPublishedDate", "start_published_date"); ok {
			result.StartPublishedDate = v
			delete(extra, "startPublishedDate")
			delete(extra, "start_published_date")
		}
		if v, ok := extractStringPtr(extra, "endPublishedDate", "end_published_date"); ok {
			result.EndPublishedDate = v
			delete(extra, "endPublishedDate")
			delete(extra, "end_published_date")
		}
		if v, ok := extractStringPtr(extra, "systemPrompt", "system_prompt"); ok {
			result.SystemPrompt = v
			delete(extra, "systemPrompt")
			delete(extra, "system_prompt")
		}
		if _, ok := extractBoolPtr(extra, "stream"); ok {
			delete(extra, "stream")
		}
		if v, ok := extractMap(extra, "outputSchema", "output_schema"); ok {
			result.OutputSchema = v
			delete(extra, "outputSchema")
			delete(extra, "output_schema")
		}
		if v, ok := extractContents(extra, "contents"); ok {
			result.Contents = v
			delete(extra, "contents")
		}
		result.ExtraParams = extra
	}

	if req.Params.IncludeRawContent != nil && *req.Params.IncludeRawContent {
		if result.Contents == nil {
			result.Contents = &ExaContentsRequest{}
		}
		if result.Contents.Text == nil {
			includeText := true
			result.Contents.Text = &includeText
		}
	}

	return result
}

// ToBifrostSearchResponse converts an Exa search response to Bifrost format.
func (resp *ExaSearchResponse) ToBifrostSearchResponse(model string, query string, includeRawContent bool) *schemas.BifrostSearchResponse {
	if resp == nil {
		return nil
	}

	results := make([]schemas.BifrostSearchResult, 0, len(resp.Results))
	for _, result := range resp.Results {
		bifrostResult := schemas.BifrostSearchResult{
			Title:   result.Title,
			URL:     result.URL,
			Score:   result.Score,
			Snippet: exaSnippet(result),
		}
		if includeRawContent && result.Text != nil && strings.TrimSpace(*result.Text) != "" {
			bifrostResult.Content = result.Text
		}
		if result.PublishedDate != nil && strings.TrimSpace(*result.PublishedDate) != "" {
			bifrostResult.PublishedAt = result.PublishedDate
		}
		if host := hostFromURL(result.URL); host != "" {
			bifrostResult.Source = &host
		}
		results = append(results, bifrostResult)
	}

	out := &schemas.BifrostSearchResponse{
		Object:  "search",
		Query:   query,
		Results: results,
		Model:   model,
		Usage: &schemas.BifrostSearchUsage{
			Queries: 1,
			Results: len(results),
		},
	}
	if resp.CostDollars != nil && resp.CostDollars.Total != nil {
		out.Usage.Credits = resp.CostDollars.Total
	}
	return out
}

// Search executes an Exa search request.
func (provider *ExaProvider) Search(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostSearchRequest) (*schemas.BifrostSearchResponse, *schemas.BifrostError) {
	if request == nil {
		return nil, providerUtils.NewBifrostOperationError("search request is nil", nil)
	}

	jsonData, bifrostErr := providerUtils.CheckContextAndGetRequestBody(
		ctx,
		request,
		func() (providerUtils.RequestBodyWithExtraParams, error) {
			return ToExaSearchRequest(request), nil
		},
	)
	if bifrostErr != nil {
		return nil, bifrostErr
	}

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	req.SetRequestURI(provider.networkConfig.BaseURL + "/search")
	req.Header.SetMethod(http.MethodPost)
	req.Header.SetContentType("application/json")
	if key.Value.GetValue() != "" {
		req.Header.Set("x-api-key", key.Value.GetValue())
	}
	providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)
	req.SetBody(jsonData)

	latency, requestErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
	defer wait()
	if requestErr != nil {
		return nil, providerUtils.EnrichError(ctx, requestErr, jsonData, nil, provider.sendBackRawRequest, provider.sendBackRawResponse)
	}

	body := append([]byte(nil), resp.Body()...)
	if resp.StatusCode() < 200 || resp.StatusCode() >= 300 {
		return nil, providerUtils.EnrichError(
			ctx,
			ExaErrorConverter(resp, schemas.SearchRequest, provider.GetProviderKey(), request.Model),
			jsonData,
			body,
			provider.sendBackRawRequest,
			provider.sendBackRawResponse,
		)
	}

	var exaResp ExaSearchResponse
	rawRequest, rawResponse, parseErr := providerUtils.HandleProviderResponse(
		body,
		&exaResp,
		jsonData,
		providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest),
		providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse),
	)
	if parseErr != nil {
		return nil, providerUtils.EnrichError(ctx, parseErr, jsonData, body, provider.sendBackRawRequest, provider.sendBackRawResponse)
	}

	includeRawContent := false
	if request != nil && request.Params != nil && request.Params.IncludeRawContent != nil {
		includeRawContent = *request.Params.IncludeRawContent
	}
	bifrostResp := exaResp.ToBifrostSearchResponse(request.Model, request.Query, includeRawContent)
	if bifrostResp == nil {
		return nil, providerUtils.NewBifrostOperationError("failed to convert exa search response", nil)
	}

	bifrostResp.ID = fmt.Sprintf("search_%d", time.Now().UnixNano())
	bifrostResp.ExtraFields.Latency = latency.Milliseconds()
	bifrostResp.ExtraFields.ProviderResponseHeaders = providerUtils.ExtractProviderResponseHeaders(resp)
	if providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest) {
		bifrostResp.ExtraFields.RawRequest = rawRequest
	}
	if providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse) {
		bifrostResp.ExtraFields.RawResponse = rawResponse
	}

	return bifrostResp, nil
}

func copyExtraParams(extra map[string]interface{}) map[string]interface{} {
	if len(extra) == 0 {
		return make(map[string]interface{})
	}
	cp := make(map[string]interface{}, len(extra))
	for k, v := range extra {
		cp[k] = v
	}
	return cp
}

func extractStringPtr(extra map[string]interface{}, keys ...string) (*string, bool) {
	for _, key := range keys {
		if raw, ok := extra[key]; ok {
			if v, ok := schemas.SafeExtractStringPointer(raw); ok && v != nil {
				return v, true
			}
		}
	}
	return nil, false
}

func extractBoolPtr(extra map[string]interface{}, keys ...string) (*bool, bool) {
	for _, key := range keys {
		if raw, ok := extra[key]; ok {
			if v, ok := schemas.SafeExtractBoolPointer(raw); ok && v != nil {
				return v, true
			}
		}
	}
	return nil, false
}

func extractIntPtr(extra map[string]interface{}, keys ...string) (*int, bool) {
	for _, key := range keys {
		if raw, ok := extra[key]; ok {
			if v, ok := schemas.SafeExtractIntPointer(raw); ok && v != nil {
				return v, true
			}
		}
	}
	return nil, false
}

func extractStringSlice(extra map[string]interface{}, keys ...string) ([]string, bool) {
	for _, key := range keys {
		if raw, ok := extra[key]; ok {
			if v, ok := schemas.SafeExtractStringSlice(raw); ok {
				return v, true
			}
		}
	}
	return nil, false
}

func extractMap(extra map[string]interface{}, keys ...string) (map[string]interface{}, bool) {
	for _, key := range keys {
		if raw, ok := extra[key]; ok {
			if v, ok := raw.(map[string]interface{}); ok {
				return v, true
			}
		}
	}
	return nil, false
}

func extractContents(extra map[string]interface{}, key string) (*ExaContentsRequest, bool) {
	raw, ok := extra[key]
	if !ok {
		return nil, false
	}
	contentMap, ok := raw.(map[string]interface{})
	if !ok {
		return nil, false
	}

	result := &ExaContentsRequest{}
	if v, ok := schemas.SafeExtractBoolPointer(contentMap["text"]); ok {
		result.Text = v
	}
	if rawHighlights, ok := contentMap["highlights"].(map[string]interface{}); ok {
		highlights := &ExaHighlightsRequest{}
		if v, ok := schemas.SafeExtractStringPointer(rawHighlights["query"]); ok {
			highlights.Query = v
		}
		if v, ok := schemas.SafeExtractIntPointer(rawHighlights["numSentences"]); ok {
			highlights.NumSentences = v
		}
		if v, ok := schemas.SafeExtractIntPointer(rawHighlights["highlightsPerUrl"]); ok {
			highlights.HighlightsPerURL = v
		}
		if v, ok := schemas.SafeExtractIntPointer(rawHighlights["maxCharacters"]); ok {
			highlights.MaxCharacters = v
		}
		result.Highlights = highlights
	} else if highlightEnabled, ok := schemas.SafeExtractBoolPointer(contentMap["highlights"]); ok && highlightEnabled != nil && *highlightEnabled {
		result.Highlights = &ExaHighlightsRequest{}
	}
	if rawSummary, ok := contentMap["summary"].(map[string]interface{}); ok {
		summary := &ExaSummaryRequest{}
		if v, ok := schemas.SafeExtractStringPointer(rawSummary["query"]); ok {
			summary.Query = v
		}
		if schemaMap, ok := rawSummary["schema"].(map[string]interface{}); ok {
			summary.Schema = schemaMap
		}
		result.Summary = summary
	} else if summaryEnabled, ok := schemas.SafeExtractBoolPointer(contentMap["summary"]); ok && summaryEnabled != nil && *summaryEnabled {
		result.Summary = &ExaSummaryRequest{}
	}
	if rawContext, ok := contentMap["context"].(map[string]interface{}); ok {
		context := &ExaContextRequest{}
		if v, ok := schemas.SafeExtractIntPointer(rawContext["maxCharacters"]); ok {
			context.MaxCharacters = v
		}
		result.Context = context
	} else if contextEnabled, ok := schemas.SafeExtractBoolPointer(contentMap["context"]); ok && contextEnabled != nil && *contextEnabled {
		result.Context = &ExaContextRequest{}
	}

	return result, true
}

func exaSnippet(result ExaResult) string {
	for _, highlight := range result.Highlights {
		if strings.TrimSpace(highlight) != "" {
			return highlight
		}
	}
	if result.Summary != nil && strings.TrimSpace(*result.Summary) != "" {
		return *result.Summary
	}
	if result.Text != nil && strings.TrimSpace(*result.Text) != "" {
		return *result.Text
	}
	return ""
}

func hostFromURL(rawURL string) string {
	if strings.TrimSpace(rawURL) == "" {
		return ""
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return parsed.Host
}
