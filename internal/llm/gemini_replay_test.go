package llm

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

const geminiTestEndpoint = "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions"
const geminiTestModel = "gemini-3.7-flash"

type geminiTestTransport func(*http.Request) (*http.Response, error)

func (f geminiTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type geminiTestRuntime struct {
	imageTestRuntime
	wireModel string
	digest    string
}

func (r geminiTestRuntime) MapModel(string) (string, error) { return r.wireModel, nil }
func (r geminiTestRuntime) BindingDigest() string           { return strings.Repeat(r.digest, 64) }

func geminiFixtureProvider(t *testing.T, handler http.Handler, runtime HTTPProviderRuntime, configuredEndpoints ...string) *OpenAICompatibleProvider {
	t.Helper()
	endpoint := geminiTestEndpoint
	if len(configuredEndpoints) != 0 {
		endpoint = configuredEndpoints[0]
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	target, _ := url.Parse(server.URL)
	provider, err := NewOpenAICompatibleProvider(OpenAICompatibleConfig{Name: "gemini-test", BaseURL: endpoint,
		APIKey: "fixture-only", DefaultModel: "route-alias", Runtime: runtime,
		HTTPClient: &http.Client{Transport: geminiTestTransport(func(request *http.Request) (*http.Response, error) {
			if request.URL.String() != geminiTestEndpoint {
				t.Errorf("Google endpoint gained a duplicate suffix: %s", request.URL)
			}
			copy := request.Clone(request.Context())
			copy.URL.Scheme, copy.URL.Host = target.Scheme, target.Host
			return http.DefaultTransport.RoundTrip(copy)
		})}})
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func TestGeminiReplayActualHTTPParallelSequentialAndRetry(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			var mu sync.Mutex
			var retryBody []byte
			attempts, successful := 0, 0
			signatures := []string{"private-Gemini-A+/= \n雪", "private-Gemini-B+/= \n"}
			messageSignatures := []string{"private-message-A \n", "private-message-B \n"}
			provider := geminiFixtureProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				attempts++
				raw, _ := io.ReadAll(r.Body)
				var body openAIChatRequest
				if json.Unmarshal(raw, &body) != nil || body.Model != geminiTestModel || body.Stream != stream {
					t.Error("route alias did not map to the exact wire model")
					w.WriteHeader(400)
					return
				}
				if len(body.Messages) > 1 {
					first := body.Messages[1]
					if first.Role != "assistant" || len(first.ToolCalls) != 2 ||
						first.ToolCalls[0].ID != "native-a" || first.ToolCalls[1].ID != "native-parallel" ||
						!reflect.DeepEqual(first.ToolCalls[0].ExtraContent, geminiExtraContent(signatures[0])) ||
						len(first.ToolCalls[1].ExtraContent) != 0 ||
						!reflect.DeepEqual(first.ExtraContent, geminiExtraContent(messageSignatures[0])) ||
						body.Messages[2].Role != "tool" || body.Messages[2].ToolCallID != "native-a" ||
						body.Messages[3].Role != "tool" || body.Messages[3].ToolCallID != "native-parallel" {
						t.Error("first response signature positions or batch/result pairing changed")
					}
					if strings.Contains(string(raw), "discard-me") || strings.Contains(string(raw), "durable-") {
						t.Error("unknown extensions or application IDs reached the native continuation")
					}
				}
				if attempts == 2 {
					retryBody = append([]byte(nil), raw...)
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				if attempts == 3 && string(raw) != string(retryBody) {
					t.Error("retry changed prior native replay or tool results")
				}
				if len(body.Messages) == 6 {
					second := body.Messages[4]
					if second.ToolCalls[0].ID != "native-b" ||
						!reflect.DeepEqual(second.ToolCalls[0].ExtraContent, geminiExtraContent(signatures[1])) ||
						!reflect.DeepEqual(second.ExtraContent, geminiExtraContent(messageSignatures[1])) ||
						body.Messages[5].Role != "tool" || body.Messages[5].ToolCallID != "native-b" {
						t.Error("second response replaced the first response signature or native identity")
					}
				}
				round := successful
				successful++
				if round == 2 {
					if stream {
						writeGeminiTestStream(w, "native-response-final", nil, "", "", "finished")
					} else {
						_ = json.NewEncoder(w).Encode(map[string]any{"id": "native-response-final", "model": "resolved-upstream-snapshot",
							"choices": []any{map[string]any{"index": 0, "finish_reason": "stop", "message": map[string]string{"role": "assistant", "content": "finished"}}},
							"usage":   map[string]int{"prompt_tokens": 2, "completion_tokens": 3, "total_tokens": 5}})
					}
					return
				}
				calls := []openAIToolCall{{ID: "native-a", Type: "function", Function: openAIFunctionCall{Name: "echo", Arguments: `{"city":"Paris"}`}}}
				if round == 0 {
					calls = append(calls, openAIToolCall{ID: "native-parallel", Type: "function", Function: openAIFunctionCall{Name: "echo", Arguments: `{"city":"London"}`}})
				} else {
					calls[0].ID, calls[0].Function.Arguments = "native-b", `{"city":"Rome"}`
				}
				if stream {
					writeGeminiTestStream(w, fmt.Sprintf("native-response-%d", round), calls, signatures[round], messageSignatures[round], "public text")
				} else {
					calls[0].ExtraContent = json.RawMessage(`{"google":{"thought_signature":` + strconvJSON(signatures[round]) + `,"unknown":"discard-me"},"other":"discard-me"}`)
					_ = json.NewEncoder(w).Encode(map[string]any{"id": fmt.Sprintf("native-response-%d", round), "model": "resolved-upstream-snapshot",
						"choices": []any{map[string]any{"index": 0, "finish_reason": "tool_calls", "message": openAIMessage{Role: "assistant", Content: stringPointer("public text"), ToolCalls: calls, ExtraContent: geminiExtraContent(messageSignatures[round])}}},
						"usage":   map[string]int{"prompt_tokens": 2, "completion_tokens": 3, "total_tokens": 5}})
				}
			}), geminiTestRuntime{wireModel: geminiTestModel, digest: "a"})
			request := ChatRequest{Messages: []Message{{Role: "user", Content: "continue"}}}
			call := func() (*ChatResponse, error) {
				if !stream {
					return provider.Chat(t.Context(), request)
				}
				chunks, err := provider.StreamChat(t.Context(), request)
				if err != nil {
					return nil, err
				}
				var text strings.Builder
				for chunk := range chunks {
					public, _ := json.Marshal(chunk)
					if strings.Contains(string(public), "private-") || strings.Contains(fmt.Sprintf("%+v", chunk), "private-Gemini") {
						t.Error("private signatures reached chunks, events or diagnostics")
					}
					if chunk.Err != nil {
						return nil, chunk.Err
					}
					text.WriteString(chunk.Text)
					if chunk.Done {
						return &ChatResponse{Text: text.String(), Model: chunk.Model, Provider: chunk.Provider, ToolCalls: chunk.ToolCalls, Replay: chunk.Replay}, nil
					}
				}
				return nil, fmt.Errorf("stream had no terminal chunk")
			}
			for round := 0; round < 2; round++ {
				response, err := call()
				if round == 1 {
					if err == nil {
						t.Fatal("fixture retry failure was not returned")
					}
					response, err = call()
				}
				if err != nil || response.Replay == nil || response.Model != "route-alias" {
					t.Fatalf("signed native response was not accepted: %v", err)
				}
				for index := range response.ToolCalls {
					response.ToolCalls[index].ID = fmt.Sprintf("durable-%d-%d", round, index)
				}
				response.Replay, err = response.Replay.BindToolCalls(response.ToolCalls)
				if err != nil {
					t.Fatal(err)
				}
				encoded, err := response.Replay.EncodeForStore()
				if err != nil || !strings.Contains(string(encoded), "private-Gemini") || strings.Contains(string(encoded), "discard-me") {
					t.Fatal("typed private replay lost native data or retained unknown extensions", err)
				}
				response.Replay, err = DecodeProviderReplay(encoded)
				if err != nil {
					t.Fatal(err)
				}
				again, _ := response.Replay.EncodeForStore()
				if string(encoded) != string(again) {
					t.Fatal("durable replay changed native bytes")
				}
				public, _ := json.Marshal(response)
				if strings.Contains(string(public), "private-") || strings.Contains(fmt.Sprintf("%#v", response.Replay), "private-Gemini") {
					t.Fatal("private replay escaped ordinary serialization")
				}
				results := make([]ToolResult, len(response.ToolCalls))
				for index, tool := range response.ToolCalls {
					results[index] = ToolResult{ToolCallID: tool.ID, Content: `{"ok":true}`}
				}
				request.Messages = append(request.Messages, Message{Role: "assistant", Content: response.Text, ToolCalls: response.ToolCalls, Replay: response.Replay}, Message{Role: "user", ToolResults: results})
			}
			final, err := call()
			if err != nil || final.Text != "finished" || final.Replay != nil {
				t.Fatal("two-step signed continuation failed", err)
			}
			mu.Lock()
			defer mu.Unlock()
			if attempts != 4 || successful != 3 {
				t.Fatalf("unexpected HTTP calls: %d/%d", attempts, successful)
			}
		})
	}
}

