// Package tavily implements the Tavily provider and its utility functions.
package tavily

import (
	"strings"
	"time"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	schemas "github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
)

// TavilyProvider implements the Provider interface for Tavily's API.
type TavilyProvider struct {
	logger              schemas.Logger
	client              *fasthttp.Client
	networkConfig       schemas.NetworkConfig
	sendBackRawRequest  bool
	sendBackRawResponse bool
}

// NewTavilyProvider creates a new Tavily provider instance.
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

// GetProviderKey returns the provider identifier for Tavily.
func (provider *TavilyProvider) GetProviderKey() schemas.ModelProvider {
	return schemas.Tavily
}

func (provider *TavilyProvider) unsupported(requestType schemas.RequestType) *schemas.BifrostError {
	return providerUtils.NewUnsupportedOperationError(requestType, provider.GetProviderKey())
}

func (provider *TavilyProvider) ListModels(_ *schemas.BifrostContext, _ []schemas.Key, _ *schemas.BifrostListModelsRequest) (*schemas.BifrostListModelsResponse, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.ListModelsRequest)
}

func (provider *TavilyProvider) TextCompletion(_ *schemas.BifrostContext, _ schemas.Key, _ *schemas.BifrostTextCompletionRequest) (*schemas.BifrostTextCompletionResponse, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.TextCompletionRequest)
}

func (provider *TavilyProvider) TextCompletionStream(_ *schemas.BifrostContext, _ schemas.PostHookRunner, _ schemas.Key, _ *schemas.BifrostTextCompletionRequest) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.TextCompletionStreamRequest)
}

func (provider *TavilyProvider) ChatCompletion(_ *schemas.BifrostContext, _ schemas.Key, _ *schemas.BifrostChatRequest) (*schemas.BifrostChatResponse, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.ChatCompletionRequest)
}

func (provider *TavilyProvider) ChatCompletionStream(_ *schemas.BifrostContext, _ schemas.PostHookRunner, _ schemas.Key, _ *schemas.BifrostChatRequest) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.ChatCompletionStreamRequest)
}

func (provider *TavilyProvider) Responses(_ *schemas.BifrostContext, _ schemas.Key, _ *schemas.BifrostResponsesRequest) (*schemas.BifrostResponsesResponse, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.ResponsesRequest)
}

func (provider *TavilyProvider) ResponsesStream(_ *schemas.BifrostContext, _ schemas.PostHookRunner, _ schemas.Key, _ *schemas.BifrostResponsesRequest) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.ResponsesStreamRequest)
}

func (provider *TavilyProvider) CountTokens(_ *schemas.BifrostContext, _ schemas.Key, _ *schemas.BifrostResponsesRequest) (*schemas.BifrostCountTokensResponse, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.CountTokensRequest)
}

func (provider *TavilyProvider) Embedding(_ *schemas.BifrostContext, _ schemas.Key, _ *schemas.BifrostEmbeddingRequest) (*schemas.BifrostEmbeddingResponse, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.EmbeddingRequest)
}

func (provider *TavilyProvider) Rerank(_ *schemas.BifrostContext, _ schemas.Key, _ *schemas.BifrostRerankRequest) (*schemas.BifrostRerankResponse, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.RerankRequest)
}

func (provider *TavilyProvider) OCR(_ *schemas.BifrostContext, _ schemas.Key, _ *schemas.BifrostOCRRequest) (*schemas.BifrostOCRResponse, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.OCRRequest)
}

func (provider *TavilyProvider) Speech(_ *schemas.BifrostContext, _ schemas.Key, _ *schemas.BifrostSpeechRequest) (*schemas.BifrostSpeechResponse, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.SpeechRequest)
}

func (provider *TavilyProvider) SpeechStream(_ *schemas.BifrostContext, _ schemas.PostHookRunner, _ schemas.Key, _ *schemas.BifrostSpeechRequest) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.SpeechStreamRequest)
}

func (provider *TavilyProvider) Transcription(_ *schemas.BifrostContext, _ schemas.Key, _ *schemas.BifrostTranscriptionRequest) (*schemas.BifrostTranscriptionResponse, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.TranscriptionRequest)
}

