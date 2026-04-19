// Package tavily implements the Tavily provider and its utility functions.
package tavily

// TavilySearchRequest represents a Tavily search API request.
type TavilySearchRequest struct {
	Query                    string                 `json:"query"`
	SearchDepth              *string                `json:"search_depth,omitempty"`
	ChunksPerSource          *int                   `json:"chunks_per_source,omitempty"`
	MaxResults               *int                   `json:"max_results,omitempty"`
	Topic                    *string                `json:"topic,omitempty"`
	TimeRange                *string                `json:"time_range,omitempty"`
	StartDate                *string                `json:"start_date,omitempty"`
	EndDate                  *string                `json:"end_date,omitempty"`
	IncludeAnswer            *bool                  `json:"include_answer,omitempty"`
	IncludeRawContent        *bool                  `json:"include_raw_content,omitempty"`
	IncludeImages            *bool                  `json:"include_images,omitempty"`
	IncludeImageDescriptions *bool                  `json:"include_image_descriptions,omitempty"`
	IncludeFavicon           *bool                  `json:"include_favicon,omitempty"`
	IncludeDomains           []string               `json:"include_domains,omitempty"`
	ExcludeDomains           []string               `json:"exclude_domains,omitempty"`
	AutoParameters           *bool                  `json:"auto_parameters,omitempty"`
	ExactMatch               *bool                  `json:"exact_match,omitempty"`
	IncludeUsage             *bool                  `json:"include_usage,omitempty"`
	Country                  *string                `json:"country,omitempty"`
	Days                     *int                   `json:"days,omitempty"`
	ExtraParams              map[string]interface{} `json:"-"`
}

// GetExtraParams implements the RequestBodyWithExtraParams interface.
func (r *TavilySearchRequest) GetExtraParams() map[string]interface{} {
	return r.ExtraParams
}

// TavilySearchResponse represents the Tavily search API response.
type TavilySearchResponse struct {
	Query          string                 `json:"query"`
	Answer         string                 `json:"answer,omitempty"`
	Images         []TavilyImage          `json:"images,omitempty"`
	Results        []TavilyResult         `json:"results,omitempty"`
	ResponseTime   *float64               `json:"response_time,omitempty"`
	AutoParameters map[string]interface{} `json:"auto_parameters,omitempty"`
	Usage          *TavilyUsage           `json:"usage,omitempty"`
	RequestID      *string                `json:"request_id,omitempty"`
}

// TavilyUsage represents Tavily credit usage data.
type TavilyUsage struct {
	Credits *float64 `json:"credits,omitempty"`
}

// TavilyResult represents a single Tavily search result.
type TavilyResult struct {
	Title         string        `json:"title"`
	URL           string        `json:"url"`
	Content       string        `json:"content"`
	RawContent    *string       `json:"raw_content,omitempty"`
	Score         *float64      `json:"score,omitempty"`
	Favicon       *string       `json:"favicon,omitempty"`
	PublishedDate *string       `json:"published_date,omitempty"`
	Images        []TavilyImage `json:"images,omitempty"`
}

// TavilyImage represents an image returned by Tavily.
type TavilyImage struct {
	URL         string  `json:"url"`
	Description *string `json:"description,omitempty"`
}

// TavilyErrorResponse captures the common Tavily error shapes.
type TavilyErrorResponse struct {
	Message string             `json:"message,omitempty"`
	Error   *TavilyErrorDetail `json:"error,omitempty"`
	Detail  *TavilyErrorDetail `json:"detail,omitempty"`
	Code    string             `json:"code,omitempty"`
}

// TavilyErrorDetail captures nested error payloads that Tavily may return.
type TavilyErrorDetail struct {
	Message string `json:"message,omitempty"`
	Code    string `json:"code,omitempty"`
	Error   string `json:"error,omitempty"`
	Detail  string `json:"detail,omitempty"`
	Type    string `json:"type,omitempty"`
}
