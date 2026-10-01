package llm

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAnthropicStreamErrorDoesNotExposeUpstreamFields(t *testing.T) {
	for _, test := range []struct {
		name, errorType string
		kind            Outcome
		reason          ProviderFailureReason
	}{
		{"rate", "rate_limit_error", OutcomeRateLimited, ProviderFailureRateLimit},
		{"overload", "overloaded_error", OutcomeRetryable, ProviderFailureNetwork},
		{"api", "api_error", OutcomeRetryable, ProviderFailureNetwork},
		{"unknown", "private-type-canary", OutcomePermanent, ProviderFailureNone},
		{"missing", "", OutcomePermanent, ProviderFailureNone},
	} {
		t.Run(test.name, func(t *testing.T) {
			markers := []string{"private-prompt-canary", "opaque-credential-canary", "上游私有内容", "private-code-canary", "private-type-canary"}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				payload, _ := json.Marshal(map[string]any{"type": "error", "error": map[string]any{
					"type": test.errorType, "code": markers[3],
					"message": strings.Join(markers[:3], "\n") + " sk-test-onlycredential12345678901234567890",
				}})
				_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
			}))
			defer server.Close()
			provider, err := NewAnthropicCompatibleProvider(AnthropicCompatibleConfig{
				Name: "privacy-test", BaseURL: server.URL, APIKey: "fixture-only", DefaultModel: "model",
			})
			if err != nil {
				t.Fatal(err)
			}
			chunks, err := provider.StreamChat(t.Context(), ChatRequest{Model: "model", Messages: []Message{{Role: "user", Content: "hello"}}})
			if err != nil {
				t.Fatal(err)
			}
			var failure *ProviderError
			for chunk := range chunks {
				if chunk.Err != nil {
					failure = NormalizeProviderError(provider.Name(), chunk.Err)
				}
				if chunk.Done && chunk.Err == nil || len(chunk.ToolCalls) != 0 {
					t.Fatal("an upstream error produced a successful response")
				}
			}
			if failure == nil || failure.Kind != test.kind || failure.Reason != test.reason || failure.Cause != nil {
				t.Fatalf("safe classification changed: %#v", failure)
			}
			raw, _ := json.Marshal(failure)
			for _, marker := range markers {
				if strings.Contains(failure.Error(), marker) || strings.Contains(string(raw), marker) {
					t.Errorf("untrusted upstream field escaped the adapter: %q", marker)
				}
			}
			if failure.Message != "returned a streaming error" {
				t.Errorf("stream error must use a fixed safe message: %q", failure.Message)
			}
		})
	}
}
