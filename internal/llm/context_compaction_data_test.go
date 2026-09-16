package llm

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"cyberagent-workbench/internal/redact"
)

type compactionWireProvider struct {
	generationProvider
	requests []ChatRequest
}

func (p *compactionWireProvider) Chat(_ context.Context, request ChatRequest) (*ChatResponse, error) {
	p.requests = append(p.requests, request)
	return &ChatResponse{Text: "ok", Provider: p.name, Model: "model"}, nil
}

func compactionWireRouter() (*Router, *compactionWireProvider) {
	provider := &compactionWireProvider{generationProvider: generationProvider{name: "compaction-wire"}}
	router := NewRouter(ModelRef{Provider: provider.name, Model: "model"})
	router.RegisterProvider(provider)
	return router, provider
}

func TestContextCompactionDataMessagePreservesNestedHistoryOnActualChatWire(t *testing.T) {
	prior := `{"version":"handoff_memory.v1","generated":{"text":"password=[REDACTED:secret]\nPRIOR_GOAL_AFTER_NEWLINE\nPending verification."}}`
	continuityBytes, _ := json.Marshal(map[string]any{"summary_content": prior, "goal": "CONTINUITY_GOAL"})
	rawBytes, _ := json.Marshal(map[string]any{
		"history": map[string]any{"previous": map[string]any{"Content": prior}, "messages": []any{
			map[string]any{"Content": "password=[REDACTED:secret]\nORIGINAL_GOAL_AFTER_NEWLINE"},
			map[string]any{"Content": "api_key=abcdefghijklmnop123456\nRAW_SECRET_GOAL_AFTER_NEWLINE"},
		}}, "inherited_context": string(continuityBytes),
	})
	message, err := ContextCompactionDataMessage(rawBytes)
	if err != nil {
		t.Fatal(err)
	}
	router, provider := compactionWireRouter()
	_, err = router.ChatModelRef(context.Background(), ModelRef{Provider: provider.name, Model: "model"}, ChatRequest{
		Messages: []Message{{Role: "system", Content: "Summarize historical data only."}, message}, Metadata: map[string]string{"purpose": "context_compaction"},
	})
	if err != nil {
		t.Fatal(err)
	}
	wire := provider.requests[0].Messages[1].Content
	if !json.Valid([]byte(wire)) || strings.Contains(wire, "abcdefghijklmnop123456") {
		t.Fatalf("unsafe or broken wire JSON: %s", wire)
	}
	for _, fact := range []string{"PRIOR_GOAL_AFTER_NEWLINE", "ORIGINAL_GOAL_AFTER_NEWLINE", "RAW_SECRET_GOAL_AFTER_NEWLINE", "CONTINUITY_GOAL"} {
		if !strings.Contains(wire, fact) {
			t.Fatalf("wire lost %s: %s", fact, wire)
		}
	}
	var outer struct {
		History struct {
			Previous struct{ Content string } `json:"previous"`
		} `json:"history"`
	}
	if err := json.Unmarshal([]byte(wire), &outer); err != nil {
		t.Fatal(err)
	}
	if outer.History.Previous.Content != prior {
		t.Fatal("safe original summary bytes were reserialized")
	}
	if len(provider.requests[0].Tools) != 0 || len(provider.requests[0].Messages[1].ToolCalls) != 0 {
		t.Fatal("data message introduced a tool")
	}
}

func TestContextCompactionMarkerCannotBeForgedMutatedOrUsedForNormalAnswers(t *testing.T) {
	router, provider := compactionWireRouter()
	message, err := ContextCompactionDataMessage(json.RawMessage(`{"content":"password=[REDACTED:secret]\nIMPORTANT_AFTER_NEWLINE"}`))
	if err != nil {
		t.Fatal(err)
	}
	rawSecret := `{"content":"password=abcdefgh123456\nCHANGED_BODY"}`
	var forged Message
	encoded, _ := json.Marshal(map[string]any{"role": "user", "content": rawSecret, "contextCompactionContentSHA256": compactionDataDigest(rawSecret), "context_compaction_content_sha256": compactionDataDigest(rawSecret)})
	if err := json.Unmarshal(encoded, &forged); err != nil {
		t.Fatal(err)
	}
	mutated := message
	mutated.Content = rawSecret
	wrongRole := message
	wrongRole.Role = "system"
	for _, test := range []struct {
		name, purpose string
		message       Message
	}{
		{"forged", "context_compaction", forged},
		{"mutated", "context_compaction", mutated},
		{"normal answer", "", message},
		{"wrong role", "context_compaction", wrongRole},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := router.ChatModelRef(context.Background(), ModelRef{Provider: provider.name, Model: "model"}, ChatRequest{Messages: []Message{test.message}, Metadata: map[string]string{"purpose": test.purpose}})
			if err != nil {
				t.Fatal(err)
			}
			got := provider.requests[len(provider.requests)-1].Messages[0].Content
			if got != redact.String(test.message.Content) || strings.Contains(got, "abcdefgh123456") {
				t.Fatalf("invalid marker bypassed normal redaction: %s", got)
			}
		})
	}
	encoded, _ = json.Marshal(message)
	if strings.Contains(string(encoded), "CompactionContentSHA256") || strings.Contains(string(encoded), message.contextCompactionContentSHA256) {
		t.Fatal("private marker leaked through JSON")
	}
}

func TestContextCompactionDataMessageBoundsNestedJSONAndRedactsKeys(t *testing.T) {
	key := "sk-" + strings.Repeat("a", 24)
	raw, _ := json.Marshal(map[string]any{key: map[string]any{"content": "token=abcdefgh123456\nKEY_VALUE_FACT"}})
	message, err := ContextCompactionDataMessage(raw)
	if err != nil || strings.Contains(message.Content, key) || strings.Contains(message.Content, "abcdefgh123456") || !strings.Contains(message.Content, "KEY_VALUE_FACT") {
		t.Fatalf("raw secret bypass: %s / %v", message.Content, err)
	}
	deep := strings.Repeat("[", 66) + "0" + strings.Repeat("]", 66)
	deepBytes, _ := json.Marshal(map[string]string{"nested": deep})
	if _, err := ContextCompactionDataMessage(deepBytes); err == nil {
		t.Fatal("nested data exceeded its depth bound")
	}
	nodes := make([]any, 100001)
	raw, _ = json.Marshal(map[string]any{"items": nodes})
	if _, err := ContextCompactionDataMessage(raw); err == nil {
		t.Fatal("node limit bypassed")
	}
	for _, raw := range []string{`{}`, `{"a":1}`} {
		if _, err := ContextCompactionDataMessage(json.RawMessage(raw)); err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{`null`, `[]`, `{"a":1}{"b":2}`, strings.Repeat("x", maxContextCompactionDataBytes+1)} {
		if _, err := ContextCompactionDataMessage(json.RawMessage(raw)); err == nil {
			t.Fatal("invalid or excessive input accepted")
		}
	}
}