func strconvJSON(value string) string { raw, _ := json.Marshal(value); return string(raw) }

func writeGeminiTestStream(w http.ResponseWriter, id string, calls []openAIToolCall, signature, messageSignature, text string) {
	w.Header().Set("Content-Type", "text/event-stream")
	emit := func(delta any, finish string, usage bool) {
		var choices []any
		if delta != nil {
			choices = []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}
		} else {
			choices = []any{}
		}
		event := map[string]any{"id": id, "model": "resolved-upstream-snapshot", "choices": choices}
		if usage {
			event["usage"] = map[string]int{"prompt_tokens": 2, "completion_tokens": 3, "total_tokens": 5}
		}
		raw, _ := json.Marshal(event)
		_, _ = fmt.Fprintf(w, "data: %s\r\n\r\n", raw)
	}
	emit(map[string]any{"role": "assistant", "content": text}, "", false)
	for index, call := range calls {
		middle := len(call.Function.Arguments) / 2
		emit(map[string]any{"tool_calls": []any{map[string]any{"index": index, "id": call.ID, "type": "function", "function": map[string]string{"name": call.Function.Name, "arguments": call.Function.Arguments[:middle]}}}}, "", false)
		emit(map[string]any{"tool_calls": []any{map[string]any{"index": index, "function": map[string]string{"arguments": call.Function.Arguments[middle:]}}}}, "", false)
	}
	finish := "stop"
	if len(calls) != 0 {
		finish = "tool_calls"
		emit(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "extra_content": json.RawMessage(`{"google":{"thought_signature":` + strconvJSON(signature) + `,"other":"discard-me"}}`)}}}, "", false)
		// A repeated complete signature must not be concatenated.
		emit(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "extra_content": geminiExtraContent(signature)}}}, "", false)
	}
	emit(map[string]any{"content": "", "extra_content": geminiExtraContent(messageSignature)}, finish, false)
	emit(nil, "", true)
	_, _ = fmt.Fprint(w, "data: [DONE]\r\n\r\n")
}

