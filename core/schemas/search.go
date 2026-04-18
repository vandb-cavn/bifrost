package schemas

// BifrostSearchRequest represents a request to search across external search providers.
type BifrostSearchRequest struct {
	Provider       ModelProvider            `json:"provider"`
	Model          string                   `json:"model"`
	Query          string                   `json:"query"`
	Params         *BifrostSearchParameters `json:"params,omitempty"`
	Fallbacks      []Fallback               `json:"fallbacks,omitempty"`
	RawRequestBody []byte                   `json:"-"`
}

// GetRawRequestBody returns the raw request body for the search request.
func (r *BifrostSearchRequest) GetRawRequestBody() []byte {
	return r.RawRequestBody
}

// BifrostSearchParameters contains optional parameters for a search request.
type BifrostSearchParameters struct {
	MaxResults        *int                   `json:"max_results,omitempty"`
	Country           *string                `json:"country,omitempty"`
	Language          *string                `json:"language,omitempty"`
	IncludeAnswer     *bool                  `json:"include_answer,omitempty"`
	IncludeRawContent *bool                  `json:"include_raw_content,omitempty"`
	ExtraParams       map[string]interface{} `json:"-"`
}

// BifrostSearchResult represents a single search result.
type BifrostSearchResult struct {
	Title       string   `json:"title"`
	URL         string   `json:"url"`
	Snippet     string   `json:"snippet"`
	Content     *string  `json:"content,omitempty"`
	PublishedAt *string  `json:"published_at,omitempty"`
	Score       *float64 `json:"score,omitempty"`
	Source      *string  `json:"source,omitempty"`
}

// BifrostSearchUsage represents usage information for a search response.
type BifrostSearchUsage struct {
	Queries int      `json:"queries"`
	Results int      `json:"results"`
	Credits *float64 `json:"credits,omitempty"`
}

// BifrostSearchResponse represents the response from a search request.
type BifrostSearchResponse struct {
	ID          string                     `json:"id,omitempty"`
	Object      string                     `json:"object,omitempty"`
	Query       string                     `json:"query"`
	Results     []BifrostSearchResult      `json:"results"`
	Answer      *string                    `json:"answer,omitempty"`
	Model       string                     `json:"model"`
	Usage       *BifrostSearchUsage        `json:"usage,omitempty"`
	ExtraFields BifrostResponseExtraFields `json:"extra_fields"`
}
