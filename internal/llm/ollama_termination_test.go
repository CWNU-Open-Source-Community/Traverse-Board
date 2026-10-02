package llm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOllamaLengthTerminationRetainsUsageAndRejectsTools(t *testing.T) {
	for _, payload := range []struct {
		name    string
		message string
	}{
		{"text", `{"role":"assistant","content":"partial answer"}`},
		{"valid_partial_tool", `{"role":"assistant","tool_calls":[{"function":{"name":"read_file","arguments":{"path":"README.md"}}}]}`},
		{"invalid_truncated_tool", `{"role":"assistant","tool_calls":[{"function":{"name":"read_file","arguments":"{\"path\":"}}]}`},
		{"empty_output", `{"role":"assistant","content":""}`},
	} {
		for _, mode := range []string{"chat", "stream_same_event", "stream_before_terminal"} {
			t.Run(payload.name+"/"+mode, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/api/chat" {
						t.Errorf("unexpected path %s", r.URL.Path)
						return
					}
					var request struct {
						Stream  bool `json:"stream"`
						Options struct {
							NumPredict int `json:"num_predict"`
						} `json:"options"`
					}
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
						return
					}
					if request.Stream != (mode != "chat") || request.Options.NumPredict != 5 {
						t.Errorf("request changed its output bound or mode: %+v", request)
					}
					message := json.RawMessage(payload.message)
					if mode == "stream_before_terminal" {
						w.Header().Set("Content-Type", "application/x-ndjson")
						_ = json.NewEncoder(w).Encode(map[string]any{"model": "model", "done": false, "message": message})
						message = json.RawMessage(`{"role":"assistant","content":""}`)
					}
					_ = json.NewEncoder(w).Encode(map[string]any{
						"model": "model", "done": true, "done_reason": "length", "message": message,
						"prompt_eval_count": 9, "eval_count": 5,
					})
				}))
				defer server.Close()
				provider := newTestOllamaProvider(t, server)
				request := ChatRequest{Model: "model", MaxTokens: 5,
					Messages: []Message{{Role: "user", Content: "inspect"}}}
				if mode == "chat" {
					response, err := provider.Chat(t.Context(), request)
					assertLimitedResponse(t, response, err, FinishReasonLength, ProviderFailureOutputLimit, 14)
					if response.Usage.InputTokens != 9 || response.Usage.OutputTokens != 5 {
						t.Fatalf("authoritative counts changed: %+v", response.Usage)
					}
					return
				}
				chunks, err := provider.StreamChat(t.Context(), request)
				if err != nil {
					t.Fatal(err)
				}
				accumulator, err := NewItemStreamAccumulator("length-fixture", provider.Name(), "model")
				if err != nil {
					t.Fatal(err)
				}
				var terminal *ChatChunk
				for chunk := range chunks {
					if _, _, err := accumulator.Consume(chunk); err != nil {
						t.Fatalf("length became an invalid item stream before its receipt: %v", err)
					}
					if chunk.Done || len(chunk.ToolCalls) != 0 || chunk.Replay != nil {
						t.Fatalf("incomplete response exposed accepted output: %+v", chunk)
					}
					for _, event := range chunk.Events {
						if event.Type == StreamResponseCompleted || event.Type == StreamToolCallCompleted ||
							event.CompletedCall != nil {
							t.Fatalf("incomplete response exposed a completed item: %+v", event)
						}
					}
					if chunk.Err != nil {
						copy := chunk
						terminal = &copy
					}
				}
				if terminal == nil || terminal.FinishReason != FinishReasonLength ||
					terminal.Usage == nil || *terminal.Usage != (Usage{InputTokens: 9, OutputTokens: 5, TotalTokens: 14}) ||
					ProviderErrorKind(terminal.Err) != OutcomePermanent ||
					ProviderErrorReason(terminal.Err) != ProviderFailureOutputLimit {
					t.Fatalf("output-limit terminal lost reason or usage: %+v", terminal)
				}
				if strings.Contains(terminal.Err.Error(), "README.md") || strings.Contains(terminal.Err.Error(), "partial answer") {
					t.Fatal("untrusted payload escaped in diagnostics")
				}
			})
		}
	}
}

