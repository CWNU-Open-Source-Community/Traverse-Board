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

func TestOpenAIResponsesProviderMapsToolLoopAndKeepsStoreDisabled(t *testing.T) {
	var captured map[string]any
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter,
		request *http.Request,
	) {
		path = request.URL.Path
		decoder := json.NewDecoder(request.Body)
		decoder.UseNumber()
		if err := decoder.Decode(&captured); err != nil {
			t.Error(err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{
			"id":"resp_safe","object":"response","status":"completed","model":"upstream-snapshot",
			"output":[{"id":"fc_new","type":"function_call","status":"completed",
			"call_id":"call_new","name":"read_file","arguments":"{\"path\":\"README.md\"}"}],
			"usage":{"input_tokens":11,"output_tokens":4,"total_tokens":15}
		}`))
	}))
	defer server.Close()
	provider := newTestResponsesProvider(t, server.URL+"/v1/responses")
	response, err := provider.Chat(t.Context(), ChatRequest{
		Messages: []Message{
			{Role: "user", Content: "inspect"},
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_old", Name: "read_file",
				Arguments: json.RawMessage(`{"path":"go.mod"}`)}}},
			{Role: "user", ToolResults: []ToolResult{{ToolCallID: "call_old",
				Content: `{"content":"module traverse"}`}}},
		},
		Tools: []ToolSpec{{Name: "read_file", Description: "Read a file",
			Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)}},
		JSONMode: true, MaxTokens: 77,
	})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/v1/responses" || captured["store"] != false || captured["stream"] != false ||
		captured["max_output_tokens"] != json.Number("77") || captured["model"] != "model-local" {
		t.Fatalf("unexpected Responses request path=%q body=%#v", path, captured)
	}
	input, ok := captured["input"].([]any)
	if !ok || len(input) != 3 || input[1].(map[string]any)["type"] != "function_call" ||
		input[2].(map[string]any)["type"] != "function_call_output" {
		t.Fatalf("tool loop input mapping=%#v", captured["input"])
	}
	tools, ok := captured["tools"].([]any)
	if !ok || len(tools) != 1 || tools[0].(map[string]any)["type"] != "function" {
		t.Fatalf("Responses tool mapping=%#v", captured["tools"])
	}
	if _, present := captured["include"]; present {
		t.Fatalf("native search/include was installed implicitly: %#v", captured)
	}
	if response.ResponseID != "resp_safe" || response.Model != "model-local" ||
		response.Provider != "responses-test" || response.Usage.TotalTokens != 15 ||
		len(response.ToolCalls) != 1 || response.ToolCalls[0].ID != "call_new" ||
		response.ToolCalls[0].Name != "read_file" {
		t.Fatalf("unexpected Responses normalization: %#v", response)
	}
}

func TestOpenAIResponsesProviderStreamsTextAndFunctionItems(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter,
		request *http.Request,
	) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if body["stream"] != true || request.Header.Get("Accept") != "text/event-stream" {
			t.Errorf("stream protocol request=%#v accept=%q", body, request.Header.Get("Accept"))
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		flusher := writer.(http.Flusher)
		write := func(payload string) {
			_, _ = fmt.Fprintf(writer, "data: %s\n\n", payload)
			flusher.Flush()
		}
		write(`{"type":"response.created","response":{"id":"resp_stream","object":"response","status":"in_progress","model":"upstream-snapshot"}}`)
		write(`{"type":"response.output_item.added","item":{"id":"reason_1","type":"reasoning","status":"in_progress"}}`)
		write(`{"type":"response.reasoning_summary_text.delta","item_id":"reason_1","delta":"private summary"}`)
		write(`{"type":"response.reasoning_summary_text.done","item_id":"reason_1"}`)
		write(`{"type":"response.output_item.done","item":{"id":"reason_1","type":"reasoning","status":"completed"}}`)
		write(`{"type":"response.output_item.added","item":{"id":"msg_1","type":"message","status":"in_progress","role":"assistant"}}`)
		write(`{"type":"response.output_text.delta","item_id":"msg_1","delta":"done"}`)
		write(`{"type":"response.output_item.done","item":{"id":"msg_1","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"done"}]}}`)
		write(`{"type":"response.output_item.added","item":{"id":"fc_1","type":"function_call","status":"in_progress","call_id":"call_1","name":"read_file","arguments":""}}`)
		write(`{"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"{\"path\":"}`)
		write(`{"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"\"README.md\"}"}`)
		write(`{"type":"response.function_call_arguments.done","item_id":"fc_1","name":"read_file","arguments":"{\"path\":\"README.md\"}"}`)
		write(`{"type":"response.output_item.done","item":{"id":"fc_1","type":"function_call","status":"completed","call_id":"call_1","name":"read_file","arguments":"{\"path\":\"README.md\"}"}}`)
		write(`{"type":"response.completed","response":{"id":"resp_stream","object":"response","status":"completed","model":"upstream-snapshot","usage":{"input_tokens":9,"output_tokens":3,"total_tokens":12}}}`)
	}))
	defer server.Close()
	provider := newTestResponsesProvider(t, server.URL)
	chunks, err := provider.StreamChat(t.Context(), ChatRequest{
		Messages: []Message{{Role: "user", Content: "inspect"}},
		Tools:    []ToolSpec{{Name: "read_file", Parameters: json.RawMessage(`{"type":"object"}`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	var final *ChatChunk
	var events []StreamEventType
	for chunk := range chunks {
		if chunk.Err != nil {
			t.Fatal(chunk.Err)
		}
		_, _ = text.WriteString(chunk.Text)
		for _, event := range chunk.Events {
			events = append(events, event.Type)
		}
		if chunk.Done {
			copy := chunk
			final = &copy
		}
	}
	want := []StreamEventType{StreamResponseStarted,
		StreamOutputItemStarted, StreamTextDelta, StreamOutputItemCompleted,
		StreamOutputItemStarted, StreamToolCallStarted,
		StreamToolArgumentDelta, StreamToolArgumentDelta,
		StreamToolCallCompleted, StreamOutputItemCompleted, StreamResponseCompleted}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("Responses stream events=%v want=%v", events, want)
	}
	if text.String() != "done" || final == nil || final.Model != "model-local" ||
		final.Usage == nil || final.Usage.TotalTokens != 12 || len(final.ToolCalls) != 1 ||
		string(final.ToolCalls[0].Arguments) != `{"path":"README.md"}` {
		t.Fatalf("unexpected Responses stream text=%q final=%#v", text.String(), final)
	}
}

func TestOpenAIResponsesProviderRejectsIncompleteAndMalformedOutputs(t *testing.T) {
	for name, payload := range map[string]string{
		"incomplete":     `{"id":"resp_x","object":"response","status":"incomplete","model":"m","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`,
		"unknown output": `{"id":"resp_x","object":"response","status":"completed","model":"m","output":[{"id":"x","type":"web_search_call","status":"completed"}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`,
		"bad usage":      `{"id":"resp_x","object":"response","status":"completed","model":"m","output":[{"id":"x","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":3}}`,
		"duplicate item": `{"id":"resp_x","object":"response","status":"completed","model":"m","output":[{"id":"same","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"ok"}]},{"id":"same","type":"function_call","status":"completed","call_id":"call_x","name":"read_file","arguments":"{}"}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				_, _ = writer.Write([]byte(payload))
			}))
			defer server.Close()
			provider := newTestResponsesProvider(t, server.URL)
			if _, err := provider.Chat(t.Context(), ChatRequest{
				Messages: []Message{{Role: "user", Content: "hello"}},
			}); err == nil || ProviderErrorKind(err) != OutcomeInvalidResponse {
				t.Fatalf("unsafe Responses output was accepted: %v", err)
			}
		})
	}
}

func TestOpenAIResponsesProviderToolSpecificationBoundary(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		mode := "chat"
		if streaming {
			mode = "stream"
		}
		t.Run(mode, func(t *testing.T) {
			t.Run("limit reaches HTTP", func(t *testing.T) {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter,
					request *http.Request,
				) {
					requests.Add(1)
					var body openAIResponsesRequest
					if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
						t.Error(err)
						writer.WriteHeader(http.StatusBadRequest)
						return
					}
					if len(body.Tools) != MaxProviderToolSpecs {
						t.Errorf("advertised tool count = %d, want %d",
							len(body.Tools), MaxProviderToolSpecs)
					}
					if streaming {
						writeSuccessfulResponsesStream(t, writer)
						return
					}
					writer.Header().Set("Content-Type", "application/json")
					_, _ = writer.Write([]byte(`{
						"id":"resp_tool_limit","object":"response","status":"completed","model":"upstream-snapshot",
						"output":[{"id":"msg_limit","type":"message","status":"completed","role":"assistant",
						"content":[{"type":"output_text","text":"ok"}]}],
						"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}
					}`))
				}))
				defer server.Close()

				provider := newTestResponsesProvider(t, server.URL)
				request := ChatRequest{
					Messages: []Message{{Role: "user", Content: "inspect"}},
					Tools:    responsesToolSpecifications(MaxProviderToolSpecs),
				}
				if streaming {
					chunks, err := provider.StreamChat(t.Context(), request)
					if err != nil {
						t.Fatal(err)
					}
					for chunk := range chunks {
						if chunk.Err != nil {
							t.Fatal(chunk.Err)
						}
					}
				} else if _, err := provider.Chat(t.Context(), request); err != nil {
					t.Fatal(err)
				}
				if requests.Load() != 1 {
					t.Fatalf("HTTP requests = %d, want 1", requests.Load())
				}
			})

			t.Run("over limit is rejected locally", func(t *testing.T) {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
					requests.Add(1)
				}))
				defer server.Close()

				provider := newTestResponsesProvider(t, server.URL)
				request := ChatRequest{
					Messages: []Message{{Role: "user", Content: "inspect"}},
					Tools:    responsesToolSpecifications(MaxProviderToolSpecs + 1),
				}
				var err error
				if streaming {
					_, err = provider.StreamChat(t.Context(), request)
				} else {
					_, err = provider.Chat(t.Context(), request)
				}
				if err == nil || ProviderErrorKind(err) != OutcomePermanent ||
					ProviderErrorReason(err) != ProviderFailureProtocolIncompatible {
					t.Fatalf("over-limit advertised tools error = %#v", err)
				}
				if requests.Load() != 0 {
					t.Fatalf("over-limit request reached HTTP %d time(s)", requests.Load())
				}
			})
		})
	}
}

func TestOpenAIResponsesProviderRejectsSeventeenReturnedToolCalls(t *testing.T) {
	t.Run("response", func(t *testing.T) {
		var requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			requests.Add(1)
			output := make([]openAIResponsesOutputItem, MaxProviderToolCalls+1)
			for index := range output {
				output[index] = responsesToolOutputItem(index, "completed")
			}
			inputTokens, outputTokens, totalTokens := 1, 1, 2
			_ = json.NewEncoder(writer).Encode(openAIResponsesResponse{
				ID: "resp_too_many_calls", Object: "response", Status: "completed",
				Model: "upstream-snapshot", Output: output,
				Usage: &openAIResponsesUsage{InputTokens: &inputTokens,
					OutputTokens: &outputTokens, TotalTokens: &totalTokens},
			})
		}))
		defer server.Close()

		provider := newTestResponsesProvider(t, server.URL)
		_, err := provider.Chat(t.Context(), ChatRequest{
			Messages: []Message{{Role: "user", Content: "inspect"}},
		})
		if err == nil || ProviderErrorKind(err) != OutcomeInvalidResponse ||
			ProviderErrorReason(err) != ProviderFailureProtocolIncompatible {
			t.Fatalf("17-call Responses error = %#v", err)
		}
		if requests.Load() != 1 {
			t.Fatalf("HTTP requests = %d, want 1", requests.Load())
		}
	})

	t.Run("stream", func(t *testing.T) {
		var requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			requests.Add(1)
			writer.Header().Set("Content-Type", "text/event-stream")
			flusher := writer.(http.Flusher)
			writeResponsesSSE(t, writer, flusher, map[string]any{
				"type": "response.created",
				"response": map[string]any{"id": "resp_too_many_stream_calls",
					"object": "response", "status": "in_progress", "model": "upstream-snapshot"},
			})
			for index := 0; index <= MaxProviderToolCalls; index++ {
				started := responsesToolOutputItem(index, "in_progress")
				writeResponsesSSE(t, writer, flusher, map[string]any{
					"type": "response.output_item.added", "item": started,
				})
				if index == MaxProviderToolCalls {
					return
				}
				completed := responsesToolOutputItem(index, "completed")
				writeResponsesSSE(t, writer, flusher, map[string]any{
					"type": "response.output_item.done", "item": completed,
				})
			}
		}))
		defer server.Close()

		provider := newTestResponsesProvider(t, server.URL)
		chunks, err := provider.StreamChat(t.Context(), ChatRequest{
			Messages: []Message{{Role: "user", Content: "inspect"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		var streamErr error
		for chunk := range chunks {
			if chunk.Err != nil {
				streamErr = chunk.Err
			}
		}
		if streamErr == nil || ProviderErrorKind(streamErr) != OutcomeInvalidResponse ||
			ProviderErrorReason(streamErr) != ProviderFailureProtocolIncompatible {
			t.Fatalf("17-call Responses stream error = %#v", streamErr)
		}
		if requests.Load() != 1 {
			t.Fatalf("HTTP requests = %d, want 1", requests.Load())
		}
	})
}

func responsesToolSpecifications(count int) []ToolSpec {
	tools := make([]ToolSpec, count)
	for index := range tools {
		tools[index] = ToolSpec{
			Name:       fmt.Sprintf("tool_%03d", index),
			Parameters: json.RawMessage(`{"type":"object"}`),
		}
	}
	return tools
}

func responsesToolOutputItem(index int, status string) openAIResponsesOutputItem {
	return openAIResponsesOutputItem{
		ID: fmt.Sprintf("fc_%03d", index), Type: "function_call", Status: status,
		CallID: fmt.Sprintf("call_%03d", index), Name: fmt.Sprintf("tool_%03d", index),
		Arguments: `{}`,
	}
}

func writeSuccessfulResponsesStream(t *testing.T, writer http.ResponseWriter) {
	t.Helper()
	writer.Header().Set("Content-Type", "text/event-stream")
	flusher := writer.(http.Flusher)
	writeResponsesSSE(t, writer, flusher, map[string]any{
		"type": "response.created",
		"response": map[string]any{"id": "resp_tool_limit", "object": "response",
			"status": "in_progress", "model": "upstream-snapshot"},
	})
	writeResponsesSSE(t, writer, flusher, map[string]any{
		"type": "response.output_item.added",
		"item": map[string]any{"id": "msg_limit", "type": "message",
			"status": "in_progress", "role": "assistant"},
	})
	writeResponsesSSE(t, writer, flusher, map[string]any{
		"type": "response.output_item.done",
		"item": map[string]any{"id": "msg_limit", "type": "message", "status": "completed",
			"role": "assistant", "content": []map[string]any{{"type": "output_text", "text": "ok"}}},
	})
	writeResponsesSSE(t, writer, flusher, map[string]any{
		"type": "response.completed",
		"response": map[string]any{"id": "resp_tool_limit", "object": "response",
			"status": "completed", "model": "upstream-snapshot",
			"usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}},
	})
}

func writeResponsesSSE(t *testing.T, writer http.ResponseWriter, flusher http.Flusher, payload any) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Error(err)
		return
	}
	_, _ = fmt.Fprintf(writer, "data: %s\n\n", raw)
	flusher.Flush()
}

func newTestResponsesProvider(t *testing.T, endpoint string) *OpenAIResponsesProvider {
	t.Helper()
	provider, err := NewOpenAIResponsesProvider(OpenAIResponsesConfig{
		Name: "responses-test", BaseURL: endpoint, APIKey: "test-secret",
		DefaultModel: "model-local",
	})
	if err != nil {
		t.Fatal(err)
	}
	return provider
}
