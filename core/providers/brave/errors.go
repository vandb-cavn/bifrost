// Package brave implements the Brave provider and its utility functions.
package brave

import (
	"strings"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	schemas "github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
)

// BraveErrorConverter converts a Brave HTTP error response into a BifrostError.
func BraveErrorConverter(resp *fasthttp.Response, requestType schemas.RequestType, providerName schemas.ModelProvider, model string) *schemas.BifrostError {
	var errorResp BraveErrorResponse
	bifrostErr := providerUtils.HandleProviderAPIError(resp, &errorResp)
	if bifrostErr == nil {
		return nil
	}

	if bifrostErr.Error == nil {
		bifrostErr.Error = &schemas.ErrorField{}
	}

	if strings.TrimSpace(errorResp.Message) != "" {
		bifrostErr.Error.Message = errorResp.Message
	}
	if strings.TrimSpace(errorResp.Code) != "" {
		code := errorResp.Code
		bifrostErr.Error.Code = &code
	}
	if strings.TrimSpace(errorResp.Type) != "" && bifrostErr.Error.Type == nil {
		errorType := errorResp.Type
		bifrostErr.Error.Type = &errorType
	}
	if strings.TrimSpace(errorResp.Detail) != "" && strings.TrimSpace(bifrostErr.Error.Message) == "" {
		bifrostErr.Error.Message = errorResp.Detail
	}

	if errorResp.Error != nil {
		if strings.TrimSpace(errorResp.Error.Message) != "" {
			bifrostErr.Error.Message = errorResp.Error.Message
		}
		if strings.TrimSpace(errorResp.Error.Code) != "" {
			code := errorResp.Error.Code
			bifrostErr.Error.Code = &code
		}
		if strings.TrimSpace(errorResp.Error.Type) != "" && bifrostErr.Error.Type == nil {
			errorType := errorResp.Error.Type
			bifrostErr.Error.Type = &errorType
		}
		if strings.TrimSpace(errorResp.Error.Detail) != "" && strings.TrimSpace(bifrostErr.Error.Message) == "" {
			bifrostErr.Error.Message = errorResp.Error.Detail
		}
	}

	if strings.TrimSpace(bifrostErr.Error.Message) == "" {
		if bifrostErr.StatusCode != nil {
			bifrostErr.Error.Message = "brave API error"
		} else {
			bifrostErr.Error.Message = "brave API error"
		}
	}

	bifrostErr.ExtraFields.RequestType = requestType
	bifrostErr.ExtraFields.Provider = providerName
	bifrostErr.ExtraFields.OriginalModelRequested = model
	bifrostErr.ExtraFields.ResolvedModelUsed = model

	return bifrostErr
}
