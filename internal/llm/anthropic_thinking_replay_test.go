package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

const anthropicReplayTestModel = "claude-sonnet-5"

func anthropicReplayTestBlocks(round int) []json.RawMessage {
	thinking := ""
	if round == 2 {
		thinking = "private-thinking-round-2  \n"
	}
	private, _ := json.Marshal(anthropicReplayBlock{Type: "thinking", Thinking: stringPointer(thinking),
		Signature: stringPointer(fmt.Sprintf("private-signature-%d  \napi_key=sk-private-replay-test", round))})
	return []json.RawMessage{
		private,
		json.RawMessage(fmt.Sprintf(`{"type":"redacted_thinking","data":"private-redacted-%d  \n"}`, round)),
		json.RawMessage(`{"type":"text","text":"first "}`),
		json.RawMessage(fmt.Sprintf(`{"type":"tool_use","id":"native-a-%d","name":"echo","input":{"round":%d,"branch":"a"}}`, round, round)),
		json.RawMessage(`{"type":"text","text":"second "}`),
		json.RawMessage(fmt.Sprintf(`{"type":"tool_use","id":"native-b-%d","name":"echo","input":{"round":%d,"branch":"b"}}`, round, round)),
	}
}

func anthropicReplayTestState(t *testing.T, responseID string, round int) (*ProviderReplay, []ToolCall) {
	t.Helper()
	builder := anthropicReplayBuilder{responseID: responseID}
	var calls []ToolCall
	for index, raw := range anthropicReplayTestBlocks(round) {
		if err := builder.startBlock(index, raw); err != nil {
			t.Fatal(err)
		}
		if err := builder.endBlock(index); err != nil {
			t.Fatal(err)
		}
		block := builder.blocks[index].block
		if block.Type == "tool_use" {
			calls = append(calls, ToolCall{ID: block.ID, Name: block.Name, Arguments: block.Input})
		}
	}
	replay, err := builder.finish("anthropic-test", anthropicReplayTestModel, strings.Repeat("a", 64), calls)
	if err != nil {
		t.Fatal(err)
	}
	return replay, calls
}

func assertAnthropicReplayPrivate(t *testing.T, value any) {
	t.Helper()
	public, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	for _, encoded := range []string{string(public), fmt.Sprintf("%v", value), fmt.Sprintf("%#v", value)} {
		if strings.Contains(encoded, "private-thinking") || strings.Contains(encoded, "private-signature") || strings.Contains(encoded, "private-redacted") ||
			strings.Contains(encoded, "sk-private-replay-test") {
			t.Fatal("private native data reached public JSON or diagnostic formatting")
		}
	}
}

func TestAnthropicReplayStoreBindingAndIndependentResponseIdentities(t *testing.T) {
	for round := 1; round <= 2; round++ {
		replay, received := anthropicReplayTestState(t, fmt.Sprintf("msg_native_%d", round), round)
		prepared := append([]ToolCall(nil), received...)
		for index := range prepared {
			prepared[index].ID = fmt.Sprintf("durable-%d-%d", round, index)
		}
		prepared[0].Arguments = json.RawMessage(fmt.Sprintf(`{"round":%d,"branch":"a","accepted_default":true}`, round))
		bound, err := replay.BindToolCalls(prepared)
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
		message := Message{Role: "assistant", Content: restored.AssistantText(), ToolCalls: prepared, Replay: restored}
		blocks, aliases, err := anthropicReplayMessage(message, "anthropic-test", anthropicReplayTestModel, strings.Repeat("a", 64))
		if err != nil {
			t.Fatal(err)
		}
		var expected []anthropicReplayBlock
		for _, raw := range anthropicReplayTestBlocks(round) {
			var block anthropicReplayBlock
			if err := json.Unmarshal(raw, &block); err != nil {
				t.Fatal(err)
			}
			expected = append(expected, block)
		}
		expected[3].Input = prepared[0].Arguments
		if !reflect.DeepEqual(blocks, expected) || restored.responseID != fmt.Sprintf("msg_native_%d", round) {
			t.Fatal("ordered native blocks or per-response provenance changed after restoration")
		}
		results := []ToolResult{{ToolCallID: prepared[1].ID, Content: "b", IsError: true}, {ToolCallID: prepared[0].ID, Content: "a"}}
		wire, err := anthropicReplayResults(results, aliases)
		if err != nil || wire[0].ToolCallID != received[1].ID || wire[1].ToolCallID != received[0].ID || !wire[0].IsError {
			t.Fatalf("native result pairing failed: %v", err)
		}
		if results[0].ToolCallID != prepared[1].ID || replay.calls[0].DurableID != received[0].ID {
			t.Fatal("binding or result remapping mutated caller-owned state")
		}
		clone := restored.Clone()
		clone.parts[0].Opaque[0] = 'x'
		if _, err := restored.EncodeForStore(); err != nil {
			t.Fatal("replay clone shares private storage")
		}
		for _, value := range []any{restored, message, ChatResponse{Replay: restored}, ChatChunk{Replay: restored}} {
			assertAnthropicReplayPrivate(t, value)
		}
		if restored.AssistantText() != "first second " || restored.ContextBytes() < len(restored.parts[0].Opaque) {
			t.Fatal("private budget or public projection is missing")
		}
	}
}

