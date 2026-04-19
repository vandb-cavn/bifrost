// Package brave implements the Brave provider and its utility functions.
package brave

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	schemas "github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
)

// ToBraveSearchQuery converts a Bifrost search request into Brave query parameters.
func ToBraveSearchQuery(req *schemas.BifrostSearchRequest) url.Values {
	query := url.Values{}
	if req == nil {
		return query
	}

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

	if req.Params.ExtraParams != nil {
		if req.Params.MaxResults == nil {
			if count, ok := schemas.SafeExtractIntPointer(req.Params.ExtraParams["count"]); ok {
				query.Set("count", strconv.Itoa(*count))
			}
		}
		if req.Params.Country == nil {
			if country, ok := schemas.SafeExtractStringPointer(req.Params.ExtraParams["country"]); ok {
				query.Set("country", *country)
			}
		}
		if req.Params.Language == nil {
			if language, ok := schemas.SafeExtractStringPointer(req.Params.ExtraParams["search_lang"]); ok {
				query.Set("search_lang", *language)
			}
			if language, ok := schemas.SafeExtractStringPointer(req.Params.ExtraParams["language"]); ok {
				query.Set("search_lang", *language)
			}
		}
	}

	return query
}

// ToBifrostSearchResponse converts a Brave search response to Bifrost format.
func (resp *BraveSearchResponse) ToBifrostSearchResponse(model string, query string) *schemas.BifrostSearchResponse {
	if resp == nil {
		return nil
	}

	out := &schemas.BifrostSearchResponse{
		Object: "search",
		Query:  query,
		Model:  model,
		Usage: &schemas.BifrostSearchUsage{
			Queries: 1,
		},
	}

	if resp.Web == nil || len(resp.Web.Results) == 0 {
		out.Results = []schemas.BifrostSearchResult{}
		return out
	}

	source := string(schemas.Brave)
	out.Results = make([]schemas.BifrostSearchResult, 0, len(resp.Web.Results))
	for _, result := range resp.Web.Results {
		item := schemas.BifrostSearchResult{
			Title:   result.Title,
			URL:     result.URL,
			Snippet: result.Description,
			Source:  &source,
		}
		if strings.TrimSpace(result.Age) != "" {
			item.PublishedAt = &result.Age
		}
		out.Results = append(out.Results, item)
	}
	out.Usage.Results = len(out.Results)
	return out
}

// Search executes a Brave search request.
func (provider *BraveProvider) Search(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostSearchRequest) (*schemas.BifrostSearchResponse, *schemas.BifrostError) {
	if request == nil {
		return nil, providerUtils.NewBifrostOperationError("search request is nil", nil)
	}

	query := ToBraveSearchQuery(request)
	uri := provider.networkConfig.BaseURL + "/res/v1/web/search"
	if encoded := query.Encode(); encoded != "" {
		uri += "?" + encoded
	}

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	req.SetRequestURI(uri)
	req.Header.SetMethod(http.MethodGet)
	if key.Value.GetValue() != "" {
		req.Header.Set("X-Subscription-Token", key.Value.GetValue())
	}
	providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)

	latency, requestErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
	defer wait()
	if requestErr != nil {
		bifrostErr := providerUtils.EnrichError(ctx, requestErr, nil, nil, provider.sendBackRawRequest, provider.sendBackRawResponse)
		if providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest) {
			bifrostErr.ExtraFields.RawRequest = uri
		}
		return nil, bifrostErr
	}

	body := append([]byte(nil), resp.Body()...)
	shouldSendBackRawRequest := providerUtils.ShouldSendBackRawRequest(ctx, provider.sendBackRawRequest)
	shouldSendBackRawResponse := providerUtils.ShouldSendBackRawResponse(ctx, provider.sendBackRawResponse)
	if resp.StatusCode() < 200 || resp.StatusCode() >= 300 {
		bifrostErr := BraveErrorConverter(resp, schemas.SearchRequest, provider.GetProviderKey(), request.Model)
		bifrostErr = providerUtils.EnrichError(ctx, bifrostErr, nil, body, provider.sendBackRawRequest, provider.sendBackRawResponse)
		if shouldSendBackRawRequest {
			bifrostErr.ExtraFields.RawRequest = uri
		}
		return nil, bifrostErr
	}

	var braveResp BraveSearchResponse
	_, rawResponse, parseErr := providerUtils.HandleProviderResponse(
		body,
		&braveResp,
		nil,
		shouldSendBackRawRequest,
		shouldSendBackRawResponse,
	)
	if parseErr != nil {
		bifrostErr := providerUtils.EnrichError(ctx, parseErr, nil, body, provider.sendBackRawRequest, provider.sendBackRawResponse)
		if shouldSendBackRawRequest {
			bifrostErr.ExtraFields.RawRequest = uri
		}
		return nil, bifrostErr
	}

	bifrostResp := braveResp.ToBifrostSearchResponse(request.Model, request.Query)
	if bifrostResp == nil {
		return nil, providerUtils.NewBifrostOperationError("failed to convert brave search response", nil)
	}

	bifrostResp.ID = fmt.Sprintf("search_%d", time.Now().UnixNano())
	bifrostResp.ExtraFields.Latency = latency.Milliseconds()
	bifrostResp.ExtraFields.ProviderResponseHeaders = providerUtils.ExtractProviderResponseHeaders(resp)
	if shouldSendBackRawRequest {
		bifrostResp.ExtraFields.RawRequest = uri
	}
	if shouldSendBackRawResponse {
		bifrostResp.ExtraFields.RawResponse = rawResponse
	}

	return bifrostResp, nil
}
