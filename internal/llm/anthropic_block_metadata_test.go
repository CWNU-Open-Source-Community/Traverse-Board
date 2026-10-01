package llm

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func writeAnthropicMetadataFixture(w http.ResponseWriter, stream bool, blocks []json.RawMessage, stop string, citationDelta ...json.RawMessage) {
	if !stream {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "msg_metadata", "model": anthropicReplayTestModel,
			"content": blocks, "stop_reason": stop, "usage": map[string]int{"input_tokens": 2, "output_tokens": 3}})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = fmt.Fprintf(w, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_metadata\",\"model\":%q,\"usage\":{\"input_tokens\":2}}}\n\n", anthropicReplayTestModel)
	for index, block := range blocks {
		_, _ = fmt.Fprintf(w, "data: {\"type\":\"content_block_start\",\"index\":%d,\"content_block\":%s}\n\n", index, block)
		var kind struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(block, &kind)
		if kind.Type == "text" && len(citationDelta) != 0 {
			_, _ = fmt.Fprintf(w, "data: {\"type\":\"content_block_delta\",\"index\":%d,\"delta\":{\"type\":\"citations_delta\",\"citation\":%s}}\n\n", index, citationDelta[0])
		}
		_, _ = fmt.Fprintf(w, "data: {\"type\":\"content_block_stop\",\"index\":%d}\n\n", index)
	}
	_, _ = fmt.Fprintf(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":%q},\"usage\":{\"output_tokens\":3}}\n\ndata: {\"type\":\"message_stop\"}\n\n", stop)
}

func TestAnthropicOrdinaryCitationDeltaDoesNotRequirePrivateReplay(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeAnthropicMetadataFixture(w, true, []json.RawMessage{json.RawMessage(`{"type":"text","text":"ok","citations":null}`)}, "end_turn",
			json.RawMessage(`{"type":"page_location","cited_text":"source","document_index":0,"document_title":null,"start_page_number":1,"end_page_number":2}`))
	}))
	defer server.Close()
	provider, err := NewAnthropicCompatibleProvider(AnthropicCompatibleConfig{Name: "anthropic-test", BaseURL: server.URL,
		APIKey: "fixture-secret", DefaultModel: anthropicReplayTestModel})
	if err != nil {
		t.Fatal(err)
	}
	response, err := anthropicReplayTestChat(t, provider, ChatRequest{Messages: []Message{{Role: "user", Content: "inspect"}}}, true)
	if err != nil || response.Text != "ok" || response.Replay != nil {
		t.Fatalf("ordinary citation delta rejected public text: %v", err)
	}
}

func TestAnthropicOfficialPublicBlockMetadataChatAndStream(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, test := range []struct {
			name, block, stop string
			tool              bool
		}{
			{"nullable citations", `{"type":"text","text":"ok","citations":null}`, "end_turn", false},
			{"empty citations", `{"type":"text","text":"ok","citations":[]}`, "end_turn", false},
			{"ordinary cited text", `{"type":"text","text":"ok","citations":[{"type":"page_location","cited_text":"source","document_index":0,"document_title":null,"start_page_number":1,"end_page_number":2}]}`, "end_turn", false},
			{"direct caller", `{"type":"tool_use","id":"native-tool","name":"echo","input":{},"caller":{"type":"direct"},"toolset_name":null}`, "tool_use", true},
			{"nullable caller", `{"type":"tool_use","id":"native-tool","name":"echo","input":{},"caller":null,"toolset_name":""}`, "tool_use", true},
		} {
			t.Run(fmt.Sprintf("stream=%t/%s", stream, test.name), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					writeAnthropicMetadataFixture(w, stream, []json.RawMessage{json.RawMessage(test.block)}, test.stop)
				}))
				defer server.Close()
				provider, err := NewAnthropicCompatibleProvider(AnthropicCompatibleConfig{Name: "anthropic-test", BaseURL: server.URL,
					APIKey: "fixture-secret", DefaultModel: anthropicReplayTestModel})
				if err != nil {
					t.Fatal(err)
				}
				response, err := anthropicReplayTestChat(t, provider, ChatRequest{Messages: []Message{{Role: "user", Content: "inspect"}}}, stream)
				if err != nil {
					t.Fatal(err)
				}
				if response.Replay != nil || (!test.tool && response.Text != "ok") || (test.tool &&
					(len(response.ToolCalls) != 1 || response.ToolCalls[0].Name != "echo")) {
					t.Fatal("official ordinary metadata changed public text or direct tool acceptance")
				}
			})
		}
	}
}