func TestAnthropicReplayRejectsSourceAndToolBatchChanges(t *testing.T) {
	replay, calls := anthropicReplayTestState(t, "msg_original", 1)
	message := Message{Role: "assistant", Content: replay.AssistantText(), ToolCalls: calls, Replay: replay}
	for name, mutate := range map[string]func(*Message){
		"public text": func(m *Message) { m.Content += "changed" },
		"role":        func(m *Message) { m.Role = "user" },
		"call id":     func(m *Message) { m.ToolCalls[0].ID = "other" },
		"call name":   func(m *Message) { m.ToolCalls[0].Name = "other" },
		"arguments":   func(m *Message) { m.ToolCalls[0].Arguments = json.RawMessage(`{"other":true}`) },
		"call order":  func(m *Message) { m.ToolCalls[0], m.ToolCalls[1] = m.ToolCalls[1], m.ToolCalls[0] },
		"response id": func(m *Message) { m.Replay.responseID = "msg_latest" },
		"transport":   func(m *Message) { m.Replay.transport = HarnessTransportOpenAIResponses },
		"version":     func(m *Message) { m.Replay.version = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			changed := message
			changed.Replay = replay.Clone()
			changed.ToolCalls = append([]ToolCall(nil), calls...)
			mutate(&changed)
			if _, _, err := anthropicReplayMessage(changed, "anthropic-test", anthropicReplayTestModel, strings.Repeat("a", 64)); err == nil {
				t.Fatal("changed native provenance or tool batch was accepted")
			}
		})
	}
	for _, source := range [][3]string{
		{"other-provider", anthropicReplayTestModel, strings.Repeat("a", 64)},
		{"anthropic-test", "other-model", strings.Repeat("a", 64)},
		{"anthropic-test", anthropicReplayTestModel, strings.Repeat("b", 64)},
	} {
		if _, _, err := anthropicReplayMessage(message, source[0], source[1], source[2]); err == nil {
			t.Fatal("changed provider, model or binding was accepted")
		}
	}
	_, aliases, err := anthropicReplayMessage(message, "anthropic-test", anthropicReplayTestModel, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	for _, results := range [][]ToolResult{
		nil,
		{{ToolCallID: calls[0].ID, Content: "a"}},
		{{ToolCallID: calls[0].ID, Content: "a"}, {ToolCallID: calls[0].ID, Content: "duplicate"}},
		{{ToolCallID: calls[0].ID, Content: "a"}, {ToolCallID: "foreign-call", Content: "b"}},
	} {
		if _, err := anthropicReplayResults(results, aliases); err == nil {
			t.Fatal("incomplete, duplicate or foreign native results were accepted")
		}
	}
}

