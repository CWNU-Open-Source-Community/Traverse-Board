package llm

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProviderNonStreamingLimitsPreserveUsageAndRejectCalls(t *testing.T) {
	t.Run("Chat Completions", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"model":"gpt-test","choices":[{"index":0,"message":{"role":"assistant","content":"partial","tool_calls":[{"id":"partial","type":"function","function":{"name":"read_file","arguments":"{\"path\":"}}]},"finish_reason":"length"}],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`))
		}))
		defer server.Close()
		response, err := newTestOpenAIProvider(t, server.URL).Chat(t.Context(), ChatRequest{
			Messages: []Message{{Role: "user", Content: "inspect"}},
		})
		assertLimitedResponse(t, response, err, FinishReasonLength, ProviderFailureOutputLimit, 6)
	})

	t.Run("Responses", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"id":"resp_limit","object":"response","status":"incomplete","model":"model-local","output":[],"incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8}}`))
		}))
		defer server.Close()
		response, err := newTestResponsesProvider(t, server.URL).Chat(t.Context(), ChatRequest{
			Messages: []Message{{Role: "user", Content: "inspect"}},
		})
		assertLimitedResponse(t, response, err, FinishReasonLength, ProviderFailureOutputLimit, 8)
	})

	t.Run("Responses refusal", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"id":"resp_refusal","object":"response","status":"completed","model":"model-local","output":[{"id":"message_refusal","type":"message","status":"completed","role":"assistant","content":[{"type":"refusal","refusal":"private refusal detail"}]}],"usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7}}`))
		}))
		defer server.Close()
		response, err := newTestResponsesProvider(t, server.URL).Chat(t.Context(), ChatRequest{
			Messages: []Message{{Role: "user", Content: "inspect"}},
		})
		assertLimitedResponse(t, response, err, FinishReasonRefusal, ProviderFailureRefusal, 7)
		if response.Text != "" {
			t.Fatal("native refusal text escaped through the public response")
		}
	})

	t.Run("Anthropic", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"id":"msg_limit","type":"message","role":"assistant","model":"model","content":[{"type":"tool_use","id":"partial","name":"read_file","input":{"path":"README.md"}}],"stop_reason":"max_tokens","usage":{"input_tokens":6,"output_tokens":4}}`))
		}))
		defer server.Close()
		provider, err := NewAnthropicCompatibleProvider(AnthropicCompatibleConfig{
			Name: "anthropic-test", BaseURL: server.URL, APIKey: "test", DefaultModel: "model",
		})
		if err != nil {
			t.Fatal(err)
		}
		response, callErr := provider.Chat(t.Context(), ChatRequest{
			Messages: []Message{{Role: "user", Content: "inspect"}},
		})
		assertLimitedResponse(t, response, callErr, FinishReasonLength, ProviderFailureOutputLimit, 10)
	})
}

func TestProviderStreamingLimitsEmitFailedTerminalWithUsage(t *testing.T) {
	t.Run("Chat Completions", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "data: {\"model\":\"gpt-test\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"partial\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\"}}]},\"finish_reason\":\"length\"}]}\n\n")
			_, _ = fmt.Fprint(w, "data: {\"model\":\"gpt-test\",\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5}}\n\n")
			_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		}))
		defer server.Close()
		chunks, err := newTestOpenAIProvider(t, server.URL).StreamChat(t.Context(), ChatRequest{
			Messages: []Message{{Role: "user", Content: "inspect"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		assertFailedTerminal(t, chunks, FinishReasonLength, ProviderFailureOutputLimit, 5)
	})

	t.Run("Responses", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			flusher := w.(http.Flusher)
			for _, payload := range []string{
				`{"type":"response.created","response":{"id":"resp_stream_limit","object":"response","status":"in_progress","model":"model-local"}}`,
				`{"type":"response.output_item.added","item":{"id":"call_item","type":"function_call","status":"in_progress","call_id":"wire_call","name":"read_file"}}`,
				`{"type":"response.function_call_arguments.delta","item_id":"call_item","delta":"{\"path\":"}`,
				`{"type":"response.output_item.done","item":{"id":"call_item","type":"function_call","status":"incomplete","call_id":"wire_call","name":"read_file","arguments":"{\"path\":"}}`,
				`{"type":"response.output_item.added","item":{"id":"message_item","type":"message","status":"in_progress","role":"assistant","content":[]}}`,
				`{"type":"response.output_text.delta","item_id":"message_item","delta":"partial"}`,
				`{"type":"response.output_item.done","item":{"id":"message_item","type":"message","status":"incomplete","role":"assistant","content":[{"type":"output_text","text":"partial"}]}}`,
				`{"type":"response.incomplete","response":{"id":"resp_stream_limit","object":"response","status":"incomplete","model":"model-local","incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":4,"output_tokens":2,"total_tokens":6}}}`,
			} {
				_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
				flusher.Flush()
			}
		}))
		defer server.Close()
		chunks, err := newTestResponsesProvider(t, server.URL).StreamChat(t.Context(), ChatRequest{
			Messages: []Message{{Role: "user", Content: "inspect"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		assertFailedTerminal(t, chunks, FinishReasonLength, ProviderFailureOutputLimit, 6)
	})

	t.Run("Responses refusal", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			flusher := w.(http.Flusher)
			for _, payload := range []string{
				`{"type":"response.created","response":{"id":"resp_stream_refusal","object":"response","status":"in_progress","model":"model-local"}}`,
				`{"type":"response.output_item.added","item":{"id":"message_refusal","type":"message","status":"in_progress","role":"assistant","content":[]}}`,
				`{"type":"response.refusal.delta","item_id":"message_refusal","delta":"private "}`,
				`{"type":"response.refusal.delta","item_id":"message_refusal","delta":"refusal"}`,
				`{"type":"response.refusal.done","item_id":"message_refusal","refusal":"private refusal"}`,
				`{"type":"response.output_item.done","item":{"id":"message_refusal","type":"message","status":"completed","role":"assistant","content":[{"type":"refusal","refusal":"private refusal"}]}}`,
				`{"type":"response.completed","response":{"id":"resp_stream_refusal","object":"response","status":"completed","model":"model-local","usage":{"input_tokens":4,"output_tokens":2,"total_tokens":6}}}`,
			} {
				_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
				flusher.Flush()
			}
		}))
		defer server.Close()
		chunks, err := newTestResponsesProvider(t, server.URL).StreamChat(t.Context(), ChatRequest{
			Messages: []Message{{Role: "user", Content: "inspect"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		assertFailedTerminal(t, chunks, FinishReasonRefusal, ProviderFailureRefusal, 6)
	})

	t.Run("Anthropic", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			flusher := w.(http.Flusher)
			for _, payload := range []string{
				`{"type":"message_start","message":{"model":"model","usage":{"input_tokens":5,"output_tokens":0}}}`,
				`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"wire_call","name":"read_file","input":{}}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}`,
				`{"type":"content_block_stop","index":0}`,
				`{"type":"message_delta","delta":{"stop_reason":"max_tokens"},"usage":{"output_tokens":3}}`,
				`{"type":"message_stop"}`,
			} {
				_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
				flusher.Flush()
			}
		}))
		defer server.Close()
		provider, err := NewAnthropicCompatibleProvider(AnthropicCompatibleConfig{
			Name: "anthropic-test", BaseURL: server.URL, APIKey: "test", DefaultModel: "model",
		})
		if err != nil {
			t.Fatal(err)
		}
		chunks, err := provider.StreamChat(t.Context(), ChatRequest{
			Messages: []Message{{Role: "user", Content: "inspect"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		assertFailedTerminal(t, chunks, FinishReasonLength, ProviderFailureOutputLimit, 8)
	})
}

func TestProviderStreamingInvalidPartialCallsFailOnSuccessfulTerminal(t *testing.T) {
	t.Run("Responses", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			flusher := w.(http.Flusher)
			for _, payload := range []string{
				`{"type":"response.created","response":{"id":"resp_stream_invalid","object":"response","status":"in_progress","model":"model-local"}}`,
				`{"type":"response.output_item.added","item":{"id":"call_item","type":"function_call","status":"in_progress","call_id":"wire_call","name":"read_file"}}`,
				`{"type":"response.function_call_arguments.delta","item_id":"call_item","delta":"{\"path\":"}`,
				`{"type":"response.function_call_arguments.done","item_id":"call_item","name":"read_file","arguments":"{\"path\":"}`,
				`{"type":"response.output_item.done","item":{"id":"call_item","type":"function_call","status":"completed","call_id":"wire_call","name":"read_file","arguments":"{\"path\":"}}`,
				`{"type":"response.completed","response":{"id":"resp_stream_invalid","object":"response","status":"completed","model":"model-local","usage":{"input_tokens":4,"output_tokens":2,"total_tokens":6}}}`,
			} {
				_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
				flusher.Flush()
			}
		}))
		defer server.Close()
		chunks, err := newTestResponsesProvider(t, server.URL).StreamChat(t.Context(), ChatRequest{
			Messages: []Message{{Role: "user", Content: "inspect"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		assertProtocolFailureTerminal(t, chunks)
	})

	t.Run("Anthropic", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			flusher := w.(http.Flusher)
			for _, payload := range []string{
				`{"type":"message_start","message":{"model":"model","usage":{"input_tokens":5,"output_tokens":0}}}`,
				`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"wire_call","name":"read_file","input":{}}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}`,
				`{"type":"content_block_stop","index":0}`,
				`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":3}}`,
				`{"type":"message_stop"}`,
			} {
				_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
				flusher.Flush()
			}
		}))
		defer server.Close()
		provider, err := NewAnthropicCompatibleProvider(AnthropicCompatibleConfig{
			Name: "anthropic-test", BaseURL: server.URL, APIKey: "test", DefaultModel: "model",
		})
		if err != nil {
			t.Fatal(err)
		}
		chunks, err := provider.StreamChat(t.Context(), ChatRequest{
			Messages: []Message{{Role: "user", Content: "inspect"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		assertProtocolFailureTerminal(t, chunks)
	})

	t.Run("Responses completed rejects incomplete item", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			flusher := w.(http.Flusher)
			for _, payload := range []string{
				`{"type":"response.created","response":{"id":"resp_bad_completed","object":"response","status":"in_progress","model":"model-local"}}`,
				`{"type":"response.output_item.added","item":{"id":"message_item","type":"message","status":"in_progress","role":"assistant","content":[]}}`,
				`{"type":"response.output_text.delta","item_id":"message_item","delta":"partial"}`,
				`{"type":"response.output_item.done","item":{"id":"message_item","type":"message","status":"incomplete","role":"assistant","content":[{"type":"output_text","text":"partial"}]}}`,
				`{"type":"response.completed","response":{"id":"resp_bad_completed","object":"response","status":"completed","model":"model-local","usage":{"input_tokens":4,"output_tokens":2,"total_tokens":6}}}`,
			} {
				_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
				flusher.Flush()
			}
		}))
		defer server.Close()
		chunks, err := newTestResponsesProvider(t, server.URL).StreamChat(t.Context(), ChatRequest{
			Messages: []Message{{Role: "user", Content: "inspect"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		assertProtocolFailureTerminal(t, chunks)
	})
}

func TestResponsesReplayRestoresNativeOrderAndWireCallIdentity(t *testing.T) {
	provider := newTestResponsesProvider(t, "https://example.invalid")
	input, output, total := 7, 3, 10
	response, err := provider.normalizeResponse("model-local", openAIResponsesResponse{
		ID: "resp_native", Object: "response", Status: "completed", Model: "model-local",
		Output: []openAIResponsesOutputItem{
			{ID: "reasoning_1", Type: "reasoning", Status: "completed",
				EncryptedContent: "opaque-state", Summary: json.RawMessage(`[{"type":"summary_text","text":"safe summary"}]`)},
			{ID: "message_1", Type: "message", Status: "completed", Role: "assistant",
				Phase: "commentary", Content: []openAIResponsesContent{{Type: "output_text", Text: "checking"}}},
			{ID: "function_1", Type: "function_call", Status: "completed", CallID: "wire_call",
				Name: "read_file", Arguments: `{"path":"README.md"}`},
		},
		Usage: &openAIResponsesUsage{InputTokens: &input, OutputTokens: &output, TotalTokens: &total},
	})
	if err != nil || response.Replay == nil || len(response.ToolCalls) != 1 {
		t.Fatalf("native response was not replayable: response=%#v err=%v", response, err)
	}
	prepared := append([]ToolCall(nil), response.ToolCalls...)
	prepared[0].ID = "durable_call"
	bound, err := response.Replay.BindToolCalls(prepared)
	if err != nil {
		t.Fatal(err)
	}
	_, wire, err := provider.prepareRequest(ChatRequest{Model: "model-local", Messages: []Message{
		{Role: "assistant", Content: response.Text, ToolCalls: prepared, Replay: bound},
		{Role: "user", ToolResults: []ToolResult{{ToolCallID: "durable_call", Content: `{"ok":true}`}}},
	}}, false)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(wire.Input)
	if err != nil {
		t.Fatal(err)
	}
	got := string(encoded)
	for _, want := range []string{`"type":"reasoning"`, `"id":"message_1"`,
		`"call_id":"wire_call"`, `"type":"function_call_output"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("replay input omitted %s: %s", want, got)
		}
	}
	if strings.Contains(got, "durable_call") {
		t.Fatalf("durable call id crossed the native provider boundary: %s", got)
	}
	if strings.Index(got, `"type":"reasoning"`) > strings.Index(got, `"id":"message_1"`) ||
		strings.Index(got, `"id":"message_1"`) > strings.Index(got, `"type":"function_call"`) {
		t.Fatalf("native output order changed: %s", got)
	}
	finalText, err := provider.normalizeResponse("model-local", openAIResponsesResponse{
		ID: "resp_final", Object: "response", Status: "completed", Model: "model-local",
		Output: []openAIResponsesOutputItem{{ID: "message_final", Type: "message", Status: "completed",
			Role: "assistant", Content: []openAIResponsesContent{{Type: "output_text", Text: "done"}}}},
		Usage: &openAIResponsesUsage{InputTokens: &input, OutputTokens: &output, TotalTokens: &total},
	})
	if err != nil || finalText.Replay != nil || finalText.FinishReason != FinishReasonStop {
		t.Fatalf("tool-free final text retained replay state: response=%#v err=%v", finalText, err)
	}
}

func assertLimitedResponse(t *testing.T, response *ChatResponse, err error,
	finish FinishReason, failure ProviderFailureReason, total int,
) {
	t.Helper()
	if response == nil || response.FinishReason != finish || response.Usage.TotalTokens != total ||
		len(response.ToolCalls) != 0 || ProviderErrorKind(err) != OutcomePermanent ||
		ProviderErrorReason(err) != failure {
		t.Fatalf("limited response=%#v err=%#v", response, err)
	}
}

func assertFailedTerminal(t *testing.T, chunks <-chan ChatChunk, finish FinishReason,
	failure ProviderFailureReason, total int,
) {
	t.Helper()
	var terminal *ChatChunk
	for chunk := range chunks {
		if chunk.Err != nil {
			copy := chunk
			terminal = &copy
		}
	}
	if terminal == nil || terminal.Done || terminal.FinishReason != finish || terminal.Usage == nil ||
		terminal.Usage.TotalTokens != total || len(terminal.ToolCalls) != 0 ||
		ProviderErrorKind(terminal.Err) != OutcomePermanent || ProviderErrorReason(terminal.Err) != failure ||
		len(terminal.Events) != 1 || terminal.Events[0].Type != StreamResponseFailed {
		t.Fatalf("failed terminal=%#v", terminal)
	}
}

func assertProtocolFailureTerminal(t *testing.T, chunks <-chan ChatChunk) {
	t.Helper()
	var terminal *ChatChunk
	for chunk := range chunks {
		if chunk.Err != nil {
			copy := chunk
			terminal = &copy
		}
	}
	if terminal == nil || terminal.Done || len(terminal.ToolCalls) != 0 ||
		ProviderErrorKind(terminal.Err) != OutcomeInvalidResponse ||
		len(terminal.Events) != 1 || terminal.Events[0].Type != StreamResponseFailed {
		t.Fatalf("protocol failure terminal=%#v", terminal)
	}
}