func TestGeminiScopeAndEndpointIsolation(t *testing.T) {
	for _, test := range []struct {
		endpoint, model, want string
		scoped                bool
	}{
		{geminiTestEndpoint, geminiTestModel, geminiTestEndpoint, true},
		{"https://generativelanguage.googleapis.com/v1beta/openai", "gemini-3.1-pro-preview", geminiTestEndpoint, true},
		{" https://generativelanguage.googleapis.com/v1beta/openai/// ", geminiTestModel, geminiTestEndpoint, true},
		{geminiTestEndpoint + "/", geminiTestModel, geminiTestEndpoint, true},
		{"https://generativelanguage.googleapis.com:443/v1beta/openai/", geminiTestModel, "https://generativelanguage.googleapis.com:443/v1beta/openai/chat/completions", true},
		{geminiTestEndpoint, "gemini-2.5-pro", geminiTestEndpoint, false},
		{"https://example.test/v1", geminiTestModel, "https://example.test/v1/chat/completions", false},
		{"https://example.test/v1/chat/completions", geminiTestModel, "https://example.test/v1/chat/completions", false},
		{"https://example.test/v1beta/openai/chat/completions", geminiTestModel, "https://example.test/v1beta/openai/chat/completions/v1/chat/completions", false},
		{"http://generativelanguage.googleapis.com/v1beta/openai", geminiTestModel, "http://generativelanguage.googleapis.com/v1beta/openai/v1/chat/completions", false},
		{"https://generativelanguage.googleapis.com.evil.test/v1beta/openai", geminiTestModel, "https://generativelanguage.googleapis.com.evil.test/v1beta/openai/v1/chat/completions", false},
		{geminiTestEndpoint, "gemini-30-pro", geminiTestEndpoint, false},
		{geminiTestEndpoint, "google/gemini-3.1-pro-preview", geminiTestEndpoint, false},
	} {
		t.Run(test.endpoint+test.model, func(t *testing.T) {
			p := &OpenAICompatibleProvider{baseURL: test.endpoint}
			if GeminiThoughtSignatureScope(test.endpoint, test.model) != test.scoped || p.endpoint("/v1/chat/completions") != test.want {
				t.Fatalf("provider scope or endpoint changed: %s", p.endpoint("/v1/chat/completions"))
			}
		})
	}
}