func (provider *TavilyProvider) TranscriptionStream(_ *schemas.BifrostContext, _ schemas.PostHookRunner, _ schemas.Key, _ *schemas.BifrostTranscriptionRequest) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.TranscriptionStreamRequest)
}

func (provider *TavilyProvider) ImageGeneration(_ *schemas.BifrostContext, _ schemas.Key, _ *schemas.BifrostImageGenerationRequest) (*schemas.BifrostImageGenerationResponse, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.ImageGenerationRequest)
}

func (provider *TavilyProvider) ImageGenerationStream(_ *schemas.BifrostContext, _ schemas.PostHookRunner, _ schemas.Key, _ *schemas.BifrostImageGenerationRequest) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.ImageGenerationStreamRequest)
}

func (provider *TavilyProvider) ImageEdit(_ *schemas.BifrostContext, _ schemas.Key, _ *schemas.BifrostImageEditRequest) (*schemas.BifrostImageGenerationResponse, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.ImageEditRequest)
}

func (provider *TavilyProvider) ImageEditStream(_ *schemas.BifrostContext, _ schemas.PostHookRunner, _ schemas.Key, _ *schemas.BifrostImageEditRequest) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.ImageEditStreamRequest)
}

func (provider *TavilyProvider) ImageVariation(_ *schemas.BifrostContext, _ schemas.Key, _ *schemas.BifrostImageVariationRequest) (*schemas.BifrostImageGenerationResponse, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.ImageVariationRequest)
}

func (provider *TavilyProvider) VideoGeneration(_ *schemas.BifrostContext, _ schemas.Key, _ *schemas.BifrostVideoGenerationRequest) (*schemas.BifrostVideoGenerationResponse, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.VideoGenerationRequest)
}

func (provider *TavilyProvider) VideoRetrieve(_ *schemas.BifrostContext, _ schemas.Key, _ *schemas.BifrostVideoRetrieveRequest) (*schemas.BifrostVideoGenerationResponse, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.VideoRetrieveRequest)
}

func (provider *TavilyProvider) VideoDownload(_ *schemas.BifrostContext, _ schemas.Key, _ *schemas.BifrostVideoDownloadRequest) (*schemas.BifrostVideoDownloadResponse, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.VideoDownloadRequest)
}

func (provider *TavilyProvider) VideoDelete(_ *schemas.BifrostContext, _ schemas.Key, _ *schemas.BifrostVideoDeleteRequest) (*schemas.BifrostVideoDeleteResponse, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.VideoDeleteRequest)
}

func (provider *TavilyProvider) VideoList(_ *schemas.BifrostContext, _ schemas.Key, _ *schemas.BifrostVideoListRequest) (*schemas.BifrostVideoListResponse, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.VideoListRequest)
}

func (provider *TavilyProvider) VideoRemix(_ *schemas.BifrostContext, _ schemas.Key, _ *schemas.BifrostVideoRemixRequest) (*schemas.BifrostVideoGenerationResponse, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.VideoRemixRequest)
}

func (provider *TavilyProvider) BatchCreate(_ *schemas.BifrostContext, _ schemas.Key, _ *schemas.BifrostBatchCreateRequest) (*schemas.BifrostBatchCreateResponse, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.BatchCreateRequest)
}

func (provider *TavilyProvider) BatchList(_ *schemas.BifrostContext, _ []schemas.Key, _ *schemas.BifrostBatchListRequest) (*schemas.BifrostBatchListResponse, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.BatchListRequest)
}

func (provider *TavilyProvider) BatchRetrieve(_ *schemas.BifrostContext, _ []schemas.Key, _ *schemas.BifrostBatchRetrieveRequest) (*schemas.BifrostBatchRetrieveResponse, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.BatchRetrieveRequest)
}

func (provider *TavilyProvider) BatchCancel(_ *schemas.BifrostContext, _ []schemas.Key, _ *schemas.BifrostBatchCancelRequest) (*schemas.BifrostBatchCancelResponse, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.BatchCancelRequest)
}

func (provider *TavilyProvider) BatchDelete(_ *schemas.BifrostContext, _ []schemas.Key, _ *schemas.BifrostBatchDeleteRequest) (*schemas.BifrostBatchDeleteResponse, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.BatchDeleteRequest)
}

