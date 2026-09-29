package llm

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPreparedRequestRejectsDriftBeforeAnyHTTP(t *testing.T) {
	for _, change := range []string{"provider", "configuration", "window", "qualification", "expired", "foreign_router", "model"} {
		t.Run(change, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); http.Error(w, "must not send", 500) }))
			defer server.Close()
			p, err := NewAnthropicCompatibleProvider(AnthropicCompatibleConfig{Name: "test", BaseURL: server.URL, APIKey: "synthetic", DefaultModel: "model"})
			if err != nil {
				t.Fatal(err)
			}
			ref := ModelRef{Provider: "test", Model: "model"}
			router := NewRouter(ref)
			router.RegisterProvider(p)
			base, err := router.HarnessProfile(ref)
			if err != nil {
				t.Fatal(err)
			}
			q := HarnessQualification{ProtocolVersion: ModelHarnessProtocolVersion, BindingDigest: base.BindingDigest, ToolCallsQualified: true, ToolResultsQualified: true, StrictJSONQualified: true, StreamingQualified: true, QualifiedAt: time.Now().UTC(), ExpiresAt: time.Now().Add(time.Hour).UTC()}
			if err := router.SetHarnessQualification(ref, q); err != nil {
				t.Fatal(err)
			}
			req, _, err := router.PrepareHarnessRequest(ref, HarnessWorkloadRoot, ChatRequest{Messages: []Message{{Role: "user", Content: "hello"}}, Tools: []ToolSpec{{Name: "echo", Parameters: []byte(`{"type":"object"}`)}}})
			if err != nil {
				t.Fatal(err)
			}
			original, _ := req.PreparedContextWindow()
			switch change {
			case "provider":
				router.RegisterProvider(p)
			case "configuration":
				next := NewRouter(ref)
				next.RegisterProvider(p)
				if err := router.ReplaceConfiguration(next); err != nil {
					t.Fatal(err)
				}
			case "window":
				w := DefaultContextWindow()
				w.MaxOutputTokens = 8192
				if err := router.SetContextWindow(ref, w); err != nil {
					t.Fatal(err)
				}
			case "qualification":
				router.ClearHarnessQualification(ref)
			case "expired":
				// Simulate elapsed wall time without sleeps. Keep the exact saved
				// qualification equal so only expiry, not a changed identity, fails.
				q.QualifiedAt = time.Now().Add(-2 * time.Hour)
				q.ExpiresAt = time.Now().Add(-time.Hour)
				router.qualifications[ref.Provider+"\x00"+ref.Model] = q
				req.preparedModel.qualification = q
			case "foreign_router":
				router = NewRouter(ref)
				router.RegisterProvider(p)
			case "model":
				req.Model = "other"
			}
			if w, _ := req.PreparedContextWindow(); w != original {
				t.Fatal("captured context window changed")
			}
			var wg sync.WaitGroup
			for i := 0; i < 4; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, err := router.StreamChatModelRef(t.Context(), ref, req)
					if !errors.Is(err, ErrPreparedRequestChanged) {
						t.Errorf("stale request error=%v", err)
					}
				}()
			}
			wg.Wait()
			if calls.Load() != 0 {
				t.Fatalf("%d stale HTTP calls", calls.Load())
			}
		})
	}
}

func TestOfficialMetadataRequiresExactModelAndEndpoint(t *testing.T) {
	for _, tc := range []struct {
		url, model, transport string
		known                 bool
	}{
		{"https://api.deepseek.com/anthropic/v1/messages", "deepseek-v4-flash", HarnessTransportAnthropicMessages, true},
		{"https://api.deepseek.com/v1/chat/completions", "deepseek-flash", HarnessTransportOpenAIChatCompletions, true},
		{"http://127.0.0.1:1234", "deepseek-v4-flash", HarnessTransportAnthropicMessages, false},
		{"https://api.deepseek.com.example.invalid/anthropic", "deepseek-v4-flash", HarnessTransportAnthropicMessages, false},
		{"https://api.deepseek.com/anthropic", "unknown-alias", HarnessTransportAnthropicMessages, false},
		{"https://api.deepseek.com/v1/responses", "deepseek-flash", HarnessTransportOpenAIResponses, false},
	} {
		w := httpModelContextWindow(tc.url, tc.model, tc.transport, nil, true)
		if (w.Source == "provider_model_metadata") != tc.known {
			t.Errorf("%+v: %+v", tc, w)
		}
	}
}
