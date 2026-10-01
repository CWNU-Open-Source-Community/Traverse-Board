package llm

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const responsesErrorMessageCanary = "private-error-message-canary 私有正文标记 sk-fixture-credential-canary"
const responsesErrorParamCanary = "private-error-param-canary"
const responsesErrorCodeCanary = "server_error-private-code-canary"

func TestResponsesGenericErrorClassificationAndPrivacy(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fields map[string]any
		kind   Outcome
		reason ProviderFailureReason
	}{
		{"capacity", map[string]any{"code": "server_error", "param": nil}, OutcomeRetryable, ProviderFailureCapacity},
		{"rate_limit", map[string]any{"code": "rate_limit_exceeded"}, OutcomeRateLimited, ProviderFailureRateLimit},
		{"authentication", map[string]any{"code": "invalid_api_key", "param": responsesErrorParamCanary}, OutcomePermanent, ProviderFailureAuthentication},
		{"network", map[string]any{"code": "request_timeout"}, OutcomeRetryable, ProviderFailureNetwork},
		{"model", map[string]any{"code": "model_not_found"}, OutcomePermanent, ProviderFailureModelNotFound},
		{"missing_code", map[string]any{}, OutcomePermanent, ProviderFailureProtocolIncompatible},
		{"null_code", map[string]any{"code": nil, "param": nil}, OutcomePermanent, ProviderFailureProtocolIncompatible},
		{"empty_code", map[string]any{"code": "", "param": ""}, OutcomePermanent, ProviderFailureProtocolIncompatible},
		{"unknown_code", map[string]any{"code": responsesErrorCodeCanary}, OutcomePermanent, ProviderFailureProtocolIncompatible},
		{"message_is_not_a_code", map[string]any{"message": "server_error " + responsesErrorMessageCanary}, OutcomePermanent, ProviderFailureProtocolIncompatible},
		{"legacy_nested_code", map[string]any{"error": map[string]any{"code": "server_error", "message": responsesErrorMessageCanary}}, OutcomeRetryable, ProviderFailureCapacity},
		{"legacy_nested_type", map[string]any{"error": map[string]any{"type": "rate_limit_error", "message": responsesErrorMessageCanary}}, OutcomeRateLimited, ProviderFailureRateLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := map[string]any{"type": "error", "sequence_number": 1}
			if _, nested := tc.fields["error"]; !nested {
				payload["message"] = responsesErrorMessageCanary
			}
			for key, value := range tc.fields {
				payload[key] = value
			}
			// A generic error is not an authoritative usage receipt, even when
			// an endpoint includes these unrelated fields.
			payload["usage"] = map[string]int{"input_tokens": 0, "output_tokens": 0, "total_tokens": 0}
			payload["response"] = map[string]any{"usage": map[string]int{"input_tokens": 2, "output_tokens": 3, "total_tokens": 5}}
			failure, chunks := responsesGenericFailure(t, responsesErrorFrame(t, payload))
			if failure.Kind != tc.kind || failure.Reason != tc.reason {
				t.Fatalf("classification: got=%+v want=%s/%s", failure, tc.kind, tc.reason)
			}
			assertResponsesPrivateFailure(t, failure, chunks)
			for _, chunk := range chunks {
				if chunk.Usage != nil {
					t.Fatal("generic error manufactured authoritative usage")
				}
			}
		})
	}
}

func TestResponsesGenericErrorRejectsInvalidShapes(t *testing.T) {
	for _, tc := range []struct{ name, payload string }{
		{"empty", `{"type":"error"}`},
		{"missing_message", `{"type":"error","code":"server_error"}`},
		{"null_message", `{"type":"error","code":"server_error","message":null}`},
		{"empty_message", `{"type":"error","message":""}`},
		{"blank_message", `{"type":"error","message":" \n\t"}`},
		{"number_message", `{"type":"error","message":42}`},
		{"object_message", `{"type":"error","message":{}}`},
		{"number_code", `{"type":"error","message":"fixture","code":42}`},
		{"object_code", `{"type":"error","message":"fixture","code":{}}`},
		{"array_param", `{"type":"error","message":"fixture","param":[]}`},
		{"boolean_param", `{"type":"error","message":"fixture","param":false}`},
		{"null_nested", `{"type":"error","error":null}`},
		{"wrong_nested_type", `{"type":"error","error":"fixture"}`},
		{"mixed_shapes", `{"type":"error","code":"server_error","message":"fixture","error":{"code":"invalid_api_key"}}`},
		{"mixed_matching_shapes", `{"type":"error","code":"server_error","message":"fixture","error":{"code":"server_error"}}`},
		{"mixed_null_nested", `{"type":"error","message":"fixture","error":null}`},
		{"malformed_json", `{"type":"error","message":`},
		{"invalid_utf8_message", "{\"type\":\"error\",\"message\":\"\xff\"}"},
		{"invalid_utf8_param", "{\"type\":\"error\",\"message\":\"fixture\",\"param\":\"\xff\"}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failure, chunks := responsesGenericFailure(t, "data: "+tc.payload+"\n\n")
			if failure.Kind != OutcomeInvalidResponse || failure.Reason != ProviderFailureProtocolIncompatible {
				t.Fatalf("invalid shape was accepted: %+v", failure)
			}
			assertResponsesPrivateFailure(t, failure, chunks)
		})
	}
}