func TestGeminiScopeRejectsOtherEndpointAndWireModelBoundaries(t *testing.T) {
	for _, endpoint := range []string{
		"https://generativelanguage.googleapis.com:8443/v1beta/openai",
		"https://fixture@generativelanguage.googleapis.com/v1beta/openai",
		"https://generativelanguage.googleapis.com/v1beta/openai?key=fixture",
		"https://generativelanguage.googleapis.com/v1beta/openai#fragment",
		"https://generativelanguage.googleapis.com/v1beta/%6fpenai",
		"https://generativelanguage.googleapis.com/v1/openai",
		"https://aiplatform.googleapis.com/v1beta/openai",
	} {
		if GeminiThoughtSignatureScope(endpoint, geminiTestModel) {
			t.Errorf("endpoint escaped the official AI Studio scope: %s", endpoint)
		}
	}
	for _, model := range []string{"GEMINI-3.1-pro-preview", "gemini-3", "gemini-30-pro", "google/gemini-3.1-pro-preview", " gemini-3.1-pro-preview ", "route-alias"} {
		if GeminiThoughtSignatureScope(geminiTestEndpoint, model) {
			t.Errorf("wire model escaped the exact Gemini 3 family: %s", model)
		}
	}
}

func TestGeminiConfiguredEndpointNormalizationActualHTTP(t *testing.T) {
	for _, endpoint := range []string{
		" https://generativelanguage.googleapis.com/v1beta/openai/// ",
		"https://generativelanguage.googleapis.com/v1beta/openai/chat/completions/",
	} {
		t.Run(endpoint, func(t *testing.T) {
			p := geminiFixtureProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1beta/openai/chat/completions" {
					t.Errorf("configured official endpoint produced another HTTP path: %s", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"normalized-response","model":"upstream-snapshot","choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"id":"native-a","type":"function","function":{"name":"echo","arguments":"{}"},"extra_content":{"google":{"thought_signature":"private-tool"}}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`)
			}), geminiTestRuntime{wireModel: geminiTestModel, digest: "a"}, endpoint)
			response, err := p.Chat(t.Context(), ChatRequest{Messages: []Message{{Role: "user", Content: "continue"}}})
			if err != nil || response.Replay == nil {
				t.Fatal("normalized official configuration lost native signed replay", err)
			}
		})
	}
}

func TestGeminiReplayCompletesParallelBatchBeforeNextMessage(t *testing.T) {
	p, replay, calls := geminiBoundTestReplay(t)
	calls = append(calls, ToolCall{ID: "native-b", Name: "echo", Arguments: json.RawMessage(`{"city":"Rome"}`)})
	b := &geminiReplayBuilder{responseID: "parallel-response", tools: map[int]string{0: "private-first-tool"}}
	parallel, err := b.replay(p, "route-alias", geminiTestModel, "upstream-snapshot", "", calls)
	if err != nil {
		t.Fatal(err)
	}
	firstResult := Message{Role: "user", ToolResults: []ToolResult{{ToolCallID: "native-a", Content: `{"ok":true}`}}}
	lastResult := Message{Role: "user", ToolResults: []ToolResult{{ToolCallID: "native-b", Content: `{"ok":true}`}}}
	batch := []Message{{Role: "user", Content: "continue"}, {Role: "assistant", ToolCalls: calls, Replay: parallel}, firstResult, lastResult}
	_, wire, err := p.prepareRequest(ChatRequest{Messages: batch}, false)
	if err != nil || len(wire.Messages) != 4 || wire.Messages[2].Role != "tool" || wire.Messages[3].Role != "tool" || len(wire.Messages[1].ToolCalls[1].ExtraContent) != 0 {
		t.Fatal("complete parallel batch was rejected or copied the first signature to its unsigned call", err)
	}
	for _, interrupted := range []Message{
		{Role: "assistant", Content: "interrupted"},
		{Role: "system", Content: "interrupted"},
		{Role: "user", Content: "new turn"},
		{Role: "assistant", Content: replay.AssistantText(), ToolCalls: calls[:1], Replay: replay},
	} {
		messages := append([]Message(nil), batch[:3]...)
		messages = append(messages, interrupted, lastResult)
		if _, _, err := p.prepareRequest(ChatRequest{Messages: messages}, false); err == nil {
			t.Errorf("incomplete batch accepted an intervening %s message", interrupted.Role)
		}
	}
}

