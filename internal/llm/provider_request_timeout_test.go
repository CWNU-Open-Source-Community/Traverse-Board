package llm

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestProviderConstructorsValidateTotalRequestTimeout(t *testing.T) {
	constructors := map[string]func(*http.Client) (Provider, error){
		"anthropic": func(client *http.Client) (Provider, error) {
			return NewAnthropicCompatibleProvider(AnthropicCompatibleConfig{
				Name: "fixture", BaseURL: "http://127.0.0.1:1", APIKey: "fixture-only", HTTPClient: client})
		},
		"openai": func(client *http.Client) (Provider, error) {
			return NewOpenAICompatibleProvider(OpenAICompatibleConfig{
				Name: "fixture", BaseURL: "http://127.0.0.1:1", APIKey: "fixture-only", HTTPClient: client})
		},
		"responses": func(client *http.Client) (Provider, error) {
			return NewOpenAIResponsesProvider(OpenAIResponsesConfig{
				Name: "fixture", BaseURL: "http://127.0.0.1:1", APIKey: "fixture-only", HTTPClient: client})
		},
		"ollama": func(client *http.Client) (Provider, error) {
			return NewOllamaProvider(OllamaConfig{
				Name: "fixture", BaseURL: "http://127.0.0.1:1", HTTPClient: client})
		},
	}
	for name, construct := range constructors {
		t.Run(name, func(t *testing.T) {
			for _, timeout := range []time.Duration{-time.Nanosecond, MaxProviderRequestTimeout + time.Nanosecond,
				time.Duration(1<<63 - 1)} {
				if _, err := construct(&http.Client{Timeout: timeout}); err == nil {
					t.Errorf("invalid timeout %s was silently accepted", timeout)
				}
			}
			for _, timeout := range []time.Duration{0, time.Millisecond, 90 * time.Second, MaxProviderRequestTimeout} {
				source := &http.Client{Timeout: timeout}
				provider, err := construct(source)
				if err != nil {
					t.Fatal(err)
				}
				var client *http.Client
				switch p := provider.(type) {
				case *AnthropicCompatibleProvider:
					client = p.client
				case *OpenAICompatibleProvider:
					client = p.client
				case *OpenAIResponsesProvider:
					client = p.client
				case *OllamaProvider:
					client = p.client
				}
				want := timeout
				if want == 0 {
					want = DefaultProviderRequestTimeout
				}
				if client == source || client.Timeout != want || source.Timeout != timeout {
					t.Fatalf("timeout choice or source client changed: got=%s want=%s source=%s", client.Timeout, want, source.Timeout)
				}
			}
		})
	}
}

func TestLongProviderRequestStillStopsOnCallerCancellation(t *testing.T) {
	for _, adapter := range []string{"anthropic", "openai", "responses", "ollama"} {
		t.Run(adapter, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, boundaryPartialStream(adapter))
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			provider := boundaryProvider(t, adapter, server.URL, &http.Client{Timeout: 120 * time.Second})
			chunks, err := provider.StreamChat(ctx, boundaryRequest())
			if err != nil {
				t.Fatal(err)
			}
			var stopped time.Time
			for chunk := range chunks {
				if chunk.Done || len(chunk.ToolCalls) != 0 {
					t.Fatal("cancelled partial stream yielded an accepted result")
				}
				if chunk.Text != "" && stopped.IsZero() {
					stopped = time.Now()
					cancel()
				}
			}
			if stopped.IsZero() || ctx.Err() != context.Canceled || time.Since(stopped) > time.Second {
				t.Fatalf("long request did not stop promptly after partial output: stopped=%v ctx=%v", stopped, ctx.Err())
			}
		})
	}
}
