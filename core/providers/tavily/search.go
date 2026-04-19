package tavily

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

// ToTavilySearchRequest converts a Bifrost search request into a Tavily search request.
func ToTavilySearchRequest(req *schemas.BifrostSearchRequest) *TavilySearchRequest {
	if req == nil {
		return nil
	}

	result := &TavilySearchRequest{
		Query: req.Query,
	}

	if req.Params == nil {
		return result
	}

	result.MaxResults = req.Params.MaxResults
	result.IncludeAnswer = req.Params.IncludeAnswer
	result.IncludeRawContent = req.Params.IncludeRawContent
	result.Country = req.Params.Country

	extra := copyExtraParams(req.Params.ExtraParams)
	if len(extra) == 0 {
		result.ExtraParams = extra
		return result
	}

	if v, ok := extractStringPtr(extra, "search_depth", "searchDepth"); ok {
		result.SearchDepth = v
		delete(extra, "search_depth")
		delete(extra, "searchDepth")
	}
	if v, ok := extractStringSlice(extra, "include_domains", "includeDomains"); ok {
		result.IncludeDomains = v
		delete(extra, "include_domains")
		delete(extra, "includeDomains")
	}
	if v, ok := extractStringSlice(extra, "exclude_domains", "excludeDomains"); ok {
		result.ExcludeDomains = v
		delete(extra, "exclude_domains")
		delete(extra, "excludeDomains")
	}
	if v, ok := extractIntPtr(extra, "chunks_per_source", "chunksPerSource"); ok {
		result.ChunksPerSource = v
		delete(extra, "chunks_per_source")
		delete(extra, "chunksPerSource")
	}
	if v, ok := extractStringPtr(extra, "topic"); ok {
		result.Topic = v
		delete(extra, "topic")
	}
	if v, ok := extractStringPtr(extra, "time_range", "timeRange"); ok {
		result.TimeRange = v
		delete(extra, "time_range")
		delete(extra, "timeRange")
	}
	if v, ok := extractStringPtr(extra, "start_date", "startDate"); ok {
		result.StartDate = v
		delete(extra, "start_date")
		delete(extra, "startDate")
	}
	if v, ok := extractStringPtr(extra, "end_date", "endDate"); ok {
		result.EndDate = v
		delete(extra, "end_date")
		delete(extra, "endDate")
	}
	if v, ok := extractBoolPtr(extra, "include_images", "includeImages"); ok {
		result.IncludeImages = v
		delete(extra, "include_images")
		delete(extra, "includeImages")
	}
	if v, ok := extractBoolPtr(extra, "include_image_descriptions", "includeImageDescriptions"); ok {
		result.IncludeImageDescriptions = v
		delete(extra, "include_image_descriptions")
		delete(extra, "includeImageDescriptions")
	}
	if v, ok := extractBoolPtr(extra, "include_favicon", "includeFavicon"); ok {
		result.IncludeFavicon = v
		delete(extra, "include_favicon")
		delete(extra, "includeFavicon")
	}
	if v, ok := extractBoolPtr(extra, "auto_parameters", "autoParameters"); ok {
		result.AutoParameters = v
		delete(extra, "auto_parameters")
		delete(extra, "autoParameters")
	}
	if v, ok := extractBoolPtr(extra, "exact_match", "exactMatch"); ok {
		result.ExactMatch = v
		delete(extra, "exact_match")
		delete(extra, "exactMatch")
	}
	if v, ok := extractBoolPtr(extra, "include_usage", "includeUsage"); ok {
		result.IncludeUsage = v
		delete(extra, "include_usage")
		delete(extra, "includeUsage")
	}
	if v, ok := extractStringPtr(extra, "country"); ok {
		if result.Country == nil {
			result.Country = v
		}
		delete(extra, "country")
	}
	if v, ok := extractIntPtr(extra, "days"); ok {
		result.Days = v
		delete(extra, "days")
	}

	result.ExtraParams = extra
	return result
}

// ToBifrostSearchResponse converts a Tavily search response to Bifrost format.
func (resp *TavilySearchResponse) ToBifrostSearchResponse(model string, includeRawContent bool) *schemas.BifrostSearchResponse {
	if resp == nil {
		return nil
	}

	results := make([]schemas.BifrostSearchResult, 0, len(resp.Results))
	for _, result := range resp.Results {
		bifrostResult := schemas.BifrostSearchResult{
			Title:   result.Title,
			URL:     result.URL,
			Snippet: result.Content,
			Score:   result.Score,
		}

		if includeRawContent && result.RawContent != nil && strings.TrimSpace(*result.RawContent) != "" {
			bifrostResult.Content = result.RawContent
		}
		if result.PublishedDate != nil && strings.TrimSpace(*result.PublishedDate) != "" {
			bifrostResult.PublishedAt = result.PublishedDate
		}
		if host := hostFromURL(result.URL); host != "" {
			bifrostResult.Source = &host
		}
		results = append(results, bifrostResult)
	}

	response := &schemas.BifrostSearchResponse{
		Object:  "search",
		Query:   resp.Query,
		Results: results,
		Model:   model,
	}
	if strings.TrimSpace(resp.Answer) != "" {
		response.Answer = &resp.Answer
	}

	usage := &schemas.BifrostSearchUsage{
		Queries: 1,
		Results: len(results),
	}
	if resp.Usage != nil && resp.Usage.Credits != nil {
		usage.Credits = resp.Usage.Credits
	}
	response.Usage = usage
	return response
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
			if str, ok := schemas.SafeExtractString(raw); ok {
				if strings.TrimSpace(str) == "" {
					return []string{}, true
				}
				items := strings.Split(str, ",")
				values := make([]string, 0, len(items))
				for _, item := range items {
					if trimmed := strings.TrimSpace(item); trimmed != "" {
						values = append(values, trimmed)
					}
				}
				return values, true
			}
		}
	}
	return nil, false
}

func hostFromURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return parsed.Host
}

// Search executes a Tavily search request.
func (provider *TavilyProvider) Search(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostSearchRequest) (*schemas.BifrostSearchResponse, *schemas.BifrostError) {
	jsonData, bifrostErr := providerUtils.CheckContextAndGetRequestBody(
		ctx,
		request,
		func() (providerUtils.RequestBodyWithExtraParams, error) {
			return ToTavilySearchRequest(request), nil
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
		req.Header.Set("Authorization", "Bearer "+key.Value.GetValue())
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
			TavilyErrorConverter(resp, schemas.SearchRequest, provider.GetProviderKey(), request.Model),
			jsonData,
			body,
			provider.sendBackRawRequest,
			provider.sendBackRawResponse,
		)
	}

	var tavilyResp TavilySearchResponse
	rawRequest, rawResponse, parseErr := providerUtils.HandleProviderResponse(
		body,
		&tavilyResp,
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
	bifrostResp := tavilyResp.ToBifrostSearchResponse(request.Model, includeRawContent)
	if bifrostResp == nil {
		return nil, providerUtils.NewBifrostOperationError("failed to convert tavily search response", nil)
	}
	if strings.TrimSpace(bifrostResp.Query) == "" && request != nil {
		bifrostResp.Query = request.Query
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