func TestGeminiSignatureValidationAndPreAccumulationBounds(t *testing.T) {
	for _, raw := range []string{`{"google":{"thought_signature":null}}`, `{"google":{"thought_signature":1}}`,
		`{"google":{"thought_signature":""}}`, `{"google":[]}`, `{"google":{"thought_signature":"\ud800"}}`,
		`{"google":{"thought_signature":"one","thought_signature":"two"}}`, `{"google":{},"google":{}}`} {
		if _, err := geminiSignature(json.RawMessage(raw)); err == nil {
			t.Fatalf("malformed recognized signature accepted: %s", raw)
		}
	}
	b := &geminiReplayBuilder{}
	if err := b.capture(geminiExtraContent(strings.Repeat("x", maxGeminiSignatureBytes+1)), nil); err == nil || b.bytes != 0 || b.signature != "" {
		t.Fatal("oversized signature was accumulated")
	}
	for index := 0; index < 4; index++ {
		if err := b.capture(geminiExtraContent(strings.Repeat("x", maxGeminiSignatureBytes)), &index); err != nil {
			t.Fatal(err)
		}
	}
	index := 4
	if err := b.capture(geminiExtraContent("y"), &index); err == nil || b.bytes != MaxProviderReplayBytes || len(b.tools) != 4 {
		t.Fatal("aggregate signature bound was enforced after accumulation")
	}
	index = 0
	if err := b.capture(geminiExtraContent("conflict"), &index); err == nil || b.tools[0] != strings.Repeat("x", maxGeminiSignatureBytes) {
		t.Fatal("conflicting complete signatures were concatenated or overwritten")
	}
}

func geminiBoundTestReplay(t *testing.T) (*OpenAICompatibleProvider, *ProviderReplay, []ToolCall) {
	t.Helper()
	p := &OpenAICompatibleProvider{name: "gemini-test", baseURL: geminiTestEndpoint, defaultModel: "route-alias",
		runtime: geminiTestRuntime{wireModel: geminiTestModel, digest: "a"}}
	calls := []ToolCall{{ID: "native-a", Name: "echo", Arguments: json.RawMessage(`{"city":"Paris"}`)}}
	b := &geminiReplayBuilder{responseID: "native-response", signature: "private-message", tools: map[int]string{0: "private-tool"}}
	replay, err := b.replay(p, "route-alias", geminiTestModel, "upstream-snapshot", " public text \n", calls)
	if err != nil {
		t.Fatal(err)
	}
	return p, replay, calls
}

