package llm

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func providerToolDeltaEventVector(argumentDeltas int) []StreamEventType {
	out := []StreamEventType{StreamResponseStarted, StreamOutputItemStarted,
		StreamToolCallStarted}
	for range argumentDeltas {
		out = append(out, StreamToolArgumentDelta)
	}
	return append(out, StreamToolCallCompleted, StreamOutputItemCompleted,
		StreamResponseCompleted)
}

func TestItemStreamAccumulatorPreservesInterleavedProviderOrderAndStableIdentity(t *testing.T) {
	providerEvents := newProviderStreamEvents("compatible", "model", "wire-response",
		StreamGranularityDelta)
	usage := Usage{InputTokens: 9, OutputTokens: 4, TotalTokens: 13}
	callA := ToolCall{ID: "wire-call-a", Name: "run_command",
		Arguments: json.RawMessage(`{"command":"id"}`)}
	callB := ToolCall{ID: "wire-call-b", Name: "read_file",
		Arguments: json.RawMessage(`{"path":"README.md"}`)}
	chunks := []ChatChunk{
		{},
		{Events: []StreamEvent{
			providerEvents.start(),
			providerEvents.emit(StreamEvent{Type: StreamOutputItemStarted, ItemID: "tool/1",
				ItemType: StreamItemToolCall, ItemStatus: StreamItemInProgress}),
			providerEvents.emit(StreamEvent{Type: StreamToolCallStarted, ItemID: "tool/1",
				CallID: callB.ID, ItemType: StreamItemToolCall, ItemStatus: StreamItemInProgress,
				ToolName: callB.Name}),
			providerEvents.emit(StreamEvent{Type: StreamToolArgumentDelta, ItemID: "tool/1",
				CallID: callB.ID, ItemType: StreamItemToolCall, ItemStatus: StreamItemInProgress,
				ToolName: callB.Name, ArgumentDelta: `{"path":"`}),
		}},
		{Events: []StreamEvent{
			providerEvents.emit(StreamEvent{Type: StreamOutputItemStarted, ItemID: "tool/0",
				ItemType: StreamItemToolCall, ItemStatus: StreamItemInProgress}),
			providerEvents.emit(StreamEvent{Type: StreamToolCallStarted, ItemID: "tool/0",
				CallID: callA.ID, ItemType: StreamItemToolCall, ItemStatus: StreamItemInProgress,
				ToolName: callA.Name}),
			providerEvents.emit(StreamEvent{Type: StreamToolArgumentDelta, ItemID: "tool/0",
				CallID: callA.ID, ItemType: StreamItemToolCall, ItemStatus: StreamItemInProgress,
				ToolName: callA.Name, ArgumentDelta: string(callA.Arguments)}),
			providerEvents.emit(StreamEvent{Type: StreamToolArgumentDelta, ItemID: "tool/1",
				CallID: callB.ID, ItemType: StreamItemToolCall, ItemStatus: StreamItemInProgress,
				ToolName: callB.Name, ArgumentDelta: `README.md"}`}),
		}},
		{Done: true, ToolCalls: []ToolCall{callA, callB}, Usage: &usage,
			Provider: "compatible", Model: "model", Events: []StreamEvent{
				providerEvents.emit(StreamEvent{Type: StreamToolCallCompleted, ItemID: "tool/0",
					CallID: callA.ID, ItemType: StreamItemToolCall,
					ItemStatus: StreamItemReadyForValidation, ToolName: callA.Name,
					CompletedCall: &callA}),
				providerEvents.emit(StreamEvent{Type: StreamOutputItemCompleted, ItemID: "tool/0",
					CallID: callA.ID, ItemType: StreamItemToolCall, ItemStatus: StreamItemCompleted,
					ToolName: callA.Name}),
				providerEvents.emit(StreamEvent{Type: StreamToolCallCompleted, ItemID: "tool/1",
					CallID: callB.ID, ItemType: StreamItemToolCall,
					ItemStatus: StreamItemReadyForValidation, ToolName: callB.Name,
					CompletedCall: &callB}),
				providerEvents.emit(StreamEvent{Type: StreamOutputItemCompleted, ItemID: "tool/1",
					CallID: callB.ID, ItemType: StreamItemToolCall, ItemStatus: StreamItemCompleted,
					ToolName: callB.Name}),
				providerEvents.terminalEvent(OutcomeSuccess, &usage),
			}},
	}

	accumulator, err := NewItemStreamAccumulator("resp_attempt", "compatible", "model")
	if err != nil {
		t.Fatal(err)
	}
	var final ChatChunk
	var gotTypes []StreamEventType
	for index, chunk := range chunks {
		normalized, events, consumeErr := accumulator.Consume(chunk)
		if consumeErr != nil {
			t.Fatalf("consume chunk %d: %v", index, consumeErr)
		}
		for _, event := range events {
			gotTypes = append(gotTypes, event.Type)
			if event.ResponseID != "resp_attempt" || event.Sequence != len(gotTypes) {
				t.Fatalf("event was not canonicalized: %#v", event)
			}
		}
		if normalized.Done {
			final = normalized
		}
	}
	wantTypes := []StreamEventType{StreamResponseStarted,
		StreamOutputItemStarted, StreamToolCallStarted, StreamToolArgumentDelta,
		StreamOutputItemStarted, StreamToolCallStarted, StreamToolArgumentDelta,
		StreamToolArgumentDelta, StreamToolCallCompleted, StreamOutputItemCompleted,
		StreamToolCallCompleted, StreamOutputItemCompleted, StreamResponseCompleted}
	if !reflect.DeepEqual(gotTypes, wantTypes) {
		t.Fatalf("event order = %v, want %v", gotTypes, wantTypes)
	}
	if !final.Done || len(final.ToolCalls) != 2 {
		t.Fatalf("unexpected final chunk: %#v", final)
	}
	for _, call := range final.ToolCalls {
		if call.StreamResponseID != "resp_attempt" || call.StreamItemID == "" ||
			call.StreamCallID == "" || call.StreamCallID == call.ID {
			t.Fatalf("provider call was not reconciled to stable identities: %#v", call)
		}
	}
	items := accumulator.Items()
	if len(items) != 2 || items[0].ToolName != callB.Name || items[1].ToolName != callA.Name ||
		items[0].Status != StreamItemCompleted || items[0].Provisional || items[0].Durable {
		t.Fatalf("ordered item projection = %#v", items)
	}
	if _, _, err := accumulator.Consume(ChatChunk{}); err == nil ||
		!strings.Contains(err.Error(), "after its terminal") {
		t.Fatalf("post-terminal chunk error = %v", err)
	}
}

