package llm

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestProviderReplayPrivateBindingAndStoreRoundTrip(t *testing.T) {
	calls := []ToolCall{{ID: "wire-call", Name: "work_item_create", Arguments: json.RawMessage(`{"title":"Original"}`)}}
	state, err := newProviderReplay("provider", "model", HarnessTransportOpenAIResponses, strings.Repeat("a", 64),
		[]providerReplayPart{
			{Kind: "reasoning", ID: "reasoning-1", Opaque: json.RawMessage(`{"id":"reasoning-1","type":"reasoning","summary":[],"encrypted_content":"opaque-test-only"}`)},
			{Kind: "text", ID: "message-1", Phase: "commentary", Text: "Preparing a work item."},
			{Kind: "tool", ID: "tool-1", CallIndex: 0},
		}, calls)
	if err != nil {
		t.Fatal(err)
	}
	prepared := []ToolCall{{ID: "durable-call", Name: calls[0].Name, Arguments: json.RawMessage(`{"title":"Normalized","priority":"high"}`)}}
	bound, err := state.BindToolCalls(prepared)
	if err != nil {
		t.Fatal(err)
	}
	if state.calls[0].DurableID != "wire-call" || bound.calls[0].WireID != "wire-call" {
		t.Fatal("binding mutated original or wire identity")
	}
	encoded, err := bound.EncodeForStore()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := DecodeProviderReplay(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if err := restored.ValidateToolCalls(prepared); err != nil {
		t.Fatal(err)
	}
	if err := restored.ValidateToolCalls(calls); err == nil {
		t.Fatal("unbound tool batch accepted")
	}
	if err := restored.ValidateSource("other", "model"); err == nil {
		t.Fatal("changed provider accepted")
	}
	if restored.matchesSource("provider", "model", HarnessTransportOpenAIResponses, strings.Repeat("b", 64)) {
		t.Fatal("changed endpoint accepted")
	}
	clone := restored.Clone()
	clone.parts[0].Opaque[0] = 'x'
	if _, err := restored.EncodeForStore(); err != nil {
		t.Fatalf("clone shares opaque storage: %v", err)
	}
	for _, value := range []any{restored, Message{Replay: restored}, ChatResponse{Replay: restored}, ChatChunk{Replay: restored}} {
		public, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(public), "opaque-test-only") || strings.Contains(fmt.Sprintf("%#v", value), "opaque-test-only") {
			t.Fatal("private replay exposed")
		}
	}
	if restored.AssistantText() != "Preparing a work item." || restored.ContextBytes() == 0 {
		t.Fatal("missing public text or opaque budget")
	}
	invalid := strings.Replace(string(encoded), `"encrypted_content":"opaque-test-only"`, `"arguments":"unvalidated input"`, 1)
	if _, err := DecodeProviderReplay([]byte(invalid)); err == nil {
		t.Fatal("raw tool payload accepted in opaque state")
	}
}