func TestGeminiReplayRejectsChangedProvenanceAndAcceptedBatch(t *testing.T) {
	p, replay, calls := geminiBoundTestReplay(t)
	message := Message{Role: "assistant", Content: replay.AssistantText(), ToolCalls: calls, Replay: replay}
	for _, test := range []struct {
		name   string
		change func(*ProviderReplay, *Message)
	}{
		{"provider", func(r *ProviderReplay, _ *Message) { r.provider = "other" }},
		{"route model", func(r *ProviderReplay, _ *Message) { r.model = geminiTestModel }},
		{"transport", func(r *ProviderReplay, _ *Message) { r.transport = HarnessTransportOpenAIResponses }},
		{"binding", func(r *ProviderReplay, _ *Message) { r.binding = strings.Repeat("b", 64) }},
		{"response identity", func(r *ProviderReplay, _ *Message) { r.responseID = "different-response" }},
		{"tool order", func(r *ProviderReplay, _ *Message) { r.parts[2].CallIndex = 1 }},
		{"missing signature", func(r *ProviderReplay, _ *Message) { r.parts[2].Opaque = json.RawMessage(`{}`) }},
		{"wire model", func(r *ProviderReplay, _ *Message) {
			r.parts[0].Opaque = json.RawMessage(`{"wire_model":"gemini-3.1-pro-preview","upstream_model":"upstream-snapshot"}`)
		}},
		{"public text", func(_ *ProviderReplay, m *Message) { m.Content = strings.TrimSpace(m.Content) }},
		{"accepted arguments", func(_ *ProviderReplay, m *Message) { m.ToolCalls[0].Arguments = json.RawMessage(`{"city":"Rome"}`) }},
		{"accepted name", func(_ *ProviderReplay, m *Message) { m.ToolCalls[0].Name = "changed" }},
		{"accepted ID", func(_ *ProviderReplay, m *Message) { m.ToolCalls[0].ID = "changed" }},
		{"missing private state", func(_ *ProviderReplay, m *Message) { m.Replay = nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := message
			m.Replay = replay.Clone()
			m.ToolCalls = append([]ToolCall(nil), calls...)
			test.change(m.Replay, &m)
			_, _, err := p.prepareRequest(ChatRequest{Messages: []Message{{Role: "user", Content: "continue"}, m, {Role: "user", ToolResults: []ToolResult{{ToolCallID: "native-a", Content: `{"ok":true}`}}}}}, false)
			if err == nil {
				t.Fatal("changed signed continuation reached HTTP preparation")
			}
		})
	}
	request := ChatRequest{Messages: []Message{{Role: "user", Content: "continue"}, message, {Role: "user", ToolResults: []ToolResult{{ToolCallID: "native-a", Content: `{"ok":true}`}}}}}
	_, wire, err := p.prepareRequest(request, false)
	if err != nil || wire.Messages[1].Content == nil || *wire.Messages[1].Content != message.Content {
		t.Fatal("scoped replay changed the accepted assistant text", err)
	}
	other := *p
	other.baseURL = "https://other.test/v1"
	if _, _, err := other.prepareRequest(request, false); err == nil {
		t.Fatal("foreign private replay was silently discarded")
	}
	other = *p
	other.runtime = geminiTestRuntime{wireModel: geminiTestModel, digest: "b"}
	if _, _, err := other.prepareRequest(request, false); err == nil {
		t.Fatal("changed runtime restored old private state")
	}
	foreign, err := newProviderReplay(p.name, p.defaultModel, HarnessTransportOpenAIResponses, strings.Repeat("a", 64), []providerReplayPart{{Kind: "tool", ID: "response-item", CallIndex: 0}}, calls)
	if err != nil {
		t.Fatal(err)
	}
	request.Messages[1].Replay = foreign
	if _, _, err := p.prepareRequest(request, false); err == nil {
		t.Fatal("Responses v1 replay entered Gemini Chat")
	}
}

func TestGeminiReplayStoredSchemaAndHistoryBounds(t *testing.T) {
	p, replay, calls := geminiBoundTestReplay(t)
	raw, _ := replay.EncodeForStore()
	for _, changed := range []string{
		strings.Replace(string(raw), `"version":3`, `"version":4`, 1),
		strings.Replace(string(raw), `"version":3`, `"version":3,"version":3`, 1),
		strings.Replace(string(raw), `"version":3`, `"VERSION":3`, 1),
		strings.Replace(string(raw), `"response_id":"native-response"`, `"response_id":null`, 1),
		strings.Replace(string(raw), `"private-tool"`, `"\ud800"`, 1),
		strings.Replace(string(raw), `"signature":"private-tool"`, `"signature":"private-tool","unknown":true`, 1),
		strings.Replace(string(raw), `"signature":"private-message"`, `"signature":""`, 1),
		strings.Replace(string(raw), `"wire_model"`, `"WIRE_MODEL"`, 1),
		strings.Replace(string(raw), `"kind":"gemini_metadata"`, `"kind":"gemini_metadata","call_index":0`, 1),
	} {
		if _, err := DecodeProviderReplay([]byte(changed)); err == nil {
			t.Fatal("ambiguous, unknown or malformed durable v3 replay was read")
		}
	}
	var messages []Message
	messages = append(messages, Message{Role: "user", Content: "continue"})
	for round := 0; round < 5; round++ {
		r := replay.Clone()
		tool, _ := json.Marshal(geminiReplayTool{Signature: strings.Repeat("x", maxGeminiSignatureBytes)})
		r.parts[2].Opaque = tool
		if _, err := r.EncodeForStore(); err != nil {
			t.Fatal(err)
		}
		messages = append(messages, Message{Role: "assistant", Content: r.AssistantText(), ToolCalls: calls, Replay: r}, Message{Role: "user", ToolResults: []ToolResult{{ToolCallID: calls[0].ID, Content: `{"ok":true}`}}})
	}
	if _, _, err := p.prepareRequest(ChatRequest{Messages: messages}, false); err == nil {
		t.Fatal("unbounded signatures were copied into outbound history")
	}
}