func TestItemStreamAccumulatorRejectsArgumentMismatchAndProviderExecution(t *testing.T) {
	t.Run("argument mismatch", func(t *testing.T) {
		events := newProviderStreamEvents("provider", "model", "wire", StreamGranularityDelta)
		accumulator, err := NewItemStreamAccumulator("resp_local", "provider", "model")
		if err != nil {
			t.Fatal(err)
		}
		call := ToolCall{ID: "wire-call", Name: "read_file",
			Arguments: json.RawMessage(`{"path":"other"}`)}
		chunk := ChatChunk{Events: []StreamEvent{
			events.start(),
			events.emit(StreamEvent{Type: StreamOutputItemStarted, ItemID: "item",
				ItemType: StreamItemToolCall, ItemStatus: StreamItemInProgress}),
			events.emit(StreamEvent{Type: StreamToolCallStarted, ItemID: "item", CallID: call.ID,
				ItemType: StreamItemToolCall, ItemStatus: StreamItemInProgress, ToolName: call.Name}),
			events.emit(StreamEvent{Type: StreamToolArgumentDelta, ItemID: "item", CallID: call.ID,
				ItemType: StreamItemToolCall, ItemStatus: StreamItemInProgress, ToolName: call.Name,
				ArgumentDelta: `{"path":"README.md"}`}),
			events.emit(StreamEvent{Type: StreamToolCallCompleted, ItemID: "item", CallID: call.ID,
				ItemType: StreamItemToolCall, ItemStatus: StreamItemReadyForValidation,
				ToolName: call.Name, CompletedCall: &call}),
		}}
		if _, _, err := accumulator.Consume(chunk); err == nil ||
			!strings.Contains(err.Error(), "do not match") {
			t.Fatalf("argument mismatch error = %v", err)
		}
	})

	t.Run("provider execution authority", func(t *testing.T) {
		events := newProviderStreamEvents("provider", "model", "wire", StreamGranularityComplete)
		accumulator, err := NewItemStreamAccumulator("resp_local", "provider", "model")
		if err != nil {
			t.Fatal(err)
		}
		chunk := ChatChunk{Events: []StreamEvent{events.start(), events.emit(StreamEvent{
			Type: StreamToolExecutionStarted, ItemID: "item", CallID: "call",
			ItemType: StreamItemToolCall, ItemStatus: StreamItemInProgress, ToolName: "read_file",
		})}}
		if _, _, err := accumulator.Consume(chunk); err == nil ||
			!strings.Contains(err.Error(), "cannot emit tool execution") {
			t.Fatalf("provider execution error = %v", err)
		}
	})

	t.Run("terminal compatibility", func(t *testing.T) {
		events := newProviderStreamEvents("provider", "model", "wire", StreamGranularityDelta)
		accumulator, err := NewItemStreamAccumulator("resp_local", "provider", "model")
		if err != nil {
			t.Fatal(err)
		}
		usage := Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2}
		chunk := ChatChunk{Text: "ok", Events: []StreamEvent{
			events.start(),
			events.emit(StreamEvent{Type: StreamOutputItemStarted, ItemID: "message",
				ItemType: StreamItemMessage, ItemStatus: StreamItemInProgress}),
			events.emit(StreamEvent{Type: StreamTextDelta, ItemID: "message",
				ItemType: StreamItemMessage, ItemStatus: StreamItemInProgress, TextDelta: "ok"}),
			events.emit(StreamEvent{Type: StreamOutputItemCompleted, ItemID: "message",
				ItemType: StreamItemMessage, ItemStatus: StreamItemCompleted}),
			events.terminalEvent(OutcomeSuccess, &usage),
		}}
		if _, _, err := accumulator.Consume(chunk); err == nil ||
			!strings.Contains(err.Error(), "not reflected") {
			t.Fatalf("unreflected terminal error = %v", err)
		}
		aborted := accumulator.Abort(OutcomeInvalidResponse)
		if len(aborted) != 2 || aborted[0].Sequence != 1 ||
			aborted[0].Type != StreamResponseStarted || aborted[1].Sequence != 2 ||
			aborted[1].Type != StreamResponseFailed {
			t.Fatalf("rejected chunk partially advanced the stream: %#v", aborted)
		}
	})
}

