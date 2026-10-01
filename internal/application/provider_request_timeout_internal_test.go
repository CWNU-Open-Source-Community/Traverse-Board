package application

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
)

func TestProviderTotalTimeoutHonorsEarlierCallerAndRunBudget(t *testing.T) {
	for _, test := range []struct {
		name      string
		client    time.Duration
		caller    time.Duration
		budget    int64
		consumed  int64
		wantLocal bool
	}{
		{name: "configured_request", client: time.Second, caller: 3 * time.Second, budget: 5},
		{name: "caller_deadline", client: 120 * time.Second, caller: 100 * time.Millisecond, budget: 5, wantLocal: true},
		{name: "remaining_run_budget", client: 120 * time.Second, caller: 3 * time.Second, budget: 1, consumed: 900, wantLocal: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cancelled := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				<-r.Context().Done()
				close(cancelled)
			}))
			defer server.Close()
			provider, err := llm.NewOpenAICompatibleProvider(llm.OpenAICompatibleConfig{
				Name: "fixture", BaseURL: server.URL, APIKey: "fixture-only", DefaultModel: "model",
				HTTPClient: &http.Client{Timeout: test.client},
			})
			if err != nil {
				t.Fatal(err)
			}
			caller, cancelCaller := context.WithTimeout(t.Context(), test.caller)
			defer cancelCaller()
			// This is the production Supervisor helper, using the persisted Run
			// budget/checkpoint rather than substituting an arbitrary test deadline.
			ctx, cancel := supervisorModelContext(caller, domain.Budget{TimeoutSeconds: test.budget},
				domain.SupervisorCheckpoint{ExecutionMillis: test.consumed}, 0)
			defer cancel()
			started := time.Now()
			response, err := provider.Chat(ctx, llm.ChatRequest{Messages: []llm.Message{{Role: "user", Content: "fixture"}}})
			failure := llm.NormalizeProviderError(provider.Name(), err)
			if response != nil || failure == nil || time.Since(started) > 2*time.Second {
				t.Fatalf("earlier deadline did not terminate HTTP: response=%+v failure=%+v", response, failure)
			}
			if test.wantLocal {
				if ctx.Err() != context.DeadlineExceeded || failure.Kind != llm.OutcomeCancelled || !errors.Is(failure, context.DeadlineExceeded) {
					t.Fatalf("caller/Run deadline was confused with client timeout: ctx=%v failure=%+v", ctx.Err(), failure)
				}
			} else if ctx.Err() != nil || failure.Kind != llm.OutcomeRetryable || failure.Reason != llm.ProviderFailureNetwork ||
				errors.Is(failure, context.DeadlineExceeded) {
				t.Fatalf("client timeout was confused with a caller deadline: ctx=%v failure=%+v", ctx.Err(), failure)
			}
			select {
			case <-cancelled:
			case <-time.After(time.Second):
				t.Fatal("HTTP server did not observe request cancellation")
			}
		})
	}
}
