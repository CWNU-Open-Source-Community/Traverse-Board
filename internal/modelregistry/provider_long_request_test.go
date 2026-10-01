package modelregistry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/llm"
)

// This is deliberately a real wall-clock integration test. Run fast checks
// with -short, and run this test separately to prove the former 60s ceiling is
// gone on the production configuration/Registry/Router/HTTP path.
func TestConfiguredProviderStreamCompletesBeyondOneMinute(t *testing.T) {
	if testing.Short() {
		t.Skip("real HTTP integration requires more than one minute")
	}
	for _, transport := range []string{ProviderTransportOpenAIChatCompletions,
		ProviderTransportOpenAIResponses, ProviderTransportAnthropicMessages, llm.HarnessTransportOllamaChat} {
		t.Run(transport, func(t *testing.T) {
			t.Parallel()
			const count = 65
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				if body["stream"] != true || body["request_timeout_seconds"] != nil {
					t.Error("stream request did not preserve local timeout policy")
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if transport == llm.HarnessTransportOllamaChat {
					w.Header().Set("Content-Type", "application/x-ndjson")
				}
				writeTimeoutStreamStart(w, transport)
				w.(http.Flusher).Flush()
				ticker := time.NewTicker(time.Second)
				defer ticker.Stop()
				for n := 0; n < count; n++ {
					select {
					case <-r.Context().Done():
						return
					case <-ticker.C:
					}
					writeTimeoutStreamDelta(w, transport)
					w.(http.Flusher).Flush()
				}
				writeTimeoutStreamEnd(w, transport, count)
				w.(http.Flusher).Flush()
			}))
			defer server.Close()
			registry := New(func(name string) (string, bool) {
				if transport != llm.HarnessTransportOllamaChat {
					return "", false
				}
				values := map[string]string{"CYBERAGENT_OLLAMA_BASE_URL": server.URL,
					"CYBERAGENT_OLLAMA_MODEL": "acme-code", "CYBERAGENT_OLLAMA_TIMEOUT_SECONDS": "120"}
				value, found := values[name]
				return value, found
			})
			ref := llm.ModelRef{Provider: "ollama", Model: "acme-code"}
			if transport != llm.HarnessTransportOllamaChat {
				definition := validCustomDefinition(server.URL)
				definition.Transport = transport
				definition.AdvancedConfig = json.RawMessage(`{"request_timeout_seconds":120}`)
				settings := routeSettings{ProviderDefinitionsSettingKey: providerDefinitionSetting(t, definition, 1)}
				if err := registry.LoadRouteSettings(t.Context(), settings); err != nil {
					t.Fatal(err)
				}
				ref.Provider = definition.ID
			}
			started := time.Now()
			chunks, err := registry.Router().StreamChatModelRef(t.Context(), ref, llm.ChatRequest{
				Messages: []llm.Message{{Role: "user", Content: "fixture"}}, MaxTokens: 128,
			})
			if err != nil {
				t.Fatal(err)
			}
			var text strings.Builder
			var usage *llm.Usage
			done, terminals := 0, 0
			for chunk := range chunks {
				if chunk.Err != nil {
					t.Fatalf("long configured stream failed after %s: %v", time.Since(started), chunk.Err)
				}
				text.WriteString(chunk.Text)
				if chunk.Done {
					done++
					usage = chunk.Usage
				}
				for _, event := range chunk.Events {
					if event.Type == llm.StreamResponseCompleted {
						terminals++
					}
				}
			}
			elapsed := time.Since(started)
			if elapsed <= time.Minute || done != 1 || terminals != 1 || text.String() != strings.Repeat("x", count) ||
				usage == nil || usage.TotalTokens != count+2 {
				t.Fatalf("long stream did not complete exactly once: elapsed=%s done=%d terminals=%d bytes=%d usage=%+v",
					elapsed, done, terminals, text.Len(), usage)
			}
			t.Logf("production config completed one terminal after %s, total_tokens=%d", elapsed, usage.TotalTokens)
		})
	}
}