func TestAnthropicReplayRejectsAmbiguousNativeBlocksAndDeltas(t *testing.T) {
	for name, raw := range map[string]string{
		"unknown type":        `{"type":"server_tool_use","id":"private-signature"}`,
		"extra private field": `{"type":"thinking","thinking":"","signature":"s","arguments":{}}`,
		"extra null field":    `{"type":"thinking","thinking":"","signature":"s","text":null}`,
		"extra empty id":      `{"type":"thinking","thinking":"","signature":"s","id":""}`,
		"null thinking":       `{"type":"thinking","thinking":null,"signature":"s"}`,
		"null signature":      `{"type":"thinking","thinking":"","signature":null}`,
		"duplicate":           `{"type":"thinking","thinking":"first","thinking":"second","signature":"s"}`,
		"case alias":          `{"type":"thinking","Thinking":"","signature":"s"}`,
		"unpaired unicode":    `{"type":"thinking","thinking":"","signature":"\ud800"}`,
		"empty redacted data": `{"type":"redacted_thinking","data":""}`,
		"tool array":          `{"type":"tool_use","id":"call","name":"echo","input":[]}`,
		"duplicate input key": `{"type":"tool_use","id":"call","name":"echo","input":{"a":1,"a":2}}`,
	} {
		t.Run(name, func(t *testing.T) {
			builder := anthropicReplayBuilder{}
			if err := builder.startBlock(0, json.RawMessage(raw)); err == nil || strings.Contains(err.Error(), "private-signature") {
				t.Fatal("ambiguous native content accepted or leaked in error")
			}
		})
	}
	for _, raw := range []string{
		`{"type":"thinking_delta","thinking":null}`,
		`{"type":"signature_delta","signature":"s","text":null}`,
		`{"type":"thinking_delta","thinking":"x","Thinking":"y"}`,
		`{"type":"future_delta","private":"private-signature"}`,
		`{"type":"text_delta","text":"wrong block"}`,
	} {
		builder := anthropicReplayBuilder{}
		if err := builder.startBlock(0, json.RawMessage(`{"type":"thinking","thinking":""}`)); err != nil {
			t.Fatal(err)
		}
		if err := builder.appendDelta(0, json.RawMessage(raw)); err == nil || strings.Contains(err.Error(), "private-signature") {
			t.Fatal("unsupported native delta accepted or leaked in error")
		}
	}
}

func TestAnthropicReplaySignatureLifecycleAndByteBounds(t *testing.T) {
	builder := anthropicReplayBuilder{responseID: "msg_signed"}
	if err := builder.startBlock(0, json.RawMessage(`{"type":"thinking","thinking":""}`)); err != nil {
		t.Fatal(err)
	}
	if err := builder.startBlock(2, json.RawMessage(`{"type":"text","text":"gap"}`)); err == nil {
		t.Fatal("sparse native block index was accepted")
	}
	for index := 0; index < 1024; index++ {
		if err := builder.appendDelta(0, json.RawMessage(`{"type":"signature_delta","signature":" s "}`)); err != nil {
			t.Fatal(err)
		}
	}
	if err := builder.appendDelta(0, json.RawMessage(`{"type":"thinking_delta","thinking":"late"}`)); err == nil {
		t.Fatal("thinking changed after the signature")
	}
	if err := builder.endBlock(0); err != nil {
		t.Fatal(err)
	}
	if err := builder.endBlock(0); err == nil {
		t.Fatal("duplicate native block stop was accepted")
	}
	replay, err := builder.finish("anthropic-test", anthropicReplayTestModel, strings.Repeat("a", 64), nil)
	if err != nil {
		t.Fatal(err)
	}
	var block anthropicReplayBlock
	if err := json.Unmarshal(replay.parts[0].Opaque, &block); err != nil || *block.Thinking != "" || *block.Signature != strings.Repeat(" s ", 1024) {
		t.Fatal("empty thinking or segmented opaque signature changed")
	}
	if err := builder.appendDelta(0, json.RawMessage(`{"type":"signature_delta","signature":"late"}`)); err == nil {
		t.Fatal("closed private block was modified")
	}
	for _, signature := range []*string{nil, new(string)} {
		unsigned := anthropicReplayBuilder{responseID: "msg_unsigned"}
		thinking := ""
		raw, _ := json.Marshal(anthropicReplayBlock{Type: "thinking", Thinking: &thinking, Signature: signature})
		if err := unsigned.startBlock(0, raw); err != nil {
			t.Fatal(err)
		}
		if err := unsigned.endBlock(0); err != nil {
			t.Fatal(err)
		}
		if _, err := unsigned.finish("anthropic-test", anthropicReplayTestModel, strings.Repeat("a", 64), nil); err == nil {
			t.Fatal("unsigned thinking was accepted for complete replay")
		}
	}
	bounded := anthropicReplayBuilder{}
	if err := bounded.startBlock(0, json.RawMessage(`{"type":"thinking","thinking":""}`)); err != nil {
		t.Fatal(err)
	}
	huge, _ := json.Marshal(anthropicReplayDelta{Type: "signature_delta", Signature: stringPointer(strings.Repeat("s", MaxProviderReplayBytes))})
	if err := bounded.appendDelta(0, huge); err == nil || bounded.blocks[0].signature.Len() != 0 {
		t.Fatal("oversized opaque delta was appended before its bound was checked")
	}
}