func TestResponsesGenericErrorPreservesSSEEventBound(t *testing.T) {
	// Many individually bounded data lines still form one oversized event.
	// This exercises the existing framing limit without changing its value.
	frame := "data: {\"type\":\"error\",\"message\":\"fixture\",\n"
	for i := range 5 {
		frame += fmt.Sprintf("data: \"padding%d\":\"%s\",\n", i, strings.Repeat("x", maxOpenAIStreamEventBytes/4))
	}
	frame += "data: \"code\":\"server_error\"}\n\n"
	failure, chunks := responsesGenericFailure(t, frame)
	if failure.Kind != OutcomeInvalidResponse || failure.Reason != ProviderFailureProtocolIncompatible {
		t.Fatalf("oversized event escaped its existing bound: %+v", failure)
	}
	assertResponsesPrivateFailure(t, failure, chunks)
}

func TestResponsesGenericErrorAfterPartialOutputDoesNotComplete(t *testing.T) {
	for _, tool := range []bool{false, true} {
		t.Run(fmt.Sprintf("completed_tool=%t", tool), func(t *testing.T) {
			frame := responsesErrorStart(t)
			item := map[string]any{"id": "message_1", "type": "message", "status": "in_progress", "role": "assistant"}
			frame += responsesErrorFrame(t, map[string]any{"type": "response.output_item.added", "item": item})
			frame += responsesErrorFrame(t, map[string]any{"type": "response.output_text.delta", "item_id": "message_1", "delta": "uncommitted-text"})
			item["status"], item["content"] = "completed", []any{map[string]any{"type": "output_text", "text": "uncommitted-text"}}
			frame += responsesErrorFrame(t, map[string]any{"type": "response.output_item.done", "item": item})
			if tool {
				call := map[string]any{"id": "function_1", "type": "function_call", "status": "in_progress", "call_id": "call_1", "name": "note_create", "arguments": ""}
				frame += responsesErrorFrame(t, map[string]any{"type": "response.output_item.added", "item": call})
				call["status"], call["arguments"] = "completed", `{"title":"uncommitted-note"}`
				frame += responsesErrorFrame(t, map[string]any{"type": "response.function_call_arguments.done", "item_id": "function_1", "arguments": call["arguments"]})
				frame += responsesErrorFrame(t, map[string]any{"type": "response.output_item.done", "item": call})
			}
			frame += responsesErrorFrame(t, map[string]any{"type": "error", "code": "server_error", "message": responsesErrorMessageCanary, "param": responsesErrorParamCanary})
			// A success frame after the failure must never be accepted.
			frame += responsesErrorFrame(t, map[string]any{"type": "response.completed", "response": map[string]any{
				"id": "response_error", "object": "response", "status": "completed", "model": "model", "usage": map[string]int{"input_tokens": 2, "output_tokens": 3, "total_tokens": 5}}})
			failure, chunks := responsesGenericFailure(t, frame)
			if failure.Kind != OutcomeRetryable || failure.Reason != ProviderFailureCapacity {
				t.Fatalf("partial output changed classification: %+v", failure)
			}
			var text strings.Builder
			for _, chunk := range chunks {
				text.WriteString(chunk.Text)
			}
			if text.String() != "uncommitted-text" {
				t.Fatalf("fixture never reached the partial-output boundary: %q", text.String())
			}
			assertResponsesPrivateFailure(t, failure, chunks)
		})
	}
}