func TestConfiguredProviderStreamTimeoutIsTotalNotIdle(t *testing.T) {
	for _, transport := range []string{ProviderTransportOpenAIChatCompletions,
		ProviderTransportOpenAIResponses, ProviderTransportAnthropicMessages, llm.HarnessTransportOllamaChat} {
		t.Run(transport, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				writeTimeoutStreamStart(w, transport)
				w.(http.Flusher).Flush()
				ticker := time.NewTicker(25 * time.Millisecond)
				defer ticker.Stop()
				for {
					select {
					case <-r.Context().Done():
						return
					case <-ticker.C:
						writeTimeoutStreamDelta(w, transport)
						w.(http.Flusher).Flush()
					}
				}
			}))
			defer server.Close()
			registry := New(func(name string) (string, bool) {
				if transport != llm.HarnessTransportOllamaChat {
					return "", false
				}
				values := map[string]string{"CYBERAGENT_OLLAMA_BASE_URL": server.URL,
					"CYBERAGENT_OLLAMA_MODEL": "acme-code", "CYBERAGENT_OLLAMA_TIMEOUT_SECONDS": "1"}
				value, found := values[name]
				return value, found
			})
			ref := llm.ModelRef{Provider: "ollama", Model: "acme-code"}
			if transport != llm.HarnessTransportOllamaChat {
				definition := validCustomDefinition(server.URL)
				definition.Transport = transport
				definition.AdvancedConfig = json.RawMessage(`{"request_timeout_seconds":1}`)
				if err := registry.LoadRouteSettings(t.Context(), routeSettings{
					ProviderDefinitionsSettingKey: providerDefinitionSetting(t, definition, 1),
				}); err != nil {
					t.Fatal(err)
				}
				ref.Provider = definition.ID
			}
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			chunks, err := registry.Router().StreamChatModelRef(ctx, ref, llm.ChatRequest{
				Messages: []llm.Message{{Role: "user", Content: "fixture"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			var failure *llm.ProviderError
			bytes := 0
			for chunk := range chunks {
				bytes += len(chunk.Text)
				if chunk.Done || len(chunk.ToolCalls) != 0 {
					t.Fatal("timed-out partial response became an accepted result")
				}
				if chunk.Err != nil {
					failure = llm.NormalizeProviderError(ref.Provider, chunk.Err)
				}
			}
			if bytes < 2 || ctx.Err() != nil || failure == nil || failure.Kind != llm.OutcomeRetryable ||
				failure.Reason != llm.ProviderFailureNetwork || errors.Is(failure, context.DeadlineExceeded) {
				t.Fatalf("continuous deltas restarted total timeout or lost classification: bytes=%d ctx=%v failure=%+v",
					bytes, ctx.Err(), failure)
			}
		})
	}
}

func timeoutSSE(w io.Writer, payload string) {
	_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
}

func writeTimeoutStreamStart(w io.Writer, transport string) {
	switch transport {
	case ProviderTransportOpenAIChatCompletions:
		timeoutSSE(w, `{"id":"chat_timeout","model":"acme-code","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`)
	case ProviderTransportOpenAIResponses:
		timeoutSSE(w, `{"type":"response.created","response":{"id":"resp_timeout","object":"response","status":"in_progress","model":"acme-code"}}`)
		timeoutSSE(w, `{"type":"response.output_item.added","item":{"id":"msg_timeout","type":"message","status":"in_progress","role":"assistant"}}`)
	case ProviderTransportAnthropicMessages:
		timeoutSSE(w, `{"type":"message_start","message":{"id":"msg_timeout","model":"acme-code","usage":{"input_tokens":2,"output_tokens":0}}}`)
		timeoutSSE(w, `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
	}
}

func writeTimeoutStreamDelta(w io.Writer, transport string) {
	switch transport {
	case ProviderTransportOpenAIChatCompletions:
		timeoutSSE(w, `{"id":"chat_timeout","model":"acme-code","choices":[{"index":0,"delta":{"content":"x"},"finish_reason":null}]}`)
	case ProviderTransportOpenAIResponses:
		timeoutSSE(w, `{"type":"response.output_text.delta","item_id":"msg_timeout","delta":"x"}`)
	case ProviderTransportAnthropicMessages:
		timeoutSSE(w, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"x"}}`)
	case llm.HarnessTransportOllamaChat:
		_, _ = io.WriteString(w, `{"model":"acme-code","message":{"role":"assistant","content":"x"},"done":false}`+"\n")
	}
}

func writeTimeoutStreamEnd(w io.Writer, transport string, count int) {
	switch transport {
	case ProviderTransportOpenAIChatCompletions:
		timeoutSSE(w, fmt.Sprintf(`{"id":"chat_timeout","model":"acme-code","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":%d,"total_tokens":%d}}`, count, count+2))
		timeoutSSE(w, "[DONE]")
	case ProviderTransportOpenAIResponses:
		item := map[string]any{"id": "msg_timeout", "type": "message", "status": "completed", "role": "assistant",
			"content": []map[string]string{{"type": "output_text", "text": strings.Repeat("x", count)}}}
		done, _ := json.Marshal(map[string]any{"type": "response.output_item.done", "item": item})
		timeoutSSE(w, string(done))
		completed, _ := json.Marshal(map[string]any{"type": "response.completed", "response": map[string]any{
			"id": "resp_timeout", "object": "response", "status": "completed", "model": "acme-code",
			"output": []any{item}, "usage": map[string]int{"input_tokens": 2, "output_tokens": count, "total_tokens": count + 2},
		}})
		timeoutSSE(w, string(completed))
	case ProviderTransportAnthropicMessages:
		timeoutSSE(w, `{"type":"content_block_stop","index":0}`)
		timeoutSSE(w, fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":%d}}`, count))
		timeoutSSE(w, `{"type":"message_stop"}`)
	case llm.HarnessTransportOllamaChat:
		_, _ = fmt.Fprintf(w, `{"model":"acme-code","message":{"role":"assistant","content":""},"done":true,"done_reason":"stop","prompt_eval_count":2,"eval_count":%d}`+"\n", count)
	}
}
