// Package brave implements the Brave provider and its utility functions.
package brave

// BraveSearchResponse represents the Brave web search response.
type BraveSearchResponse struct {
	Web *BraveWebResults `json:"web,omitempty"`
}

// BraveWebResults contains Brave web search result items.
type BraveWebResults struct {
	Results []BraveResult `json:"results,omitempty"`
}

// BraveResult represents a single Brave web search result.
type BraveResult struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
	Age         string `json:"age,omitempty"`
}

// BraveErrorResponse captures the most common Brave error payload shapes.
type BraveErrorResponse struct {
	Error   *BraveErrorDetail `json:"error,omitempty"`
	Message string            `json:"message,omitempty"`
	Code    string            `json:"code,omitempty"`
	Type    string            `json:"type,omitempty"`
	Detail  string            `json:"detail,omitempty"`
}

// BraveErrorDetail captures nested Brave error payloads.
type BraveErrorDetail struct {
	Message string `json:"message,omitempty"`
	Code    string `json:"code,omitempty"`
	Type    string `json:"type,omitempty"`
	Detail  string `json:"detail,omitempty"`
}