func TestGeminiReplayRequiresNativeMetadataForParallelCalls(t *testing.T) {
	p, _, calls := geminiBoundTestReplay(t)
	calls = append(calls, ToolCall{ID: "native-b", Name: "echo", Arguments: json.RawMessage(`{}`)})
	b := &geminiReplayBuilder{responseID: "parallel-response", tools: map[int]string{0: "private-first-tool"}}
	replay, err := b.replay(p, "route-alias", geminiTestModel, "upstream-snapshot", "", calls)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := replay.EncodeForStore()
	if err != nil {
		t.Fatal(err)
	}
	var stored map[string]json.RawMessage
	var parts []json.RawMessage
	if json.Unmarshal(raw, &stored) != nil || json.Unmarshal(stored["parts"], &parts) != nil || len(parts) != 3 {
		t.Fatal("parallel fixture did not contain native metadata and both tool positions")
	}
	stored["parts"], _ = json.Marshal(parts[1:])
	missingMetadata, _ := json.Marshal(stored)
	if decoded, err := DecodeProviderReplay(missingMetadata); err == nil || decoded != nil {
		t.Fatal("durable v3 replay accepted parallel calls without wire/upstream provenance")
	}
	replay.parts = replay.parts[1:]
	if _, err := replay.EncodeForStore(); err == nil {
		t.Fatal("v3 writer accepted parallel calls without native metadata")
	}
}

func TestGeminiQualificationBindingRejectsOldOneRoundRecord(t *testing.T) {
	p, _, _ := geminiBoundTestReplay(t)
	old := providerHarnessBinding(p.runtime, p.name, p.baseURL, p.defaultModel, HarnessTransportOpenAIChatCompletions, HarnessToolStrategyNative, HarnessJSONStrategyNative)
	profile := p.DescribeModelHarness(p.defaultModel)
	now := time.Now().UTC()
	record := HarnessQualification{ProtocolVersion: ModelHarnessProtocolVersion, BindingDigest: old, ToolCallsQualified: true, ToolResultsQualified: true, StrictJSONQualified: true, StreamingQualified: true, QualifiedAt: now, ExpiresAt: now.Add(time.Hour)}
	if record.Validate(profile.BindingDigest, now) == nil {
		t.Fatal("old one-round qualification certified sequential Gemini replay")
	}
	ordinary := *p
	ordinary.baseURL = "https://example.test/v1"
	expected := providerHarnessBinding(ordinary.runtime, ordinary.name, ordinary.baseURL, ordinary.defaultModel, HarnessTransportOpenAIChatCompletions, HarnessToolStrategyNative, HarnessJSONStrategyNative)
	if ordinary.DescribeModelHarness(ordinary.defaultModel).BindingDigest != expected {
		t.Fatal("unrelated provider qualification binding changed")
	}
}

func TestGeminiStreamMalformedAndIncompleteReplayFailsClosed(t *testing.T) {
	for _, test := range []struct{ name, payload string }{
		{"changed native response ID", `data: {"id":"changed","model":"upstream-snapshot","choices":[{"index":0,"delta":{}}]}\n\n`},
		{"missing tool index", `data: {"id":"native-response","model":"upstream-snapshot","choices":[{"index":0,"delta":{"tool_calls":[{"extra_content":{"google":{"thought_signature":"private-tool"}}}]}}]}\n\n`},
		{"conflicting signature", `data: {"id":"native-response","model":"upstream-snapshot","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"extra_content":{"google":{"thought_signature":"conflicting"}}}]}}]}\n\n`},
		{"duplicate metadata container", `data: {"id":"native-response","model":"upstream-snapshot","choices":[{"index":0,"delta":{"extra_content":{"google":{"thought_signature":"private"}},"extra_content":{"google":{"thought_signature":"private"}}}}]}\n\n`},
		{"missing DONE", ``},
	} {
		t.Run(test.name, func(t *testing.T) {
			p, replay, calls := geminiBoundTestReplay(t)
			_ = replay
			_ = calls
			initial := `data: {"id":"native-response","model":"upstream-snapshot","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"native-a","type":"function","function":{"name":"echo","arguments":"{}"},"extra_content":{"google":{"thought_signature":"private-tool"}}}]}}]}\n\n`
			finish := `data: {"id":"native-response","model":"upstream-snapshot","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}\n\n`
			body := strings.ReplaceAll(initial+test.payload+finish, `\n`, "\n")
			if test.name != "missing DONE" {
				body += "data: [DONE]\n\n"
			}
			chunks := make(chan ChatChunk, 64)
			p.readStream(t.Context(), io.NopCloser(strings.NewReader(body)), p.defaultModel, chunks, geminiTestModel)
			failed := false
			for chunk := range chunks {
				if chunk.Done || chunk.Replay != nil {
					t.Fatal("incomplete or malformed stream exposed replay")
				}
				if chunk.Err != nil {
					failed = true
				}
			}
			if !failed {
				t.Fatal("malformed native stream was not rejected")
			}
		})
	}
}