func TestItemStreamAccumulatorLegacyProjectionAndCancellation(t *testing.T) {
	accumulator, err := NewItemStreamAccumulator("resp_legacy", "legacy", "model")
	if err != nil {
		t.Fatal(err)
	}
	normalized, events, err := accumulator.Consume(ChatChunk{Text: "working"})
	if err != nil || normalized.Text != "working" || len(events) != 3 ||
		events[0].Type != StreamResponseStarted || events[2].Type != StreamTextDelta {
		t.Fatalf("legacy projection: normalized=%#v events=%#v err=%v", normalized, events, err)
	}
	aborted := accumulator.Abort(OutcomeCancelled)
	if len(aborted) != 1 || aborted[0].Type != StreamResponseCancelled ||
		aborted[0].Outcome != OutcomeCancelled {
		t.Fatalf("cancel events = %#v", aborted)
	}
	items := accumulator.Items()
	if len(items) != 1 || items[0].Status != StreamItemCancelled || !items[0].Provisional {
		t.Fatalf("cancelled items = %#v", items)
	}
	for _, event := range aborted {
		if event.Type == StreamResponseCompleted {
			t.Fatal("cancellation synthesized a successful completion")
		}
	}

	completed, err := NewItemStreamAccumulator("resp_complete", "legacy", "model")
	if err != nil {
		t.Fatal(err)
	}
	usage := Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2}
	call := ToolCall{ID: "provider-call", Name: "read_file",
		Arguments: json.RawMessage(`{"path":"README.md"}`)}
	_, completedEvents, err := completed.Consume(ChatChunk{Done: true,
		ToolCalls: []ToolCall{call}, Usage: &usage, Provider: "legacy", Model: "model"})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range completedEvents {
		if boundary, ok := BoundaryForEvent(event); ok {
			if err := boundary.Validate("resp_complete"); err != nil {
				t.Fatalf("legacy boundary %#v: %v", boundary, err)
			}
		}
	}
}

