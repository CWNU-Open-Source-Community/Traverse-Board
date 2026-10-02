package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

func kimiHTTPFixtureProvider(t *testing.T, handler http.Handler, endpoint string) *OpenAICompatibleProvider {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	target, _ := url.Parse(server.URL)
	p, err := NewOpenAICompatibleProvider(OpenAICompatibleConfig{Name: "kimi-http", BaseURL: endpoint,
		DefaultModel: "route-alias", APIKey: "fixture-only", Runtime: kimiReplayTestRuntime{wireModel: "kimi-k3", digest: "a"},
		HTTPClient: &http.Client{Transport: geminiTestTransport(func(request *http.Request) (*http.Response, error) {
			if request.URL.String() != endpoint {
				t.Error("native endpoint changed")
			}
			copy := request.Clone(request.Context())
			copy.URL.Scheme, copy.URL.Host = target.Scheme, target.Host
			return http.DefaultTransport.RoundTrip(copy)
		})}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func collectKimiHTTPStream(t *testing.T, p *OpenAICompatibleProvider, request ChatRequest) (*ChatResponse, error) {
	t.Helper()
	chunks, err := p.StreamChat(t.Context(), request)
	if err != nil {
		return nil, err
	}
	var text strings.Builder
	var response *ChatResponse
	for chunk := range chunks {
		public, _ := json.Marshal(chunk)
		if bytes.Contains(public, []byte("private-K3")) || strings.Contains(fmt.Sprintf("%+v", chunk), "private-K3") {
			t.Error("native reasoning leaked through chunks, events, or formatting")
		}
		if chunk.Err != nil {
			return nil, chunk.Err
		}
		text.WriteString(chunk.Text)
		if chunk.Done {
			if response != nil {
				t.Error("stream completed twice")
			}
			response = &ChatResponse{Text: text.String(), Model: chunk.Model, Provider: chunk.Provider,
				Usage: *chunk.Usage, ToolCalls: chunk.ToolCalls, Replay: chunk.Replay}
		}
	}
	if response == nil {
		return nil, fmt.Errorf("stream omitted completion")
	}
	return response, nil
}

func writeKimiHTTPStream(w http.ResponseWriter, id, reason, text, finish string, calls []openAIToolCall) {
	w.Header().Set("Content-Type", "text/event-stream")
	emit := func(delta any, stop string, usage bool) {
		choices := []any{}
		if delta != nil {
			choices = append(choices, map[string]any{"index": 0, "delta": delta, "finish_reason": stop})
		}
		event := map[string]any{"id": id, "model": "upstream-k3-snapshot", "choices": choices}
		if usage {
			event["usage"] = map[string]int{"prompt_tokens": 2, "completion_tokens": 3, "total_tokens": 5}
		}
		raw, _ := json.Marshal(event)
		_, _ = fmt.Fprintf(w, "data: %s\r\n\r\n", raw)
	}
	emit(map[string]any{"role": "assistant", "reasoning_content": nil}, "", false)
	// Split by runes so the fixture itself does not cut a UTF-8 sequence.
	runes := []rune(reason)
	for _, fragment := range []string{"", string(runes[:len(runes)/2]), string(runes[len(runes)/2:])} {
		emit(map[string]any{"reasoning_content": fragment, "unknown_private": "discard-me"}, "", false)
	}
	emit(map[string]any{"reasoning_content": nil, "content": text}, "", false)
	for index, call := range calls {
		middle := len(call.Function.Arguments) / 2
		emit(map[string]any{"tool_calls": []any{map[string]any{"index": index, "id": call.ID, "type": "function",
			"function": map[string]string{"name": call.Function.Name, "arguments": call.Function.Arguments[:middle]}}}}, "", false)
		emit(map[string]any{"tool_calls": []any{map[string]any{"index": index,
			"function": map[string]string{"arguments": call.Function.Arguments[middle:]}}}}, "", false)
	}
	emit(map[string]any{"reasoning_content": nil}, finish, false)
	emit(nil, "", true)
	_, _ = io.WriteString(w, "data: [DONE]\r\n\r\n")
}

func TestKimiActualHTTPParallelSequentialRetryAndOrdinaryFollowup(t *testing.T) {
	for _, endpoint := range []string{kimiReplayTestEndpoint, "https://api.moonshot.cn/v1/chat/completions"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", endpoint, stream), func(t *testing.T) {
				var mu sync.Mutex
				attempts, successes := 0, 0
				var retry []byte
				reasons := []string{" private-K3-A \n雪+/= ", " private-K3-B \n ", " private-K3-final \n ", " private-K3-followup "}
				p := kimiHTTPFixtureProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					defer mu.Unlock()
					attempts++
					raw, _ := io.ReadAll(r.Body)
					var body openAIChatRequest
					if json.Unmarshal(raw, &body) != nil || body.Model != "kimi-k3" || body.Stream != stream {
						t.Error("request escaped actual mapped K3 scope")
						w.WriteHeader(400)
						return
					}
					if bytes.Contains(raw, []byte("discard-me")) || bytes.Contains(raw, []byte("durable-")) {
						t.Error("unknown extension or durable identity reached native history")
					}
					if successes > 0 {
						if len(body.Messages) < 4 || len(body.Messages[1].ToolCalls) != 2 ||
							body.Messages[1].ToolCalls[0].ID != "native-a" || body.Messages[1].ToolCalls[1].ID != "native-parallel" ||
							string(body.Messages[1].ReasoningContent) != strconvJSON(reasons[0]) ||
							body.Messages[2].ToolCallID != "native-a" || body.Messages[3].ToolCallID != "native-parallel" {
							t.Error("first native assistant/result batch changed")
						}
					}
					if attempts == 2 {
						retry = append([]byte(nil), raw...)
						w.WriteHeader(503)
						return
					}
					if attempts == 3 && !bytes.Equal(raw, retry) {
						t.Error("retry changed native history")
					}
					if successes > 1 && (len(body.Messages) < 6 || len(body.Messages[4].ToolCalls) != 1 || string(body.Messages[4].ReasoningContent) != strconvJSON(reasons[1]) ||
						body.Messages[4].ToolCalls[0].ID != "native-b" || body.Messages[5].ToolCallID != "native-b") {
						t.Error("second native assistant/result batch changed")
					}
					if successes == 3 && (len(body.Messages) != 8 || string(body.Messages[6].ReasoningContent) != strconvJSON(reasons[2]) ||
						string(body.Messages[6].ContentRaw) != `" final answer "` || body.Messages[7].Role != "user" || len(body.Messages[6].ToolCalls) != 0) {
						t.Error("ordinary answer lost private history at the next user turn")
					}
					index := successes
					successes++
					var calls []openAIToolCall
					finish, text := "stop", " final answer "
					if index < 2 {
						finish, text = "tool_calls", " tool explanation "
						calls = []openAIToolCall{{ID: "native-a", Type: "function", Function: openAIFunctionCall{Name: "echo", Arguments: `{"city":"Paris"}`}}}
						if index == 0 {
							calls = append(calls, openAIToolCall{ID: "native-parallel", Type: "function", Function: openAIFunctionCall{Name: "echo", Arguments: `{"city":"Rome"}`}})
						} else {
							calls[0].ID = "native-b"
						}
					}
					if stream {
						writeKimiHTTPStream(w, fmt.Sprintf("native-response-%d", index), reasons[index], text, finish, calls)
						return
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"id": fmt.Sprintf("native-response-%d", index), "model": "upstream-k3-snapshot",
						"choices": []any{map[string]any{"index": 0, "finish_reason": finish, "message": map[string]any{
							"role": "assistant", "content": text, "tool_calls": calls, "reasoning_content": reasons[index], "unknown_private": "discard-me"}}},
						"usage": map[string]int{"prompt_tokens": 2, "completion_tokens": 3, "total_tokens": 5}})
				}), endpoint)
				request := ChatRequest{Messages: []Message{{Role: "user", Content: "original"}}}
				call := func() (*ChatResponse, error) {
					if stream {
						return collectKimiHTTPStream(t, p, request)
					}
					return p.Chat(t.Context(), request)
				}
				for round := 0; round < 4; round++ {
					response, err := call()
					if round == 1 {
						if err == nil {
							t.Fatal("503 did not fail")
						}
						response, err = call()
					}
					if err != nil || !response.Replay.RequiresPrivateAssistantHistory() {
						t.Fatal("native response failed", err)
					}
					for index := range response.ToolCalls {
						response.ToolCalls[index].ID = fmt.Sprintf("durable-%d-%d", round, index)
					}
					response.Replay, err = response.Replay.BindToolCalls(response.ToolCalls)
					if err != nil {
						t.Fatal(err)
					}
					raw, err := response.Replay.Clone().EncodeForStore()
					if err != nil {
						t.Fatal(err)
					}
					response.Replay, err = DecodeProviderReplay(raw)
					if err != nil {
						t.Fatal(err)
					}
					public, _ := json.Marshal(response)
					if bytes.Contains(public, []byte("private-K3")) {
						t.Fatal("ordinary response leaked private reasoning")
					}
					if round < 2 {
						results := make([]ToolResult, len(response.ToolCalls))
						for index, tool := range response.ToolCalls {
							results[index] = ToolResult{ToolCallID: tool.ID, Content: `{"ok":true}`}
						}
						request.Messages = append(request.Messages, Message{Role: "assistant", Content: response.Text, ToolCalls: response.ToolCalls, Replay: response.Replay}, Message{Role: "user", ToolResults: results})
					} else if round == 2 {
						request.Messages = append(request.Messages, Message{Role: "assistant", Content: response.Text, Replay: response.Replay}, Message{Role: "user", Content: "fresh follow-up"})
					}
				}
				mu.Lock()
				defer mu.Unlock()
				if attempts != 5 || successes != 4 {
					t.Fatal("extra HTTP requests", attempts, successes)
				}
			})
		}
	}
}