func TestGeminiUnrelatedChatMetadataRemainsInert(t *testing.T) {
	requests := 0
	p, err := NewOpenAICompatibleProvider(OpenAICompatibleConfig{Name: "unrelated", BaseURL: "https://unrelated.test/v1", DefaultModel: geminiTestModel, APIKey: "fixture",
		HTTPClient: &http.Client{Transport: geminiTestTransport(func(request *http.Request) (*http.Response, error) {
			requests++
			body, _ := io.ReadAll(request.Body)
			if strings.Contains(string(body), "extra_content") {
				t.Error("unknown private extension was reflected into another provider request")
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"model":"other-valid-upstream-model","choices":[{"index":0,"message":{"role":"assistant","extra_content":{"google":{"thought_signature":false}},"tool_calls":[{"id":"native-a","type":"function","function":{"name":"echo","arguments":"{}"},"extra_content":{"google":{"thought_signature":null}}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`)), Header: make(http.Header)}, nil
		})}})
	if err != nil {
		t.Fatal(err)
	}
	request := ChatRequest{Messages: []Message{{Role: "user", Content: "continue"}}}
	response, err := p.Chat(t.Context(), request)
	if err != nil || response.Replay != nil || len(response.ToolCalls) != 1 || response.Model != geminiTestModel {
		t.Fatal("Gemini model spelling changed an unrelated provider's normal Chat behavior", err)
	}
	request.Messages = append(request.Messages, Message{Role: "assistant", ToolCalls: response.ToolCalls}, Message{Role: "user", ToolResults: []ToolResult{{ToolCallID: response.ToolCalls[0].ID, Content: `{"ok":true}`}}})
	if _, err := p.Chat(t.Context(), request); err != nil || requests != 2 {
		t.Fatal("unrelated unsigned tool continuation changed", err)
	}
}

func TestGeminiChatRequiresNativeFirstToolSignature(t *testing.T) {
	for _, test := range []struct{ name, id, extra string }{
		{"missing identity", "", `{"google":{"thought_signature":"private"}}`},
		{"missing signature", "native-response", `{}`},
		{"oversized signature", "native-response", string(geminiExtraContent(strings.Repeat("x", maxGeminiSignatureBytes+1)))},
		{"duplicate container", "native-response", `{"google":{"thought_signature":"private"},"google":{"thought_signature":"private"}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			p, err := NewOpenAICompatibleProvider(OpenAICompatibleConfig{Name: "test", BaseURL: geminiTestEndpoint, DefaultModel: geminiTestModel, APIKey: "fixture",
				HTTPClient: &http.Client{Transport: geminiTestTransport(func(*http.Request) (*http.Response, error) {
					body := `{"id":` + strconvJSON(test.id) + `,"model":" valid-upstream-snapshot ","choices":[{"index":0,"message":{"role":"assistant","extra_content":{"google":{"thought_signature":"message-does-not-replace-tool-signature"}},"tool_calls":[{"id":"native-a","type":"function","function":{"name":"echo","arguments":"{}"},"extra_content":` + test.extra + `}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
				})}})
			if err != nil {
				t.Fatal(err)
			}
			if response, err := p.Chat(t.Context(), ChatRequest{Messages: []Message{{Role: "user", Content: "continue"}}}); err == nil || response != nil {
				t.Fatal("missing, oversized or ambiguous native signature state was accepted")
			}
		})
	}
}