func TestItemStreamAccumulatorLegacyChunkValidationIsTransactional(t *testing.T) {
	accumulator, err := NewItemStreamAccumulator("resp_legacy", "legacy", "model")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := accumulator.Consume(ChatChunk{Text: "uncommitted", Done: true}); err == nil {
		t.Fatal("legacy terminal without usage was accepted")
	}
	aborted := accumulator.Abort(OutcomeInvalidResponse)
	if len(aborted) != 2 || aborted[0].Sequence != 1 || aborted[1].Sequence != 2 ||
		aborted[0].Type != StreamResponseStarted || aborted[1].Type != StreamResponseFailed ||
		len(accumulator.Items()) != 0 {
		t.Fatalf("malformed legacy chunk partially advanced the stream: %#v %#v",
			aborted, accumulator.Items())
	}
}

func TestItemStreamAccumulatorLegacyPreservesUTF8AndModelBoundaries(t *testing.T) {
	accumulator, err := NewItemStreamAccumulator("resp_legacy", "legacy", "requested")
	if err != nil {
		t.Fatal(err)
	}
	encoded := []byte("你")
	for _, part := range []string{string(encoded[:1]), string(encoded[1:2]), string(encoded[2:])} {
		if _, _, err := accumulator.Consume(ChatChunk{Text: part, Model: "actual"}); err != nil {
			t.Fatalf("split UTF-8 delta was rejected: %v", err)
		}
	}
	usage := Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2}
	if _, _, err := accumulator.Consume(ChatChunk{Done: true, Usage: &usage,
		Provider: "legacy", Model: "changed"}); err == nil ||
		!strings.Contains(err.Error(), "model identity changed") {
		t.Fatalf("changed compatibility model error = %v", err)
	}
	if _, _, err := accumulator.Consume(ChatChunk{Done: true, Usage: &usage,
		Provider: "legacy", Model: "actual"}); err != nil {
		t.Fatalf("stable compatibility model was rejected: %v", err)
	}
}

func TestItemStreamPublicAndDurableShapesOmitRawContent(t *testing.T) {
	secret := ToolCall{ID: "wire", Name: "run_command",
		Arguments: json.RawMessage(`{"token":"top-secret"}`)}
	event := StreamEvent{Version: ItemStreamProtocolVersion, Sequence: 1,
		Type: StreamToolArgumentDelta, ResponseID: "response", ItemID: "item", CallID: "call",
		ItemType: StreamItemToolCall, ItemStatus: StreamItemInProgress, ToolName: "run_command",
		Provider: "provider", Model: "model", Provisional: true,
		TextDelta: "private-reasoning", ArgumentDelta: "top-secret", CompletedCall: &secret}
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"private-reasoning", "top-secret", "arguments"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("raw stream content leaked into JSON: %s", encoded)
		}
	}
	credentialLikeName := "g" + "hp_" + strings.Repeat("a", 24)
	boundary, ok := BoundaryForEvent(StreamEvent{Sequence: 2, Type: StreamToolCallStarted,
		ItemID: "item", CallID: "call", ItemType: StreamItemToolCall,
		ItemStatus: StreamItemInProgress, ToolName: credentialLikeName})
	if !ok || boundary.ToolName != "redacted_tool" {
		t.Fatalf("credential-like tool name was not redacted: %#v", boundary)
	}
	encodedBoundary, err := json.Marshal(boundary)
	if err != nil || strings.Contains(string(encodedBoundary), credentialLikeName) {
		t.Fatalf("credential-like tool name leaked into a boundary: %s err=%v",
			encodedBoundary, err)
	}

	completedCalls, items, err := CompleteOutputItems("resp_durable", "", nil, nil)
	if err != nil || len(completedCalls) != 0 || len(items) != 1 ||
		items[0].Type != StreamItemMessage || !items[0].Durable || items[0].Provisional {
		t.Fatalf("empty legacy completion projection: calls=%#v items=%#v err=%v",
			completedCalls, items, err)
	}
}