func TestKimiActualWireUsesExactPrivateKeyAndRejectsUnknownStreamTerminal(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, variant := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%t/variant=%t", stream, variant), func(t *testing.T) {
				p := kimiHTTPFixtureProvider(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					fields := `"reasoning_content":"private-K3-canonical","REASONING_CONTENT":"discard-me"`
					if variant {
						fields = `"Reasoning_Content":"discard-me"`
					}
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = fmt.Fprintf(w, "data: {\"id\":\"native\",\"model\":\"kimi-k3\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"answer\",%s},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":3,\"total_tokens\":5}}\n\ndata: [DONE]\n\n", fields)
					} else {
						_, _ = fmt.Fprintf(w, `{"id":"native","model":"kimi-k3","choices":[{"index":0,"message":{"content":"answer",%s},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`, fields)
					}
				}), kimiReplayTestEndpoint)
				request := ChatRequest{Messages: []Message{{Role: "user", Content: "hello"}}}
				var response *ChatResponse
				var err error
				if stream {
					response, err = collectKimiHTTPStream(t, p, request)
				} else {
					response, err = p.Chat(t.Context(), request)
				}
				if err != nil {
					t.Fatal(err)
				}
				wire, err := p.kimiMessages([]Message{{Role: "assistant", Content: response.Text, Replay: response.Replay}}, "route-alias", "kimi-k3")
				if err != nil || len(wire) != 1 {
					t.Fatal(err)
				}
				if variant && len(wire[0].ReasoningContent) != 0 || !variant && string(wire[0].ReasoningContent) != `"private-K3-canonical"` {
					t.Fatal("unknown case alias overwrote or fabricated private reasoning")
				}
			})
		}
	}
	p := kimiHTTPFixtureProvider(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeKimiHTTPStream(w, "unknown-finish", "private-K3-invalid", "answer", "unknown-terminal", nil)
	}), kimiReplayTestEndpoint)
	if response, err := collectKimiHTTPStream(t, p, ChatRequest{Messages: []Message{{Role: "user", Content: "hello"}}}); err == nil || response != nil {
		t.Fatal("unknown stream terminal published private history")
	}
}