func TestAnthropicSupportedMetadataSurvivesPrivateReplay(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, citations := range []string{"null", "[]"} {
			t.Run(fmt.Sprintf("stream=%t/citations=%s", stream, citations), func(t *testing.T) {
				blocks := []json.RawMessage{
					json.RawMessage(`{"type":"thinking","thinking":"","signature":"private-signature  \n"}`),
					json.RawMessage(`{"type":"text","text":"ok","citations":` + citations + `}`),
					json.RawMessage(`{"type":"tool_use","id":"native-tool","name":"echo","input":{"n":1},"caller":{"type":"direct"},"toolset_name":null}`),
				}
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if requests.Add(1) == 1 {
						writeAnthropicMetadataFixture(w, stream, blocks, "tool_use")
						return
					}
					var body struct {
						Messages []struct {
							Content json.RawMessage `json:"content"`
						} `json:"messages"`
					}
					if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Messages) != 3 {
						t.Error("invalid continuation request")
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					var actual, expected any
					_ = json.Unmarshal(body.Messages[1].Content, &actual)
					raw, _ := json.Marshal(blocks)
					_ = json.Unmarshal(raw, &expected)
					if !reflect.DeepEqual(actual, expected) {
						t.Error("supported metadata changed in the actual native replay request")
					}
					writeAnthropicMetadataFixture(w, stream, []json.RawMessage{json.RawMessage(`{"type":"text","text":"done","citations":null}`)}, "end_turn")
				}))
				defer server.Close()
				provider, err := NewAnthropicCompatibleProvider(AnthropicCompatibleConfig{Name: "anthropic-test", BaseURL: server.URL,
					APIKey: "fixture-secret", DefaultModel: anthropicReplayTestModel})
				if err != nil {
					t.Fatal(err)
				}
				req := ChatRequest{Messages: []Message{{Role: "user", Content: "inspect"}}}
				response, err := anthropicReplayTestChat(t, provider, req, stream)
				if err != nil || response.Replay == nil {
					t.Fatalf("missing native replay: %v", err)
				}
				calls := append([]ToolCall(nil), response.ToolCalls...)
				calls[0].ID = "durable-tool"
				bound, err := response.Replay.BindToolCalls(calls)
				if err != nil {
					t.Fatal(err)
				}
				encoded, err := bound.EncodeForStore()
				if err != nil {
					t.Fatal(err)
				}
				restored, err := DecodeProviderReplay(encoded)
				if err != nil {
					t.Fatal(err)
				}
				req.Messages = append(req.Messages, Message{Role: "assistant", Content: response.Text, ToolCalls: calls, Replay: restored},
					Message{Role: "user", ToolResults: []ToolResult{{ToolCallID: calls[0].ID, Content: "result"}}})
				final, err := anthropicReplayTestChat(t, provider, req, stream)
				if err != nil || final.Text != "done" || requests.Load() != 2 {
					t.Fatalf("native continuation failed: %v", err)
				}
			})
		}
	}
}

func TestAnthropicMetadataCannotExpandLocalToolAuthorityOrPrivateSchema(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for name, block := range map[string]string{
			"server caller":          `{"type":"tool_use","id":"native-tool","name":"echo","input":{},"caller":{"type":"code_execution_20250825","tool_id":"server-tool"}}`,
			"new server caller":      `{"type":"tool_use","id":"native-tool","name":"echo","input":{},"caller":{"type":"code_execution_20260120","tool_id":"server-tool"}}`,
			"unknown caller":         `{"type":"tool_use","id":"native-tool","name":"echo","input":{},"caller":{"type":"future_caller"}}`,
			"direct extra fields":    `{"type":"tool_use","id":"native-tool","name":"echo","input":{},"caller":{"type":"direct","tool_id":"server-tool"}}`,
			"toolset":                `{"type":"tool_use","id":"native-tool","name":"echo","input":{},"toolset_name":"remote-family"}`,
			"thinking metadata":      `{"type":"thinking","thinking":"","signature":"private-signature","citations":null}`,
			"private cited text":     `{"type":"text","text":"ok","citations":[{"type":"page_location","cited_text":"source"}]}`,
			"private citation delta": `{"type":"text","text":"ok","citations":null}`,
		} {
			t.Run(fmt.Sprintf("stream=%t/%s", stream, name), func(t *testing.T) {
				blocks := []json.RawMessage{json.RawMessage(`{"type":"thinking","thinking":"","signature":"private-signature"}`), json.RawMessage(block)}
				if name == "private cited text" || name == "private citation delta" || name == "thinking metadata" {
					blocks = append(blocks, json.RawMessage(`{"type":"tool_use","id":"native-tool","name":"echo","input":{}}`))
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if name == "private citation delta" {
						if !stream {
							blocks[1] = json.RawMessage(`{"type":"text","text":"ok","citations":[{"type":"page_location","cited_text":"source"}]}`)
						}
						writeAnthropicMetadataFixture(w, stream, blocks, "tool_use", json.RawMessage(`{"type":"page_location","cited_text":"source"}`))
					} else {
						writeAnthropicMetadataFixture(w, stream, blocks, "tool_use")
					}
				}))
				defer server.Close()
				provider, err := NewAnthropicCompatibleProvider(AnthropicCompatibleConfig{Name: "anthropic-test", BaseURL: server.URL,
					APIKey: "fixture-secret", DefaultModel: anthropicReplayTestModel})
				if err != nil {
					t.Fatal(err)
				}
				response, err := anthropicReplayTestChat(t, provider, ChatRequest{Messages: []Message{{Role: "user", Content: "inspect"}}}, stream)
				if ProviderErrorKind(err) != OutcomeInvalidResponse || (response != nil && (len(response.ToolCalls) != 0 || response.Replay != nil)) ||
					strings.Contains(err.Error(), "private-signature") {
					t.Fatal("unsupported authority or private schema was accepted or exposed")
				}
			})
		}
	}
}
