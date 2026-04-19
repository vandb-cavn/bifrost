package logging

import (
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/logstore"
)

func TestExtractInputHistorySearchRequest(t *testing.T) {
	t.Parallel()

	plugin := &LoggerPlugin{}
	query := "bifrost gateway"
	request := &schemas.BifrostRequest{
		RequestType: schemas.SearchRequest,
		SearchRequest: &schemas.BifrostSearchRequest{
			Query: query,
		},
	}

	inputHistory, responsesInputHistory := plugin.extractInputHistory(request)
	if len(responsesInputHistory) != 0 {
		t.Fatalf("responses input history = %+v, want empty", responsesInputHistory)
	}
	if len(inputHistory) != 1 {
		t.Fatalf("input history len = %d, want 1", len(inputHistory))
	}
	if inputHistory[0].Role != schemas.ChatMessageRoleUser {
		t.Fatalf("input history role = %q, want user", inputHistory[0].Role)
	}
	if inputHistory[0].Content == nil || inputHistory[0].Content.ContentStr == nil || *inputHistory[0].Content.ContentStr != query {
		t.Fatalf("input history content = %+v, want %q", inputHistory[0].Content, query)
	}
}

func TestApplyNonStreamingOutputToEntrySearchResponse(t *testing.T) {
	t.Parallel()

	plugin := &LoggerPlugin{}
	snippet := "search result snippet"
	entry := &logstore.Log{}
	result := &schemas.BifrostResponse{
		SearchResponse: &schemas.BifrostSearchResponse{
			Results: []schemas.BifrostSearchResult{
				{
					Snippet: snippet,
				},
			},
		},
	}

	plugin.applyNonStreamingOutputToEntry(entry, result)
	if entry.OutputMessageParsed == nil || entry.OutputMessageParsed.Content == nil || entry.OutputMessageParsed.Content.ContentStr == nil || *entry.OutputMessageParsed.Content.ContentStr != snippet {
		t.Fatalf("output message = %+v, want snippet %q", entry.OutputMessageParsed, snippet)
	}
}
