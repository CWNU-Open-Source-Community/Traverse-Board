package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const kimiReplayTestEndpoint = "https://api.moonshot.ai/v1/chat/completions"

type kimiReplayTestRuntime struct {
	imageTestRuntime
	wireModel string
	digest    string
}

func (r kimiReplayTestRuntime) MapModel(string) (string, error) { return r.wireModel, nil }
func (r kimiReplayTestRuntime) BindingDigest() string           { return strings.Repeat(r.digest, 64) }

func kimiHelperProvider(t *testing.T, endpoint string) *OpenAICompatibleProvider {
	t.Helper()
	p, err := NewOpenAICompatibleProvider(OpenAICompatibleConfig{Name: "kimi-fixture", BaseURL: endpoint,
		APIKey: "fixture-only", DefaultModel: "route-alias",
		Runtime: kimiReplayTestRuntime{wireModel: "kimi-k3", digest: "a"}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func kimiHelperReplay(t *testing.T, p *OpenAICompatibleProvider, id string, reason, content json.RawMessage, calls []ToolCall) *ProviderReplay {
	t.Helper()
	message := openAIMessage{Role: "assistant", ReasoningContent: reason, ContentRaw: content}
	for _, call := range calls {
		message.ToolCalls = append(message.ToolCalls, openAIToolCall{ID: call.ID, Type: "function", Function: openAIFunctionCall{Name: call.Name, Arguments: string(call.Arguments)}})
	}
	var text string
	if len(content) != 0 && string(content) != "null" {
		if err := json.Unmarshal(content, &text); err != nil {
			t.Fatal(err)
		}
		message.Content = &text
	}
	finish := "stop"
	if len(calls) != 0 {
		finish = "tool_calls"
	}
	response := openAIChatResponse{ID: id, Model: "upstream-snapshot", Choices: []openAIChoice{{Message: message, FinishReason: finish}}}
	result := &ChatResponse{Text: text, ToolCalls: calls}
	if err := p.captureKimiResponse("route-alias", "kimi-k3", response, result); err != nil || result.Replay == nil {
		t.Fatal("native replay was not captured", err)
	}
	return result.Replay
}

func kimiHelperCalls() []ToolCall {
	return []ToolCall{
		{ID: "native-a", Name: "echo", Arguments: json.RawMessage(`{"nonce":"a"}`)},
		{ID: "native-b", Name: "echo", Arguments: json.RawMessage(`{"nonce":"b"}`)},
	}
}

func TestKimiReplayScopeAndMappedRouteIsolation(t *testing.T) {
	for _, endpoint := range []string{
		kimiReplayTestEndpoint, "https://api.moonshot.ai/v1", " https://api.moonshot.ai/v1/// ",
		"https://api.moonshot.ai:443/v1/", "https://api.moonshot.cn/v1/chat/completions/",
	} {
		if !KimiReasoningScope(endpoint, "kimi-k3") {
			t.Errorf("documented endpoint rejected: %s", endpoint)
		}
		p := kimiHelperProvider(t, endpoint)
		_, wire, err := p.prepareRequest(ChatRequest{Messages: []Message{{Role: "user", Content: "hello"}}}, false)
		if err != nil || wire.Model != "kimi-k3" || len(wire.Messages) != 1 {
			t.Fatal("configured alias failed to map to K3", err)
		}
	}
	for _, endpoint := range []string{
		"http://api.moonshot.ai/v1", "https://api.moonshot.ai:444/v1", "https://api.moonshot.ai.evil.test/v1",
		"https://fixture@api.moonshot.ai/v1", "https://api.moonshot.ai/v1?key=fixture", "https://api.moonshot.ai/v1?",
		"https://api.moonshot.ai/v1#fragment", "https://api.moonshot.ai/v1#", "https://api.moonshot.ai/%761",
		"https://api.moonshot.ai/anthropic", "https://api.moonshot.ai/v1/responses", "https://api.kimi.com/coding/v1/chat/completions",
		"https://proxy.test/v1", "https://api.moonshot.ai/v1/../v1", "https://api.moonshot.ai/v1/chat/completions/extra",
	} {
		if KimiReasoningScope(endpoint, "kimi-k3") {
			t.Errorf("unsupported endpoint accepted: %s", endpoint)
		}
	}
	for _, model := range []string{"route-alias", "k3", "k3-256k", "kimi-for-coding", "kimi-k2.6", "kimi-k30", "kimi-k3-snapshot", "KIMI-K3", " kimi-k3 "} {
		if KimiReasoningScope(kimiReplayTestEndpoint, model) {
			t.Errorf("unmapped model entered K3 scope: %s", model)
		}
	}
	p := kimiHelperProvider(t, kimiReplayTestEndpoint)
	base := kimiHelperProvider(t, "https://api.moonshot.ai/v1/")
	cn := kimiHelperProvider(t, "https://api.moonshot.cn/v1")
	if p.kimiReplayBinding("route-alias", "kimi-k3") != base.kimiReplayBinding("route-alias", "kimi-k3") ||
		p.kimiReplayBinding("route-alias", "kimi-k3") == cn.kimiReplayBinding("route-alias", "kimi-k3") {
		t.Fatal("binding did not canonicalize the path while isolating origins")
	}
	r := kimiHelperReplay(t, p, "ordinary", json.RawMessage(`"private"`), json.RawMessage(`"answer"`), nil)
	history := []Message{{Role: "assistant", Content: "answer", Replay: r}}
	for name, mutated := range map[string]*OpenAICompatibleProvider{"origin": cn, "runtime": func() *OpenAICompatibleProvider {
		copy := *p
		copy.runtime = kimiReplayTestRuntime{wireModel: "kimi-k3", digest: "b"}
		return &copy
	}()} {
		if _, err := mutated.kimiMessages(history, "route-alias", "kimi-k3"); err == nil {
			t.Errorf("changed %s replay source accepted", name)
		}
	}
	if _, err := p.kimiMessages(history, "another-route", "kimi-k3"); err == nil {
		t.Fatal("another configured route accepted old replay")
	}
}

func TestKimiReplayPreservesNullableReasoningAndContent(t *testing.T) {
	p := kimiHelperProvider(t, kimiReplayTestEndpoint)
	nonempty, _ := json.Marshal(" \n秘密 +/= ")
	values := []json.RawMessage{nil, json.RawMessage("null"), json.RawMessage(`""`), nonempty}
	for reasonIndex, reason := range values {
		for contentIndex, content := range values[:3] {
			t.Run(fmt.Sprintf("reason=%d/content=%d", reasonIndex, contentIndex), func(t *testing.T) {
				calls := kimiHelperCalls()[:1]
				r := kimiHelperReplay(t, p, "native-presence", reason, content, calls)
				raw, err := r.Clone().EncodeForStore()
				if err != nil {
					t.Fatal(err)
				}
				decoded, err := DecodeProviderReplay(raw)
				if err != nil {
					t.Fatal(err)
				}
				calls[0].ID = "durable-a"
				bound, err := decoded.BindToolCalls(calls)
				if err != nil {
					t.Fatal(err)
				}
				wire, err := p.kimiMessages([]Message{{Role: "assistant", ToolCalls: calls, Replay: bound},
					{Role: "user", ToolResults: []ToolResult{{ToolCallID: "durable-a", Content: `{"ok":true}`}}}}, "route-alias", "kimi-k3")
				if err != nil || len(wire) != 2 || wire[0].ToolCalls[0].ID != "native-a" || wire[1].ToolCallID != "native-a" {
					t.Fatal("native call/result aliases did not survive reopen", err)
				}
				encoded, _ := json.Marshal(wire[0])
				var fields map[string]json.RawMessage
				if json.Unmarshal(encoded, &fields) != nil || !bytes.Equal(fields["reasoning_content"], reason) || !bytes.Equal(fields["content"], content) {
					t.Fatal("reasoning or content presence/value changed")
				}
			})
		}
	}
}

func TestKimiReplayStreamFragmentsAndNativeIdentity(t *testing.T) {
	p := kimiHelperProvider(t, kimiReplayTestEndpoint)
	b := &kimiReplayBuilder{}
	if b.observeID("") != nil || b.observeID("native-stream") != nil || b.observeID("native-stream") != nil || b.observeID("different") == nil {
		t.Fatal("native identity was not immutable")
	}
	for _, fragment := range []json.RawMessage{nil, json.RawMessage("null"), json.RawMessage(`""`), json.RawMessage(`" \n"`), json.RawMessage("null"), json.RawMessage(`"秘密"`), json.RawMessage(`" +/= "`), nil} {
		if err := b.capture(fragment, true); err != nil {
			t.Fatal(err)
		}
	}
	empty := ""
	b.observeContent(&empty)
	r, err := b.replay(p, "route-alias", "kimi-k3", "upstream-snapshot", "answer", nil)
	if err != nil {
		t.Fatal(err)
	}
	var metadata kimiReplayMetadata
	if decodeKimiReplayJSON(r.parts[0].Opaque, &metadata) != nil {
		t.Fatal("missing stream metadata")
	}
	var reason string
	if json.Unmarshal(metadata.ReasoningContent, &reason) != nil || reason != " \n秘密 +/= " || metadata.ContentState != "string" {
		t.Fatal("stream fragments were trimmed, overwritten, or reordered")
	}
	if b.capture(json.RawMessage(`"complete"`), false) == nil {
		t.Fatal("mixed complete/stream capture accepted")
	}
	for _, raw := range []json.RawMessage{json.RawMessage(`123`), json.RawMessage(`true`), json.RawMessage(`[]`), json.RawMessage(`{"x":1,"x":2}`),
		json.RawMessage(`"\ud800"`), json.RawMessage(`"\udc00"`), json.RawMessage(`"ok" null`), {0x22, 0xff, 0x22}} {
		builder := &kimiReplayBuilder{}
		if builder.capture(raw, false) == nil {
			t.Error("malformed reasoning accepted")
		}
	}
	for _, fragments := range [][]json.RawMessage{{nil}, {json.RawMessage("null")}, {json.RawMessage(`""`), json.RawMessage("null")}} {
		builder := &kimiReplayBuilder{responseID: "nullable-stream"}
		for _, fragment := range fragments {
			if err := builder.capture(fragment, true); err != nil {
				t.Fatal(err)
			}
		}
		got, err := builder.replay(p, "route-alias", "kimi-k3", "snapshot", "answer", nil)
		if err != nil || decodeKimiReplayJSON(got.parts[0].Opaque, &metadata) != nil {
			t.Fatal("nullable stream marker rejected", err)
		}
		// Use a fresh target: json.Unmarshal intentionally preserves absent fields.
		var current kimiReplayMetadata
		_ = decodeKimiReplayJSON(got.parts[0].Opaque, &current)
		if !bytes.Equal(current.ReasoningContent, fragments[0]) {
			t.Fatal("a null fragment erased stream presence")
		}
	}
}

func TestKimiReplayParallelSequentialOrdinaryHistoryAndPairing(t *testing.T) {
	p := kimiHelperProvider(t, kimiReplayTestEndpoint)
	calls := kimiHelperCalls()
	first := kimiHelperReplay(t, p, "response-a", json.RawMessage(`"reason-a"`), json.RawMessage(`" tool explanation "`), calls)
	secondCalls := []ToolCall{{ID: "native-c", Name: "echo", Arguments: json.RawMessage(`{"nonce":"c"}`)}}
	second := kimiHelperReplay(t, p, "response-b", json.RawMessage(`"reason-b"`), nil, secondCalls)
	final := kimiHelperReplay(t, p, "response-final", json.RawMessage(`"reason-final"`), json.RawMessage(`" final answer "`), nil)
	// Empty call arrays must survive Clone, storage, and exact reattachment.
	encoded, err := final.Clone().EncodeForStore()
	if err != nil || bytes.Contains(encoded, []byte(`"calls":null`)) {
		t.Fatal("ordinary answer cannot be durably cloned", err)
	}
	final, err = DecodeProviderReplay(encoded)
	if err != nil {
		t.Fatal(err)
	}
	history := []Message{{Role: "system", Content: "instructions"}, {Role: "user", Content: "original"},
		{Role: "assistant", Content: first.AssistantText(), ToolCalls: calls, Replay: first},
		{Role: "user", ToolResults: []ToolResult{{ToolCallID: "native-b", Content: `{"ok":true}`}, {ToolCallID: "native-a", Content: `{"ok":true}`}}},
		{Role: "assistant", ToolCalls: secondCalls, Replay: second},
		{Role: "user", ToolResults: []ToolResult{{ToolCallID: "native-c", Content: `{"ok":true}`}}},
		{Role: "assistant", Content: final.AssistantText(), Replay: final}, {Role: "user", Content: "fresh follow-up"}}
	wire, err := p.kimiMessages(history, "route-alias", "kimi-k3")
	if err != nil || len(wire) != 9 || !bytes.Equal(wire[2].ReasoningContent, json.RawMessage(`"reason-a"`)) ||
		!bytes.Equal(wire[5].ReasoningContent, json.RawMessage(`"reason-b"`)) || !bytes.Equal(wire[7].ReasoningContent, json.RawMessage(`"reason-final"`)) ||
		string(wire[2].ContentRaw) != `" tool explanation "` || string(wire[7].ContentRaw) != `" final answer "` {
		t.Fatal("full native history did not survive two rounds and an ordinary turn", err)
	}
	for name, mutate := range map[string]func([]Message) []Message{
		"missing result": func(h []Message) []Message { h[3].ToolResults = h[3].ToolResults[:1]; return h },
		"duplicate result": func(h []Message) []Message {
			h[3].ToolResults = []ToolResult{h[3].ToolResults[0], h[3].ToolResults[0]}
			return h
		},
		"unknown result": func(h []Message) []Message {
			h[3].ToolResults = []ToolResult{{ToolCallID: "unknown", Content: "ok"}}
			return h
		},
		"early user":   func(h []Message) []Message { h[3] = Message{Role: "user", Content: "interrupt"}; return h },
		"early system": func(h []Message) []Message { h[3] = Message{Role: "system", Content: "interrupt"}; return h },
		"partial result with user": func(h []Message) []Message {
			h[3].ToolResults = h[3].ToolResults[:1]
			h[3].Content = "new turn"
			return h
		},
		"legacy ordinary": func(h []Message) []Message { h[6].Replay = nil; return h },
		"legacy tools":    func(h []Message) []Message { h[2].Replay = nil; return h },
		"content drift":   func(h []Message) []Message { h[6].Content = "changed"; return h },
		"call order":      func(h []Message) []Message { h[2].ToolCalls = []ToolCall{calls[1], calls[0]}; return h },
		"call argument drift": func(h []Message) []Message {
			h[2].ToolCalls = append([]ToolCall(nil), calls...)
			h[2].ToolCalls[0].Arguments = json.RawMessage(`{"nonce":"changed"}`)
			return h
		},
		"foreign replay": func(h []Message) []Message { h[6].Replay = h[6].Replay.Clone(); h[6].Replay.version = 3; return h },
		"replay on user": func(h []Message) []Message { h[7].Replay = final; return h },
	} {
		t.Run(name, func(t *testing.T) {
			copy := append([]Message(nil), history...)
			if _, err := p.kimiMessages(mutate(copy), "route-alias", "kimi-k3"); err == nil {
				t.Fatal("inconsistent native history accepted")
			}
		})
	}
}

func TestKimiReplayStrictStoredSchemaAndPrivateErrors(t *testing.T) {
	p := kimiHelperProvider(t, kimiReplayTestEndpoint)
	secret := "private-reasoning-marker"
	r := kimiHelperReplay(t, p, "strict-response", json.RawMessage(`"`+secret+`"`), json.RawMessage(`"answer"`), kimiHelperCalls())
	raw, err := r.EncodeForStore()
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(map[string]any){
		"missing metadata":          func(v map[string]any) { v["parts"] = v["parts"].([]any)[1:] },
		"missing response identity": func(v map[string]any) { delete(v, "response_id") },
		"null calls":                func(v map[string]any) { v["calls"] = nil },
		"unknown envelope field":    func(v map[string]any) { v["unknown"] = secret },
		"wrong case":                func(v map[string]any) { v["Provider"] = v["provider"]; delete(v, "provider") },
		"unknown metadata field": func(v map[string]any) {
			v["parts"].([]any)[0].(map[string]any)["opaque"].(map[string]any)["unknown"] = secret
		},
		"invalid reason type": func(v map[string]any) {
			v["parts"].([]any)[0].(map[string]any)["opaque"].(map[string]any)["reasoning_content"] = true
		},
		"missing content state": func(v map[string]any) {
			delete(v["parts"].([]any)[0].(map[string]any)["opaque"].(map[string]any), "content_state")
		},
		"invalid content state": func(v map[string]any) {
			v["parts"].([]any)[0].(map[string]any)["opaque"].(map[string]any)["content_state"] = "null"
		},
		"wrong wire model": func(v map[string]any) {
			v["parts"].([]any)[0].(map[string]any)["opaque"].(map[string]any)["wire_model"] = "k3"
		},
		"duplicate tool position": func(v map[string]any) { v["parts"].([]any)[3] = v["parts"].([]any)[2] },
		"tool opaque forwarding": func(v map[string]any) {
			v["parts"].([]any)[2].(map[string]any)["opaque"] = map[string]any{"secret": secret}
		},
		"empty wrong-kind field": func(v map[string]any) { v["parts"].([]any)[2].(map[string]any)["text"] = "" },
	} {
		t.Run(name, func(t *testing.T) {
			var value map[string]any
			_ = json.Unmarshal(raw, &value)
			mutate(value)
			encoded, _ := json.Marshal(value)
			if _, err := DecodeProviderReplay(encoded); err == nil || strings.Contains(err.Error(), secret) {
				t.Fatal("corrupt envelope accepted or private state leaked")
			}
		})
	}
	for _, bad := range [][]byte{
		append(append([]byte(nil), raw...), []byte(` null`)...),
		bytes.Replace(raw, []byte(`"version":4`), []byte(`"version":4,"version":4`), 1),
		bytes.Replace(raw, []byte(secret), []byte(`\ud800`), 1),
	} {
		if _, err := DecodeProviderReplay(bad); err == nil {
			t.Fatal("ambiguous stored JSON accepted")
		}
	}
	for _, mutate := range []func(*ProviderReplay){
		func(v *ProviderReplay) { v.parts = v.parts[1:] },
		func(v *ProviderReplay) { v.parts[3] = v.parts[2] },
		func(v *ProviderReplay) { v.responseID = "changed" },
		func(v *ProviderReplay) { v.parts[2].Opaque = json.RawMessage(`{}`) },
	} {
		copy := r.Clone()
		mutate(copy)
		if _, err := copy.EncodeForStore(); err == nil {
			t.Fatal("corrupt in-memory replay encoded")
		}
	}
	public, _ := json.Marshal(Message{Role: "assistant", Content: "answer", Replay: r})
	responseJSON, _ := json.Marshal(ChatResponse{Text: "answer", Replay: r})
	if bytes.Contains(public, []byte(secret)) || bytes.Contains(responseJSON, []byte(secret)) ||
		strings.Contains(fmt.Sprintf("%v %#v", r, r), secret) {
		t.Fatal("private reasoning leaked into public serialization or formatting")
	}
}

func TestKimiReplayBoundsIncludeEnvelopeAndHistory(t *testing.T) {
	p := kimiHelperProvider(t, kimiReplayTestEndpoint)
	large, _ := json.Marshal(strings.Repeat("r", MaxProviderReplayBytes/2+1024))
	b := &kimiReplayBuilder{responseID: "bounded-response"}
	if b.capture(large, true) != nil || b.capture(large, true) == nil {
		t.Fatal("stream reasoning aggregate bound was not enforced")
	}
	tooLarge, _ := json.Marshal(strings.Repeat("r", MaxProviderReplayBytes))
	if (&kimiReplayBuilder{}).capture(tooLarge, false) == nil {
		t.Fatal("oversized native field accepted")
	}
	// Native reasoning below its own cap must still fit the serialized envelope.
	nearLimit, _ := json.Marshal(strings.Repeat("r", MaxProviderReplayBytes-64))
	b = &kimiReplayBuilder{responseID: "bounded-envelope"}
	if err := b.capture(nearLimit, false); err != nil {
		t.Fatal(err)
	}
	if _, err := b.replay(p, "route-alias", "kimi-k3", "snapshot", "answer", nil); err == nil {
		t.Fatal("storage overhead was not included in response bound")
	}
	first := kimiHelperReplay(t, p, "large-a", large, json.RawMessage(`"first"`), nil)
	second := kimiHelperReplay(t, p, "large-b", large, json.RawMessage(`"second"`), nil)
	if _, err := p.kimiMessages([]Message{{Role: "assistant", Content: "first", Replay: first}, {Role: "user", Content: "next"},
		{Role: "assistant", Content: "second", Replay: second}, {Role: "user", Content: "follow-up"}}, "route-alias", "kimi-k3"); err == nil {
		t.Fatal("aggregate history bound was not enforced")
	}
}

func TestKimiReplayReasoningStaysExactWhilePublicContentIsSanitized(t *testing.T) {
	p := kimiHelperProvider(t, kimiReplayTestEndpoint)
	native := " \nAuthorization: Bearer abcdefghijklmnopqrstuvwxyz123456\n "
	raw, _ := json.Marshal(native)
	r := kimiHelperReplay(t, p, "private-vs-public", raw, raw, nil)
	if r.AssistantText() == native {
		t.Fatal("public native content was not sanitized")
	}
	wire, err := p.kimiMessages([]Message{{Role: "assistant", Content: r.AssistantText(), Replay: r.Clone()},
		{Role: "user", Content: "follow up"}}, "route-alias", "kimi-k3")
	if err != nil || !bytes.Equal(wire[0].ReasoningContent, raw) {
		t.Fatal("reasoning was changed by public sanitization", err)
	}
	var accepted string
	if json.Unmarshal(wire[0].ContentRaw, &accepted) != nil || accepted != r.AssistantText() {
		t.Fatal("outbound content differs from accepted content")
	}
	if _, err := p.kimiMessages([]Message{{Role: "assistant", Content: native, Replay: r}}, "route-alias", "kimi-k3"); err == nil {
		t.Fatal("unsanitized public replacement matched the accepted replay")
	}
	response := openAIChatResponse{ID: "unfinished", Model: "snapshot", Choices: []openAIChoice{{
		Message: openAIMessage{ContentRaw: json.RawMessage(`"answer"`)}, FinishReason: "unknown-terminal"}}}
	if err := p.captureKimiResponse("route-alias", "kimi-k3", response, &ChatResponse{Text: "answer"}); err == nil {
		t.Fatal("unknown completion state was sealed as ordinary private history")
	}
	response.Choices[0].FinishReason = "stop"
	if err := p.captureKimiResponse("route-alias", "kimi-k3", response, &ChatResponse{Text: "different"}); err == nil {
		t.Fatal("changed accepted content was sealed as the native response")
	}
}