func stringPointer(value string) *string { return &value }

func TestAnthropicReplayStoredSchemaAndResponsesV1Compatibility(t *testing.T) {
	replay, _ := anthropicReplayTestState(t, "msg_schema", 1)
	encoded, err := replay.EncodeForStore()
	if err != nil {
		t.Fatal(err)
	}
	for _, changed := range []string{
		strings.Replace(string(encoded), `"response_id":"msg_schema"`, `"response_id":null`, 1),
		strings.Replace(string(encoded), `"response_id":"msg_schema"`, `"Response_ID":"msg_schema"`, 1),
		strings.Replace(string(encoded), `"version":2`, `"version":2,"version":2`, 1),
		strings.Replace(string(encoded), `"wire_id"`, `"Wire_ID"`, 1),
		strings.Replace(string(encoded), `"parts":[`, `"parts":null,"Parts":[`, 1),
	} {
		if _, err := DecodeProviderReplay([]byte(changed)); err == nil {
			t.Fatal("ambiguous v2 envelope was accepted")
		}
	}
	legacy, err := newProviderReplay("provider", "model", HarnessTransportOpenAIResponses, strings.Repeat("b", 64),
		[]providerReplayPart{{Kind: "reasoning", ID: "reasoning-1", Opaque: json.RawMessage(`{"id":"reasoning-1","type":"reasoning","summary":[],"encrypted_content":"opaque"}`)}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	old, err := legacy.EncodeForStore()
	if err != nil || strings.Contains(string(old), "response_id") || !strings.Contains(string(old), `"version":1`) {
		t.Fatal("Responses v1 storage format changed")
	}
	restored, err := DecodeProviderReplay(old)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, err := restored.EncodeForStore()
	if err != nil || string(roundTrip) != string(old) {
		t.Fatal("Responses v1 replay no longer round-trips byte-for-byte")
	}
	legacyWithNativeID := strings.TrimSuffix(string(old), "}") + `,"response_id":null}`
	if _, err := DecodeProviderReplay([]byte(legacyWithNativeID)); err == nil {
		t.Fatal("v2-only native identity was accepted in a v1 envelope")
	}
}

func TestAnthropicThinkingReplayRejectsIncompleteAndForeignRequests(t *testing.T) {
	provider, err := NewAnthropicCompatibleProvider(AnthropicCompatibleConfig{Name: "anthropic-test", BaseURL: "https://api.anthropic.com",
		APIKey: "fixture-secret", DefaultModel: anthropicReplayTestModel})
	if err != nil {
		t.Fatal(err)
	}
	replay, calls := anthropicReplayTestState(t, "msg_request", 1)
	replay.binding = provider.replayBinding(anthropicReplayTestModel)
	assistant := Message{Role: "assistant", Content: replay.AssistantText(), ToolCalls: calls, Replay: replay}
	results := Message{Role: "user", ToolResults: []ToolResult{{ToolCallID: calls[0].ID, Content: "a"}, {ToolCallID: calls[1].ID, Content: "b"}}}
	for name, messages := range map[string][]Message{
		"missing results":     {assistant},
		"new user text":       {assistant, {Role: "user", Content: "skip tool results"}},
		"incomplete results":  {assistant, {Role: "user", ToolResults: results.ToolResults[:1]}},
		"next assistant":      {assistant, {Role: "assistant", Content: "changed"}, results},
		"system between pair": {assistant, {Role: "system", Content: "changed"}, results},
		"replay on user":      {{Role: "user", Replay: replay}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := provider.toRequest(anthropicReplayTestModel, ChatRequest{Messages: messages}); err == nil {
				t.Fatal("unpaired native request was sent")
			}
		})
	}
	for name, change := range map[string]func(*AnthropicCompatibleProvider){
		"endpoint":               func(p *AnthropicCompatibleProvider) { p.baseURL = "https://other.invalid" },
		"provider":               func(p *AnthropicCompatibleProvider) { p.name = "other" },
		"thinking configuration": func(p *AnthropicCompatibleProvider) { p.disableThinking = true },
	} {
		t.Run(name, func(t *testing.T) {
			changed := *provider
			change(&changed)
			if _, err := changed.toRequest(anthropicReplayTestModel, ChatRequest{Messages: []Message{assistant, results}}); err == nil {
				t.Fatal("foreign replay was silently converted to plain tool history")
			}
		})
	}
	if _, err := provider.toRequest("other-model", ChatRequest{Model: "other-model", Messages: []Message{assistant, results}}); err == nil {
		t.Fatal("native replay moved to a different model")
	}
}

func TestAnthropicThinkingReplayInvalidPrivateStateDoesNotAuthorizeTools(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, failure := range []string{"missing signature", "empty signature", "missing response id", "unknown block", "truncated"} {
			t.Run(fmt.Sprintf("stream=%t/%s", stream, failure), func(t *testing.T) {
				blocks := anthropicReplayTestBlocks(1)
				responseID, stop := "msg_invalid", "tool_use"
				switch failure {
				case "missing signature":
					blocks[0] = json.RawMessage(`{"type":"thinking","thinking":"private-signature"}`)
				case "empty signature":
					blocks[0] = json.RawMessage(`{"type":"thinking","thinking":"","signature":""}`)
				case "missing response id":
					responseID = ""
				case "unknown block":
					blocks[0] = json.RawMessage(`{"type":"future_native_block","secret":"private-signature"}`)
				case "truncated":
					stop = "max_tokens"
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						// Malformed fixtures use raw start/stop pairs; a valid
						// tool delta is unnecessary for this failure boundary.
						_, _ = fmt.Fprintf(w, "data: {\"type\":\"message_start\",\"message\":{\"id\":%q,\"model\":%q,\"usage\":{\"input_tokens\":8}}}\n\n", responseID, anthropicReplayTestModel)
						for index, raw := range blocks {
							_, _ = fmt.Fprintf(w, "data: {\"type\":\"content_block_start\",\"index\":%d,\"content_block\":%s}\n\ndata: {\"type\":\"content_block_stop\",\"index\":%d}\n\n", index, raw, index)
						}
						_, _ = fmt.Fprintf(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":%q},\"usage\":{\"output_tokens\":6}}\n\ndata: {\"type\":\"message_stop\"}\n\n", stop)
					} else {
						_ = json.NewEncoder(w).Encode(map[string]any{"id": responseID, "model": anthropicReplayTestModel, "content": blocks,
							"stop_reason": stop, "usage": map[string]int{"input_tokens": 8, "output_tokens": 6}})
					}
				}))
				defer server.Close()
				provider, err := NewAnthropicCompatibleProvider(AnthropicCompatibleConfig{Name: "anthropic-test", BaseURL: server.URL,
					APIKey: "fixture-secret", DefaultModel: anthropicReplayTestModel})
				if err != nil {
					t.Fatal(err)
				}
				response, err := anthropicReplayTestChat(t, provider, ChatRequest{Messages: []Message{{Role: "user", Content: "inspect"}}}, stream)
				if err == nil || (response != nil && (len(response.ToolCalls) != 0 || response.Replay != nil)) || strings.Contains(err.Error(), "private-signature") {
					t.Fatal("invalid or truncated private response authorized tools or exposed opaque state")
				}
				if failure != "truncated" && ProviderErrorKind(err) != OutcomeInvalidResponse {
					t.Fatalf("native protocol failure has wrong classification: %v", err)
				}
			})
		}
	}
}

