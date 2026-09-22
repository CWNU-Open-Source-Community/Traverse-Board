package webevidence

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

func TestAnthropicSearchLedgerRejectsDuplicatePendingToolUseID(t *testing.T) {
	ledger := newAnthropicSearchLedger(3)
	response := anthropicSearchResponse{Content: []json.RawMessage{
		json.RawMessage(`{"type":"server_tool_use","id":"srvtoolu_same","name":"web_search","input":{"query":"one"}}`),
		json.RawMessage(`{"type":"server_tool_use","id":"srvtoolu_same","name":"web_search","input":{"query":"two"}}`),
	}}
	err := ledger.consume(response)
	var qualification *NativeSearchQualificationError
	if !errors.As(err, &qualification) || qualification.Reason != NativeSearchReasonResponseInvalid {
		t.Fatalf("duplicate pending id err=%v qualification=%#v", err, qualification)
	}
}

func TestAnthropicSearchProviderReplaysPauseAndKeepsOnlyPairedResults(t *testing.T) {
	runtime := newResponsesSearchRuntimeStub("anthropic-credential")
	requests := 0
	client := responsesSearchTestClient(t, func(request *http.Request) (*http.Response, error) {
		requests++
		var body struct {
			Messages []json.RawMessage `json:"messages"`
			Tools    []map[string]any  `json:"tools"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Tools) != 1 || body.Tools[0]["type"] != anthropicSearchToolType {
			t.Fatalf("missing Anthropic search tool: %#v", body.Tools)
		}
		allowed, _ := body.Tools[0]["allowed_domains"].([]any)
		if len(allowed) != 1 || allowed[0] != "example.com" {
			t.Fatalf("domain filter not sent: %#v", body.Tools[0])
		}
		headers := http.Header{"Content-Type": []string{"application/json"}}
		if requests == 1 {
			return webResponse(200, headers, `{"type":"message","role":"assistant","stop_reason":"pause_turn","content":[{"type":"server_tool_use","id":"srvtoolu_1","name":"web_search","input":{"query":"release"}}]}`), nil
		}
		if len(body.Messages) != 2 {
			t.Fatalf("paused assistant content not replayed: %#v", body.Messages)
		}
		return webResponse(200, headers, `{"type":"message","role":"assistant","stop_reason":"end_turn","content":[{"type":"web_search_tool_result","tool_use_id":"srvtoolu_1","content":[{"type":"web_search_result","url":"https://docs.example.com/release","title":"Release","encrypted_content":"opaque"}]},{"type":"text","text":"invented https://evil.example.net/","citations":[{"type":"web_search_result_location","url":"https://docs.example.com/release","title":"Official","cited_text":"released today"}]}]}`), nil
	})
	provider, err := NewAnthropicSearchProvider(client, "https://api.vendor.com/v1", "anthropic", "claude", runtime)
	if err != nil {
		t.Fatal(err)
	}
	results, err := provider.SearchFiltered(t.Context(), SearchRequest{Query: "release", Limit: 3,
		AllowedDomains: []string{"example.com"}}, responsesSearchAuthority())
	if err != nil || requests != 2 || len(results) != 1 ||
		results[0].URL != "https://docs.example.com/release" || results[0].Title != "Official" ||
		results[0].Snippet != "released today" {
		t.Fatalf("requests=%d results=%#v err=%v", requests, results, err)
	}
}
