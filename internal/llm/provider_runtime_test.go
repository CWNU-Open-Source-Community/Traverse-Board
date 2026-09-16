package llm

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

type keylessTestRuntime struct{ allow bool }

func (r keylessTestRuntime) ResolveCredential(context.Context) (string, error) { return "", nil }
func (r keylessTestRuntime) MapModel(model string) (string, error)             { return model, nil }
func (r keylessTestRuntime) Apply(string, http.Header, map[string]any) error   { return nil }
func (r keylessTestRuntime) BindingDigest() string                             { return strings.Repeat("a", 64) }
func (r keylessTestRuntime) AllowsKeylessEndpoint(string) bool                 { return r.allow }

type countingKeylessTransport struct{ calls atomic.Int32 }

func (t *countingKeylessTransport) RoundTrip(*http.Request) (*http.Response, error) {
	t.calls.Add(1)
	return nil, errors.New("unexpected HTTP request")
}

func TestHTTPAdaptersRejectEmptyCredentialOutsideBoundLoopbackRuntime(t *testing.T) {
	factories := []struct {
		name  string
		build func(string, HTTPProviderRuntime, *http.Client) (Provider, error)
	}{
		{"chat", func(endpoint string, runtime HTTPProviderRuntime, client *http.Client) (Provider, error) {
			return NewOpenAICompatibleProvider(OpenAICompatibleConfig{
				Name: "local-test", BaseURL: endpoint, Runtime: runtime, HTTPClient: client})
		}},
		{"responses", func(endpoint string, runtime HTTPProviderRuntime, client *http.Client) (Provider, error) {
			return NewOpenAIResponsesProvider(OpenAIResponsesConfig{
				Name: "local-test", BaseURL: endpoint, Runtime: runtime, HTTPClient: client})
		}},
		{"anthropic", func(endpoint string, runtime HTTPProviderRuntime, client *http.Client) (Provider, error) {
			return NewAnthropicCompatibleProvider(AnthropicCompatibleConfig{
				Name: "local-test", BaseURL: endpoint, Runtime: runtime, HTTPClient: client})
		}},
	}
	for _, factory := range factories {
		for _, current := range []struct {
			name, endpoint string
			allow          bool
		}{
			{"external runtime claim", "https://api.example.com/v1", true},
			{"unapproved local runtime", "http://127.0.0.1:1234/v1", false},
		} {
			t.Run(factory.name+"/"+current.name, func(t *testing.T) {
				transport := &countingKeylessTransport{}
				provider, err := factory.build(current.endpoint, keylessTestRuntime{allow: current.allow},
					&http.Client{Transport: transport})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := provider.ListModels(t.Context()); err == nil {
					t.Fatal("model listing ignored credential boundary")
				}
				request := ChatRequest{Messages: []Message{{Role: "user", Content: "hello"}}}
				if _, err := provider.Chat(t.Context(), request); err == nil {
					t.Fatal("chat ignored credential boundary")
				}
				if _, err := provider.StreamChat(t.Context(), request); err == nil {
					t.Fatal("stream ignored credential boundary")
				}
				if transport.calls.Load() != 0 {
					t.Fatal("request crossed the credential boundary")
				}
			})
		}
	}
}
