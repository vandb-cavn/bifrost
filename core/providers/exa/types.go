// Package exa implements the Exa provider and its utility functions.
package exa

// ExaSearchRequest represents an Exa search request.
type ExaSearchRequest struct {
	Query              string                 `json:"query"`
	Type               *string                `json:"type,omitempty"`
	Category           *string                `json:"category,omitempty"`
	UserLocation       *string                `json:"userLocation,omitempty"`
	NumResults         *int                   `json:"numResults,omitempty"`
	AdditionalQueries  []string               `json:"additionalQueries,omitempty"`
	IncludeDomains     []string               `json:"includeDomains,omitempty"`
	ExcludeDomains     []string               `json:"excludeDomains,omitempty"`
	StartPublishedDate *string                `json:"startPublishedDate,omitempty"`
	EndPublishedDate   *string                `json:"endPublishedDate,omitempty"`
	SystemPrompt       *string                `json:"systemPrompt,omitempty"`
	Stream             *bool                  `json:"stream,omitempty"`
	OutputSchema       map[string]interface{} `json:"outputSchema,omitempty"`
	Contents           *ExaContentsRequest    `json:"contents,omitempty"`
	ExtraParams        map[string]interface{} `json:"-"`
}

// GetExtraParams implements the RequestBodyWithExtraParams interface.
func (r *ExaSearchRequest) GetExtraParams() map[string]interface{} {
	return r.ExtraParams
}

// ExaContentsRequest configures which content Exa should return with results.
type ExaContentsRequest struct {
	Text       *bool                 `json:"text,omitempty"`
	Highlights *ExaHighlightsRequest `json:"highlights,omitempty"`
	Summary    *ExaSummaryRequest    `json:"summary,omitempty"`
	Context    *ExaContextRequest    `json:"context,omitempty"`
}

// ExaHighlightsRequest configures highlight generation.
type ExaHighlightsRequest struct {
	Query            *string `json:"query,omitempty"`
	NumSentences     *int    `json:"numSentences,omitempty"`
	HighlightsPerURL *int    `json:"highlightsPerUrl,omitempty"`
	MaxCharacters    *int    `json:"maxCharacters,omitempty"`
}

// ExaSummaryRequest configures summary generation.
type ExaSummaryRequest struct {
	Query  *string                `json:"query,omitempty"`
	Schema map[string]interface{} `json:"schema,omitempty"`
}

// ExaContextRequest configures context string generation.
type ExaContextRequest struct {
	MaxCharacters *int `json:"maxCharacters,omitempty"`
}

// ExaSearchResponse represents the Exa search response.
type ExaSearchResponse struct {
	RequestID   string          `json:"requestId,omitempty"`
	Results     []ExaResult     `json:"results,omitempty"`
	SearchType  string          `json:"searchType,omitempty"`
	Context     string          `json:"context,omitempty"`
	Output      *ExaOutput      `json:"output,omitempty"`
	CostDollars *ExaCostDollars `json:"costDollars,omitempty"`
}

// ExaOutput represents the synthesized output payload from Exa.
type ExaOutput struct {
	Content   string         `json:"content,omitempty"`
	Grounding []ExaGrounding `json:"grounding,omitempty"`
}

// ExaGrounding represents grounded citations in Exa output.
type ExaGrounding struct {
	Field      string        `json:"field,omitempty"`
	Citations  []ExaCitation `json:"citations,omitempty"`
	Confidence string        `json:"confidence,omitempty"`
}

// ExaCitation represents a citation in Exa output.
type ExaCitation struct {
	URL   string `json:"url,omitempty"`
	Title string `json:"title,omitempty"`
}

// ExaCostDollars captures request cost information from Exa.
type ExaCostDollars struct {
	Total            *float64           `json:"total,omitempty"`
	BreakDown        []ExaCostBreakDown `json:"breakDown,omitempty"`
	PerRequestPrices map[string]float64 `json:"perRequestPrices,omitempty"`
	PerPagePrices    map[string]float64 `json:"perPagePrices,omitempty"`
}

// ExaCostBreakDown captures per-operation search cost details.
type ExaCostBreakDown struct {
	Search    *float64           `json:"search,omitempty"`
	Contents  *float64           `json:"contents,omitempty"`
	Breakdown map[string]float64 `json:"breakdown,omitempty"`
}

// ExaResult represents a single Exa search result.
type ExaResult struct {
	Title           string                 `json:"title"`
	URL             string                 `json:"url"`
	PublishedDate   *string                `json:"publishedDate,omitempty"`
	Author          *string                `json:"author,omitempty"`
	ID              *string                `json:"id,omitempty"`
	Image           *string                `json:"image,omitempty"`
	Favicon         *string                `json:"favicon,omitempty"`
	Text            *string                `json:"text,omitempty"`
	Highlights      []string               `json:"highlights,omitempty"`
	HighlightScores []float64              `json:"highlightScores,omitempty"`
	Summary         *string                `json:"summary,omitempty"`
	Subpages        []ExaResult            `json:"subpages,omitempty"`
	Score           *float64               `json:"score,omitempty"`
	Extras          map[string]interface{} `json:"extras,omitempty"`
}

// ExaErrorResponse captures the common Exa error payload shape.
type ExaErrorResponse struct {
	RequestID string `json:"requestId,omitempty"`
	Error     string `json:"error,omitempty"`
	Message   string `json:"message,omitempty"`
	Detail    string `json:"detail,omitempty"`
}