func TestOllamaTerminationReasonCompatibility(t *testing.T) {
	for _, test := range []struct {
		name       string
		reason     string
		omitReason bool
		done       bool
		tools      bool
		wantError  bool
		wantFinish FinishReason
	}{
		{name: "stop_text", reason: "stop", done: true, wantFinish: FinishReasonStop},
		{name: "stop_tools", reason: "stop", done: true, tools: true, wantFinish: FinishReasonStop},
		{name: "optional_reason_omitted", omitReason: true, done: true},
		{name: "optional_reason_empty", done: true},
		{name: "unknown_reason", reason: "private-unknown-reason-canary", done: true, wantError: true},
		{name: "missing_terminal", reason: "length", wantError: true},
	} {
		for _, streaming := range []bool{false, true} {
			t.Run(test.name+map[bool]string{false: "/chat", true: "/stream"}[streaming], func(t *testing.T) {
				message := map[string]any{"role": "assistant", "content": "complete answer"}
				if test.tools {
					message["content"] = ""
					message["tool_calls"] = []map[string]any{{"function": map[string]any{
						"name": "read_file", "arguments": map[string]any{"path": "README.md"},
					}}}
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					body := map[string]any{"model": "model", "done": test.done, "message": message,
						"prompt_eval_count": 9, "eval_count": 5}
					if !test.omitReason {
						body["done_reason"] = test.reason
					}
					_ = json.NewEncoder(w).Encode(body)
				}))
				defer server.Close()
				provider := newTestOllamaProvider(t, server)
				request := ChatRequest{Model: "model", Messages: []Message{{Role: "user", Content: "inspect"}}}
				if !streaming {
					response, err := provider.Chat(t.Context(), request)
					if test.wantError {
						if response != nil || ProviderErrorKind(err) != OutcomeInvalidResponse ||
							ProviderErrorReason(err) != ProviderFailureProtocolIncompatible ||
							strings.Contains(err.Error(), test.reason) && test.reason != "length" {
							t.Fatalf("invalid terminal accepted or leaked: response=%+v err=%v", response, err)
						}
						return
					}
					if err != nil || response == nil || response.FinishReason != test.wantFinish ||
						response.Usage.TotalTokens != 14 || (len(response.ToolCalls) == 1) != test.tools {
						t.Fatalf("normal terminal changed: response=%+v err=%v", response, err)
					}
					return
				}
				chunks, err := provider.StreamChat(t.Context(), request)
				if err != nil {
					t.Fatal(err)
				}
				accumulator, _ := NewItemStreamAccumulator("stop-fixture", provider.Name(), "model")
				var terminal *ChatChunk
				for chunk := range chunks {
					if _, _, err := accumulator.Consume(chunk); err != nil && !test.wantError {
						t.Fatalf("invalid normalized stream: %v", err)
					}
					if chunk.Done || chunk.Err != nil {
						copy := chunk
						terminal = &copy
					}
				}
				if test.wantError {
					if terminal == nil || terminal.Done || ProviderErrorKind(terminal.Err) != OutcomeInvalidResponse ||
						ProviderErrorReason(terminal.Err) != ProviderFailureProtocolIncompatible {
						t.Fatalf("invalid stream terminal accepted: %+v", terminal)
					}
					return
				}
				if terminal == nil || terminal.Err != nil || !terminal.Done || terminal.FinishReason != test.wantFinish ||
					terminal.Usage == nil || terminal.Usage.TotalTokens != 14 ||
					(len(terminal.ToolCalls) == 1) != test.tools {
					t.Fatalf("normal stream terminal changed: %+v", terminal)
				}
			})
		}
	}
}