func TestResponsesFailedEventRetainsSeparateAuthoritativeUsage(t *testing.T) {
	for _, tc := range []struct {
		name  string
		usage any
		known bool
	}{
		{"valid", map[string]int{"input_tokens": 2, "output_tokens": 3, "total_tokens": 5}, true},
		{"missing", nil, false},
		{"invalid", map[string]int{"input_tokens": -1, "output_tokens": 3, "total_tokens": 2}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := map[string]any{"id": "response_error", "object": "response", "status": "failed", "model": "model",
				"error": map[string]any{"code": "server_error", "message": responsesErrorMessageCanary}, "usage": tc.usage}
			failure, chunks := responsesGenericFailure(t, responsesErrorStart(t)+responsesErrorFrame(t, map[string]any{"type": "response.failed", "response": response}))
			if failure.Kind != OutcomeRetryable || failure.Reason != ProviderFailureCapacity {
				t.Fatalf("response.failed classification changed: %+v", failure)
			}
			last := chunks[len(chunks)-1]
			if (last.Usage != nil) != tc.known || last.Usage != nil && last.Usage.TotalTokens != 5 {
				t.Fatalf("response.failed usage changed: %+v", last.Usage)
			}
			assertResponsesPrivateFailure(t, failure, chunks)
		})
	}
}

func TestResponsesGenericErrorDoesNotChangeHTTPErrorClassification(t *testing.T) {
	for _, tc := range []struct {
		status int
		kind   Outcome
		reason ProviderFailureReason
	}{
		{http.StatusTooManyRequests, OutcomeRateLimited, ProviderFailureRateLimit},
		{http.StatusUnauthorized, OutcomePermanent, ProviderFailureAuthentication},
		{http.StatusBadRequest, OutcomeRetryable, ProviderFailureCapacity},
	} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, `{"error":{"code":"server_error","message":"`+responsesErrorMessageCanary+`"}}`)
			}))
			defer server.Close()
			provider := newTestResponsesProvider(t, server.URL)
			_, err := provider.StreamChat(t.Context(), boundaryRequest())
			failure := NormalizeProviderError(provider.Name(), err)
			if err == nil || failure.Kind != tc.kind || failure.Reason != tc.reason {
				t.Fatalf("HTTP status classification changed: %+v err=%v", failure, err)
			}
			assertResponsesPrivateFailure(t, failure, nil)
		})
	}
}

func responsesErrorFrame(t *testing.T, payload any) string {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return "data: " + string(raw) + "\n\n"
}

func responsesErrorStart(t *testing.T) string {
	return responsesErrorFrame(t, map[string]any{"type": "response.created", "response": map[string]any{
		"id": "response_error", "object": "response", "status": "in_progress", "model": "model"}})
}

func responsesGenericFailure(t *testing.T, stream string) (*ProviderError, []ChatChunk) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, stream)
	}))
	defer server.Close()
	provider := newTestResponsesProvider(t, server.URL)
	channel, err := provider.StreamChat(t.Context(), boundaryRequest())
	if err != nil {
		t.Fatal(err)
	}
	var chunks []ChatChunk
	var failure *ProviderError
	terminals := 0
	for chunk := range channel {
		chunks = append(chunks, chunk)
		if chunk.Done || len(chunk.ToolCalls) != 0 || chunk.Replay != nil {
			t.Fatal("failed response accepted output, tools or replay")
		}
		if chunk.Err != nil {
			if failure != nil {
				t.Fatal("duplicate failure chunk")
			}
			failure = NormalizeProviderError(provider.Name(), chunk.Err)
		}
		for _, event := range chunk.Events {
			if event.Type == StreamResponseCompleted || event.Usage != nil {
				t.Fatal("failed response emitted a success or public usage")
			}
			if event.Type == StreamResponseFailed {
				terminals++
			}
		}
	}
	if failure == nil || terminals != 1 {
		t.Fatalf("expected one typed failure terminal: failure=%+v terminals=%d", failure, terminals)
	}
	return failure, chunks
}

func assertResponsesPrivateFailure(t *testing.T, failure *ProviderError, chunks []ChatChunk) {
	t.Helper()
	if failure.Cause != nil {
		t.Fatal("upstream error retained a raw cause")
	}
	raw, err := json.Marshal([]any{failure, chunks})
	if err != nil {
		t.Fatal(err)
	}
	transient := fmt.Sprintf("%+v", chunks)
	for _, marker := range []string{responsesErrorMessageCanary, "私有正文标记", "sk-fixture-credential-canary", responsesErrorParamCanary, responsesErrorCodeCanary} {
		if strings.Contains(string(raw), marker) || strings.Contains(transient, marker) || strings.Contains(failure.Error(), marker) || strings.Contains(fmt.Sprintf("%+v", failure), marker) {
			t.Fatalf("upstream error leaked into diagnostic or public event: %q", marker)
		}
	}
}
