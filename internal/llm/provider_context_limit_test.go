package llm

import (
	"net/http"
	"strings"
	"testing"
)

func TestProviderContextLimitClassification(t *testing.T) {
	for name, classify := range map[string]func(string, int, string, []byte) *ProviderError{
		"openai": openAIHTTPError, "anthropic": anthropicHTTPError,
	} {
		t.Run(name, func(t *testing.T) {
			for _, test := range []struct {
				name         string
				status       int
				body         string
				contextLimit bool
			}{
				{"exact code", http.StatusBadRequest, `{"error":{"type":"invalid_request_error","code":"context_length_exceeded","message":"private-echo-token"}}`, true},
				{"ordinary validation", http.StatusBadRequest, `{"error":{"type":"invalid_request_error","message":"invalid tools: private-echo-token"}}`, false},
				{"unrelated status", http.StatusInternalServerError, `{"error":{"code":"context_length_exceeded","message":"private-echo-token"}}`, false},
				{"malformed body", http.StatusBadRequest, `context_length_exceeded private-echo-token`, false},
			} {
				t.Run(test.name, func(t *testing.T) {
					err := classify("test-provider", test.status, "", []byte(test.body))
					if got := string(err.Reason) == "context_limit"; got != test.contextLimit {
						t.Fatalf("context limit = %v, want %v; reason=%s", got, test.contextLimit, err.Reason)
					}
					if test.contextLimit && err.Kind.Retryable() {
						t.Fatal("context overflow must not enable retrying the unchanged request")
					}
					if strings.Contains(err.Error(), "private-echo-token") {
						t.Fatal("provider body leaked into a public error")
					}
				})
			}
		})
	}
}
