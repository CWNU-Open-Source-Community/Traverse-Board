package application_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/store"
)

func TestSupervisorAnthropicStreamErrorRemainsPrivateAfterReopen(t *testing.T) {
	markers := []string{"private-prompt-canary", "opaque-credential-canary", "私有正文标记", "private-type-canary"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		payload, _ := json.Marshal(map[string]any{"type": "error", "error": map[string]any{
			"type": markers[3], "message": strings.Join(markers[:3], "\n"),
		}})
		_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
	}))
	defer server.Close()
	provider, err := llm.NewAnthropicCompatibleProvider(llm.AnthropicCompatibleConfig{
		Name: "privacy-test", BaseURL: server.URL, APIKey: "fixture-only", DefaultModel: "model",
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "privacy.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	run, supervisor, _ := newQualifiedHTTPProviderSupervisor(t, st, provider, domain.Budget{MaxTurns: 2})
	supervisor.WithModelRetryPolicy(application.ModelRetryPolicy{MaxAttempts: 1})
	result, stepErr := supervisor.Step(t.Context(), run.ID)
	if stepErr == nil || result.ModelOutcome != llm.OutcomePermanent || result.ModelAttempts != 1 {
		t.Fatalf("failed upstream response was not rejected: result=%+v err=%v", result, stepErr)
	}
	assertOrdinaryMoneyNoAssistant(t, st, run)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	timeline, err := reopened.ListRunEvents(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if countEventType(timeline, events.ModelFailedEvent) != 1 || countEventType(timeline, events.ModelCompletedEvent) != 0 ||
		countEventType(timeline, events.RunExecutionLeaseReleasedEvent) != 1 {
		t.Fatalf("incorrect persistent failure or lease boundary: %+v", timeline)
	}
	messages, err := reopened.ListSessionMessages(t.Context(), run.SessionID, true)
	if err != nil {
		t.Fatal(err)
	}
	cp, found, err := reopened.GetSupervisorCheckpoint(t.Context(), run.ID)
	if err != nil || !found || cp.Phase != domain.SupervisorTurnFailed {
		t.Fatalf("failed checkpoint lost across reopen: %+v found=%t err=%v", cp, found, err)
	}
	raw, _ := json.Marshal([]any{timeline, messages, cp, result})
	for _, marker := range markers {
		if strings.Contains(string(raw), marker) || strings.Contains(stepErr.Error(), marker) {
			t.Errorf("upstream content survived the real Supervisor persistence path: %q", marker)
		}
	}
	if !strings.Contains(string(raw), "returned a streaming error") {
		t.Error("persistent failure lost its safe diagnostic")
	}
}

func newQualifiedHTTPProviderSupervisor(t *testing.T, st *store.SQLiteStore, provider llm.Provider,
	budget domain.Budget,
) (domain.Run, *application.AgentRunner, *llm.Router) {
	t.Helper()
	run := newStartedRunForProvider(t, st, provider.Name(), budget)
	ref := llm.ModelRef{Provider: provider.Name(), Model: "model"}
	router := llm.NewRouter(ref)
	router.RegisterProvider(provider)
	harness, err := router.HarnessProfile(ref)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := router.SetHarnessQualification(ref, llm.HarnessQualification{
		ProtocolVersion: llm.ModelHarnessProtocolVersion, BindingDigest: harness.BindingDigest,
		ToolCallsQualified: true, ToolResultsQualified: true, StrictJSONQualified: true, StreamingQualified: true,
		QualifiedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	return run, application.NewAgentRunner(st, router, policy.NewDefaultChecker()), router
}
