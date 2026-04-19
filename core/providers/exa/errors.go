// Package exa implements the Exa provider and its utility functions.
package exa

import (
	"strings"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	schemas "github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
)

// ExaErrorConverter converts an Exa HTTP error response into a BifrostError.
func ExaErrorConverter(resp *fasthttp.Response, requestType schemas.RequestType, providerName schemas.ModelProvider, model string) *schemas.BifrostError {
	var errorResp ExaErrorResponse
	bifrostErr := providerUtils.HandleProviderAPIError(resp, &errorResp)
	if bifrostErr == nil {
		return nil
	}

	if bifrostErr.Error == nil {
		bifrostErr.Error = &schemas.ErrorField{}
	}

	if strings.TrimSpace(errorResp.Error) != "" {
		bifrostErr.Error.Message = errorResp.Error
	}
	if strings.TrimSpace(errorResp.Message) != "" && strings.TrimSpace(bifrostErr.Error.Message) == "" {
		bifrostErr.Error.Message = errorResp.Message
	}
	if strings.TrimSpace(errorResp.Detail) != "" && strings.TrimSpace(bifrostErr.Error.Message) == "" {
		bifrostErr.Error.Message = errorResp.Detail
	}
	if strings.TrimSpace(bifrostErr.Error.Message) == "" {
		bifrostErr.Error.Message = "exa API error"
	}

	bifrostErr.ExtraFields.RequestType = requestType
	bifrostErr.ExtraFields.Provider = providerName
	bifrostErr.ExtraFields.OriginalModelRequested = model
	bifrostErr.ExtraFields.ResolvedModelUsed = model

	return bifrostErr
}
