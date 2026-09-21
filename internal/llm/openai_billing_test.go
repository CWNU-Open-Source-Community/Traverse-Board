package llm

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestOpenAIPaymentRequiredIsPermanentCapacityFailure(t *testing.T) {
	for _, body := range []string{
		`{"error":{"message":"Insufficient Balance","type":"unknown_error","code":"invalid_request_error"}}`,
		`{"error":{"type":"server_error","code":"rate_limit_exceeded"}}`,
		`not json`,
		``,
	} {
		failure := openAIHTTPError("billing-test", http.StatusPaymentRequired, "60", []byte(body))
		if failure.Kind != OutcomePermanent || failure.Reason != ProviderFailureCapacity || failure.Kind.Retryable() || failure.StatusCode != 402 {
			t.Fatalf("billing error became retryable or protocol failure: %+v", failure)
		}
	}
	if got := ProviderErrorReason(&ProviderError{Kind: OutcomePermanent, StatusCode: 402}); got != ProviderFailureCapacity {
		t.Fatalf("status-only classification=%s", got)
	}
}

func TestResponsesPaymentRequiredDoesNotRetryOrExposeBody(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"error":{"message":"private billing detail","type":"server_error","code":"rate_limit_exceeded"}}`))
	}))
	defer server.Close()
	provider := newTestResponsesProvider(t, server.URL+"/v1/responses")
	_, err := provider.Chat(t.Context(), ChatRequest{Messages: []Message{{Role: "user", Content: "hello"}}})
	var failure *ProviderError
	if !errors.As(err, &failure) || failure.Kind != OutcomePermanent || failure.Reason != ProviderFailureCapacity || failure.Kind.Retryable() || requests.Load() != 1 {
		t.Fatalf("unexpected billing behavior: error=%v requests=%d", err, requests.Load())
	}
	if strings.Contains(err.Error(), "private billing detail") || failure.Cause != nil {
		t.Fatal("provider response body leaked")
	}
}