func TestKimiRejectedReplayRetainsOnlyValidatedUsage(t *testing.T) {
	p := kimiHTTPFixtureProvider(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"id":"native-rejected","model":"kimi-k3","choices":[{"index":0,"message":{"content":"answer","reasoning_content":{"unexpected":"private-K3-invalid"}},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`)
	}), kimiReplayTestEndpoint)
	request := ChatRequest{Messages: []Message{{Role: "user", Content: "hello"}}}
	response, err := p.Chat(t.Context(), request)
	if err == nil || response == nil || response.Replay != nil || response.Usage.TotalTokens != 5 {
		t.Fatal("rejected native replay lost received usage or exposed private state")
	}
	p = kimiHTTPFixtureProvider(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeKimiHTTPStream(w, "native-rejected", "private-K3-rejected", "answer", "unknown-terminal", nil)
	}), kimiReplayTestEndpoint)
	chunks, err := p.StreamChat(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	var failed *ChatChunk
	for chunk := range chunks {
		if chunk.Err != nil {
			copy := chunk
			failed = &copy
		}
	}
	if failed == nil || failed.Done || failed.Replay != nil || failed.Usage == nil || failed.Usage.TotalTokens != 5 {
		t.Fatal("rejected stream replay lost its validated usage")
	}
	public, _ := json.Marshal(failed)
	if bytes.Contains(public, []byte("private-K3")) {
		t.Fatal("failure accounting leaked native state")
	}
}

func TestKimiNativeToolIdentityCannotBeSilentlyTrimmed(t *testing.T) {
	for _, stream := range []bool{false, true} {
		p := kimiHTTPFixtureProvider(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls := []openAIToolCall{{ID: " native-id ", Type: "function", Function: openAIFunctionCall{Name: "echo", Arguments: `{}`}}}
			if stream {
				writeKimiHTTPStream(w, "native-identity", "private-K3-identity", "", "tool_calls", calls)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "native-identity", "model": "kimi-k3", "choices": []any{map[string]any{
				"index": 0, "finish_reason": "tool_calls", "message": map[string]any{"tool_calls": calls, "reasoning_content": "private-K3-identity"}}},
				"usage": map[string]int{"prompt_tokens": 2, "completion_tokens": 3, "total_tokens": 5}})
		}), kimiReplayTestEndpoint)
		request := ChatRequest{Messages: []Message{{Role: "user", Content: "hello"}}}
		if stream {
			if _, err := collectKimiHTTPStream(t, p, request); err == nil {
				t.Fatal("stream accepted a changed native tool ID")
			}
		} else {
			if response, err := p.Chat(t.Context(), request); err == nil || response != nil && response.Replay != nil {
				t.Fatal("Chat accepted a changed native tool ID")
			}
		}
	}
}
