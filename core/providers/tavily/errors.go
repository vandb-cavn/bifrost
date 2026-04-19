package tavily

import (
	"fmt"
	"strings"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	schemas "github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
)

// TavilyErrorConverter converts a Tavily HTTP error response to a BifrostError.
func TavilyErrorConverter(resp *fasthttp.Response, requestType schemas.RequestType, providerName schemas.ModelProvider, model string) *schemas.BifrostError {
	var errorResp TavilyErrorResponse
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

	if errorResp.Error != nil {
		if strings.TrimSpace(errorResp.Error.Message) != "" {
			bifrostErr.Error.Message = errorResp.Error.Message
		}
		if strings.TrimSpace(errorResp.Error.Code) != "" {
			code := errorResp.Error.Code
			bifrostErr.Error.Code = &code
		}
		if strings.TrimSpace(errorResp.Error.Error) != "" && strings.TrimSpace(bifrostErr.Error.Message) == "" {
			bifrostErr.Error.Message = errorResp.Error.Error
		}
		if strings.TrimSpace(errorResp.Error.Detail) != "" && strings.TrimSpace(bifrostErr.Error.Message) == "" {
			bifrostErr.Error.Message = errorResp.Error.Detail
		}
		if strings.TrimSpace(errorResp.Error.Type) != "" && bifrostErr.Error.Type == nil {
			errorType := errorResp.Error.Type
			bifrostErr.Error.Type = &errorType
		}
	}

	if errorResp.Detail != nil {
		if strings.TrimSpace(errorResp.Detail.Message) != "" {
			bifrostErr.Error.Message = errorResp.Detail.Message
		}
		if strings.TrimSpace(errorResp.Detail.Code) != "" {
			code := errorResp.Detail.Code
			bifrostErr.Error.Code = &code
		}
		if strings.TrimSpace(errorResp.Detail.Error) != "" && strings.TrimSpace(bifrostErr.Error.Message) == "" {
			bifrostErr.Error.Message = errorResp.Detail.Error
		}
		if strings.TrimSpace(errorResp.Detail.Detail) != "" && strings.TrimSpace(bifrostErr.Error.Message) == "" {
			bifrostErr.Error.Message = errorResp.Detail.Detail
		}
		if strings.TrimSpace(errorResp.Detail.Type) != "" && bifrostErr.Error.Type == nil {
			errorType := errorResp.Detail.Type
			bifrostErr.Error.Type = &errorType
		}
	}

	if strings.TrimSpace(bifrostErr.Error.Message) == "" {
		if bifrostErr.StatusCode != nil {
			bifrostErr.Error.Message = fmt.Sprintf("tavily API error (status %d)", *bifrostErr.StatusCode)
		} else {
			bifrostErr.Error.Message = "tavily API error"
		}
	}

	bifrostErr.ExtraFields.RequestType = requestType
	bifrostErr.ExtraFields.Provider = providerName
	bifrostErr.ExtraFields.OriginalModelRequested = model
	bifrostErr.ExtraFields.ResolvedModelUsed = model

	return bifrostErr
}