func TestAnthropicReplayKeepsPlainChatTextBoundaries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"msg_plain","model":"model","content":[{"type":"text","text":"first"},{"type":"text","text":"  "},{"type":"text","text":"second"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":3}}`))
	}))
	defer server.Close()
	provider, err := NewAnthropicCompatibleProvider(AnthropicCompatibleConfig{Name: "test", BaseURL: server.URL, APIKey: "fixture-secret", DefaultModel: "model"})
	if err != nil {
		t.Fatal(err)
	}
	response, err := provider.Chat(t.Context(), ChatRequest{Messages: []Message{{Role: "user", Content: "inspect"}}})
	if err != nil || response.Text != "first\nsecond" || response.Replay != nil {
		t.Fatalf("plain Chat projection changed: %v", err)
	}
}

func TestAnthropicThinkingReplayTwoToolRoundsAndRetry(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			var requestCount atomic.Int32
			var requestMu sync.Mutex
			var retryBody map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				requestMu.Lock()
				defer requestMu.Unlock()
				var body map[string]any
				if json.NewDecoder(request.Body).Decode(&body) != nil {
					http.Error(w, "fixture decoding failed", 500)
					return
				}
				count := int(requestCount.Add(1))
				if count > 1 {
					messages := body["messages"].([]any)
					for round := 1; round <= min(count-1, 2); round++ {
						if count == 3 && round == 2 {
							break // This request retries the first tool-result continuation.
						}
						assistant := messages[1+(round-1)*2].(map[string]any)
						var expected any
						raw, _ := json.Marshal(anthropicReplayTestBlocks(round))
						_ = json.Unmarshal(raw, &expected)
						if !reflect.DeepEqual(assistant["content"], expected) {
							t.Error("native assistant blocks changed in the actual HTTP request")
						}
						results := messages[2+(round-1)*2].(map[string]any)["content"].([]any)
						if len(results) != 2 || results[0].(map[string]any)["tool_use_id"] != fmt.Sprintf("native-a-%d", round) ||
							results[1].(map[string]any)["tool_use_id"] != fmt.Sprintf("native-b-%d", round) {
							t.Error("native tool results changed or extra user text was injected")
						}
					}
				}
				if count == 2 {
					retryBody = body
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				if count == 3 && !reflect.DeepEqual(body, retryBody) {
					t.Error("retry changed previously restored native history")
				}
				round := count
				if count >= 3 {
					round--
				}
				blocks, stop := anthropicReplayTestBlocks(round), "tool_use"
				if round == 3 {
					blocks, stop = []json.RawMessage{json.RawMessage(`{"type":"text","text":"done"}`)}, "end_turn"
				}
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					for _, event := range anthropicReplayTestSSE(t, fmt.Sprintf("msg_round_%d", round), blocks, stop) {
						_, _ = fmt.Fprintf(w, "data: %s\n\n", event)
					}
				} else {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]any{"id": fmt.Sprintf("msg_round_%d", round), "type": "message", "role": "assistant",
						"model": anthropicReplayTestModel, "content": blocks, "stop_reason": stop,
						"usage": map[string]int{"input_tokens": 8, "output_tokens": 6}})
				}
			}))
			defer server.Close()
			provider, err := NewAnthropicCompatibleProvider(AnthropicCompatibleConfig{Name: "anthropic-test", BaseURL: server.URL,
				APIKey: "fixture-secret", DefaultModel: anthropicReplayTestModel})
			if err != nil {
				t.Fatal(err)
			}
			req := ChatRequest{Model: anthropicReplayTestModel, MaxTokens: 256, Messages: []Message{{Role: "user", Content: "inspect"}}}
			for round := 1; round <= 3; round++ {
				response, err := anthropicReplayTestChat(t, provider, req, stream)
				if round == 2 {
					if ProviderErrorKind(err) != OutcomeRetryable || response != nil {
						t.Fatalf("expected isolated retryable continuation error: %v", err)
					}
					response, err = anthropicReplayTestChat(t, provider, req, stream)
				}
				if err != nil {
					t.Fatal(err)
				}
				assertAnthropicReplayPrivate(t, response)
				if round == 3 {
					if response.Text != "done" || response.Replay != nil {
						t.Fatal("final output retained unexpected tool-round replay")
					}
					break
				}
				if response.Replay == nil || len(response.ToolCalls) != 2 || response.Raw != nil {
					t.Fatal("successful native tool response has no private replay")
				}
				prepared := append([]ToolCall(nil), response.ToolCalls...)
				for index := range prepared {
					prepared[index].ID = fmt.Sprintf("durable-%d-%d", round, index)
				}
				bound, err := response.Replay.BindToolCalls(prepared)
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
				req.Messages = append(req.Messages, Message{Role: "assistant", Content: restored.AssistantText(), ToolCalls: prepared, Replay: restored},
					Message{Role: "user", ToolResults: []ToolResult{{ToolCallID: prepared[0].ID, Content: "result a"}, {ToolCallID: prepared[1].ID, Content: "result b", IsError: true}}})
			}
			if requestCount.Load() != 4 {
				t.Fatal("did not perform two tool rounds, a retry and the final continuation")
			}
		})
	}
}

