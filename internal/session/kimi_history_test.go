package session

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/policy"
)

type kimiSessionTransport func(*http.Request) (*http.Response, error)

func (f kimiSessionTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestKimiLegacySessionRefusesBeforeProviderCall(t *testing.T) {
	var requests atomic.Int32
	p, err := llm.NewOpenAICompatibleProvider(llm.OpenAICompatibleConfig{Name: "kimi-session", DefaultModel: "kimi-k3", APIKey: "fixture-only", BaseURL: "https://api.moonshot.ai/v1",
		HTTPClient: &http.Client{Transport: kimiSessionTransport(func(*http.Request) (*http.Response, error) {
			requests.Add(1)
			return nil, apperror.New(apperror.CodeFailedPrecondition, "unexpected request")
		})}})
	if err != nil {
		t.Fatal(err)
	}
	router := llm.NewRouter(llm.ModelRef{Provider: p.Name(), Model: "kimi-k3"})
	router.RegisterProvider(p)
	store := newMemorySessionStore()
	manager := NewManager(store, router, policy.NewDefaultChecker())
	sess, err := manager.Create(t.Context(), "ws-demo", "K3 legacy", "kimi-session/kimi-k3")
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.Send(t.Context(), sess.ID, "first user request")
	if apperror.CodeOf(err) != apperror.CodeFailedPrecondition || !strings.Contains(err.Error(), "Root Supervisor private assistant history") || requests.Load() != 0 {
		t.Fatal("legacy path sent an unsupported model request", err)
	}
	for _, message := range store.messages {
		if message.Role == "assistant" {
			t.Fatal("legacy path persisted an unbound ordinary answer")
		}
	}
}