func (provider *TavilyProvider) BatchResults(_ *schemas.BifrostContext, _ []schemas.Key, _ *schemas.BifrostBatchResultsRequest) (*schemas.BifrostBatchResultsResponse, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.BatchResultsRequest)
}

func (provider *TavilyProvider) FileUpload(_ *schemas.BifrostContext, _ schemas.Key, _ *schemas.BifrostFileUploadRequest) (*schemas.BifrostFileUploadResponse, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.FileUploadRequest)
}

func (provider *TavilyProvider) FileList(_ *schemas.BifrostContext, _ []schemas.Key, _ *schemas.BifrostFileListRequest) (*schemas.BifrostFileListResponse, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.FileListRequest)
}

func (provider *TavilyProvider) FileRetrieve(_ *schemas.BifrostContext, _ []schemas.Key, _ *schemas.BifrostFileRetrieveRequest) (*schemas.BifrostFileRetrieveResponse, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.FileRetrieveRequest)
}

func (provider *TavilyProvider) FileDelete(_ *schemas.BifrostContext, _ []schemas.Key, _ *schemas.BifrostFileDeleteRequest) (*schemas.BifrostFileDeleteResponse, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.FileDeleteRequest)
}

func (provider *TavilyProvider) FileContent(_ *schemas.BifrostContext, _ []schemas.Key, _ *schemas.BifrostFileContentRequest) (*schemas.BifrostFileContentResponse, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.FileContentRequest)
}

func (provider *TavilyProvider) ContainerCreate(_ *schemas.BifrostContext, _ schemas.Key, _ *schemas.BifrostContainerCreateRequest) (*schemas.BifrostContainerCreateResponse, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.ContainerCreateRequest)
}

func (provider *TavilyProvider) ContainerList(_ *schemas.BifrostContext, _ []schemas.Key, _ *schemas.BifrostContainerListRequest) (*schemas.BifrostContainerListResponse, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.ContainerListRequest)
}

func (provider *TavilyProvider) ContainerRetrieve(_ *schemas.BifrostContext, _ []schemas.Key, _ *schemas.BifrostContainerRetrieveRequest) (*schemas.BifrostContainerRetrieveResponse, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.ContainerRetrieveRequest)
}

func (provider *TavilyProvider) ContainerDelete(_ *schemas.BifrostContext, _ []schemas.Key, _ *schemas.BifrostContainerDeleteRequest) (*schemas.BifrostContainerDeleteResponse, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.ContainerDeleteRequest)
}

func (provider *TavilyProvider) ContainerFileCreate(_ *schemas.BifrostContext, _ schemas.Key, _ *schemas.BifrostContainerFileCreateRequest) (*schemas.BifrostContainerFileCreateResponse, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.ContainerFileCreateRequest)
}

func (provider *TavilyProvider) ContainerFileList(_ *schemas.BifrostContext, _ []schemas.Key, _ *schemas.BifrostContainerFileListRequest) (*schemas.BifrostContainerFileListResponse, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.ContainerFileListRequest)
}

func (provider *TavilyProvider) ContainerFileRetrieve(_ *schemas.BifrostContext, _ []schemas.Key, _ *schemas.BifrostContainerFileRetrieveRequest) (*schemas.BifrostContainerFileRetrieveResponse, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.ContainerFileRetrieveRequest)
}

func (provider *TavilyProvider) ContainerFileContent(_ *schemas.BifrostContext, _ []schemas.Key, _ *schemas.BifrostContainerFileContentRequest) (*schemas.BifrostContainerFileContentResponse, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.ContainerFileContentRequest)
}

func (provider *TavilyProvider) ContainerFileDelete(_ *schemas.BifrostContext, _ []schemas.Key, _ *schemas.BifrostContainerFileDeleteRequest) (*schemas.BifrostContainerFileDeleteResponse, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.ContainerFileDeleteRequest)
}

func (provider *TavilyProvider) Passthrough(_ *schemas.BifrostContext, _ schemas.Key, _ *schemas.BifrostPassthroughRequest) (*schemas.BifrostPassthroughResponse, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.PassthroughRequest)
}

func (provider *TavilyProvider) PassthroughStream(_ *schemas.BifrostContext, _ schemas.PostHookRunner, _ schemas.Key, _ *schemas.BifrostPassthroughRequest) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	return nil, provider.unsupported(schemas.PassthroughStreamRequest)
}