func anthropicReplayTestChat(t *testing.T, provider *AnthropicCompatibleProvider, req ChatRequest, stream bool) (*ChatResponse, error) {
	t.Helper()
	if !stream {
		return provider.Chat(context.Background(), req)
	}
	chunks, err := provider.StreamChat(context.Background(), req)
	if err != nil {
		return nil, err
	}
	var text strings.Builder
	var response *ChatResponse
	for chunk := range chunks {
		assertAnthropicReplayPrivate(t, chunk)
		if chunk.Err != nil {
			return nil, chunk.Err
		}
		text.WriteString(chunk.Text)
		if chunk.Done {
			response = &ChatResponse{Text: text.String(), ToolCalls: chunk.ToolCalls, Replay: chunk.Replay, Provider: chunk.Provider,
				Model: chunk.Model, FinishReason: chunk.FinishReason, Usage: *chunk.Usage}
		}
	}
	if response == nil {
		return nil, fmt.Errorf("fixture stream has no successful completion")
	}
	return response, nil
}

func anthropicReplayTestSSE(t *testing.T, responseID string, blocks []json.RawMessage, stop string) []string {
	t.Helper()
	events := []string{fmt.Sprintf(`{"type":"message_start","message":{"id":%q,"model":%q,"usage":{"input_tokens":8,"output_tokens":0}}}`, responseID, anthropicReplayTestModel)}
	for index, raw := range blocks {
		var block anthropicReplayBlock
		if err := json.Unmarshal(raw, &block); err != nil {
			t.Fatal(err)
		}
		start := block
		var deltaType, field, value string
		switch block.Type {
		case "thinking":
			start.Thinking = stringPointer("")
			start.Signature = nil
			deltaType, field, value = "signature_delta", "signature", *block.Signature
		case "text":
			start.Text = stringPointer("")
			deltaType, field, value = "text_delta", "text", *block.Text
		case "tool_use":
			start.Input = json.RawMessage(`{}`)
			deltaType, field, value = "input_json_delta", "partial_json", string(block.Input)
		}
		encoded, _ := json.Marshal(start)
		events = append(events, fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":%s}`, index, encoded))
		if block.Type == "thinking" && *block.Thinking != "" {
			value := *block.Thinking
			for _, segment := range []string{value[:len(value)/2], value[len(value)/2:]} {
				delta, _ := json.Marshal(map[string]string{"type": "thinking_delta", "thinking": segment})
				events = append(events, fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":%s}`, index, delta))
			}
		}
		if deltaType != "" {
			for _, segment := range []string{value[:len(value)/2], value[len(value)/2:]} {
				delta, _ := json.Marshal(map[string]string{"type": deltaType, field: segment})
				events = append(events, fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":%s}`, index, delta))
			}
		}
		events = append(events, fmt.Sprintf(`{"type":"content_block_stop","index":%d}`, index))
	}
	return append(events, fmt.Sprintf(`{"type":"message_delta","delta":{"type":"message_delta","stop_reason":%q},"usage":{"output_tokens":6}}`, stop), `{"type":"message_stop"}`)
}
