package modelregistry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/llm"
)

func TestProviderRequestTimeoutConfigIsBoundedAndCanonical(t *testing.T) {
	for _, seconds := range []int{1, 60, 120, 1800} {
		definition := validCustomDefinition("http://127.0.0.1:1/v1/chat/completions")
		definition.AdvancedConfig = json.RawMessage(fmt.Sprintf(`{"request_timeout_seconds":%d}`, seconds))
		runtime, err := newProviderRequestRuntime(definition, nil)
		if err != nil || runtime.requestTimeout != time.Duration(seconds)*time.Second {
			t.Fatalf("configured timeout %d: runtime=%+v err=%v", seconds, runtime, err)
		}
	}
	definition := validCustomDefinition("http://127.0.0.1:1/v1/chat/completions")
	runtime, err := newProviderRequestRuntime(definition, nil)
	if err != nil || runtime.requestTimeout != llm.DefaultProviderRequestTimeout {
		t.Fatalf("default request timeout changed: runtime=%+v err=%v", runtime, err)
	}
	for _, value := range []string{`0`, `-1`, `1801`, `9223372036854775808`, `1.5`, `1e3`, `null`, `true`, `"120"`, `{}`, `[]`} {
		raw := json.RawMessage(`{"request_timeout_seconds":` + value + `}`)
		if _, err := ValidateAndNormalizeProviderAdvancedConfig(raw, definition.ID); err == nil {
			t.Errorf("invalid request timeout was accepted: %s", value)
		}
	}
	for _, key := range []string{"Request_timeout_seconds", "REQUEST_TIMEOUT_SECONDS"} {
		if _, err := ValidateAndNormalizeProviderAdvancedConfig(json.RawMessage(`{"`+key+`":120}`), definition.ID); err == nil {
			t.Errorf("noncanonical timeout key was left inert: %s", key)
		}
	}
}

func TestBuiltinProviderTimeoutEnvironmentValidatesEachProvider(t *testing.T) {
	providers := []struct{ name, key, endpoint, model, timeout string }{
		{"mimo", "MIMO_API_KEY", "MIMO_BASE_URL", "MIMO_MODEL", "MIMO_TIMEOUT_SECONDS"},
		{"deepseek", "DEEPSEEK_API_KEY", "DEEPSEEK_BASE_URL", "DEEPSEEK_MODEL", "DEEPSEEK_TIMEOUT_SECONDS"},
		{"anthropic", "CYBERAGENT_ANTHROPIC_API_KEY", "CYBERAGENT_ANTHROPIC_BASE_URL", "CYBERAGENT_ANTHROPIC_MODEL", "CYBERAGENT_ANTHROPIC_TIMEOUT_SECONDS"},
		{"openai", "CYBERAGENT_OPENAI_API_KEY", "CYBERAGENT_OPENAI_BASE_URL", "CYBERAGENT_OPENAI_MODEL", "CYBERAGENT_OPENAI_TIMEOUT_SECONDS"},
		{"ollama", "", "CYBERAGENT_OLLAMA_BASE_URL", "CYBERAGENT_OLLAMA_MODEL", "CYBERAGENT_OLLAMA_TIMEOUT_SECONDS"},
	}
	for _, provider := range providers {
		t.Run(provider.name, func(t *testing.T) {
			values := map[string]string{provider.endpoint: "http://127.0.0.1:1", provider.model: "fixture-model"}
			if provider.key != "" {
				values[provider.key] = "fixture-only"
			}
			lookup := func(name string) (string, bool) { value, found := values[name]; return value, found }
			for _, value := range []string{"1", "60", "120", "1800"} {
				values[provider.timeout] = value
				registry := New(lookup)
				availability, found := providerByName(registry.Snapshot(), provider.name)
				if !found || availability.Status != ProviderAvailable || availability.ConfigurationError {
					t.Fatalf("valid timeout %s disabled Provider: %+v", value, availability)
				}
			}
			for _, value := range []string{"", "0", "-1", "1801", "9223372036854775808", "1.5", "private-timeout-canary"} {
				values[provider.timeout] = value
				registry := New(lookup)
				availability, found := providerByName(registry.Snapshot(), provider.name)
				if !found || availability.Status != ProviderInvalidConfiguration || !availability.ConfigurationError {
					t.Fatalf("invalid timeout %q did not fail closed: %+v", value, availability)
				}
				mock, found := providerByName(registry.Snapshot(), "mock")
				if !found || mock.Status != ProviderAvailable {
					t.Fatal("one invalid timeout disabled unrelated Mock Provider")
				}
				encoded, _ := json.Marshal(registry.Snapshot())
				if strings.Contains(string(encoded), "private-timeout-canary") {
					t.Fatal("invalid configuration escaped into public availability")
				}
			}
		})
	}
}

// Check the existing definition/reload production path against a real HTTP
// server. Timeout is local policy and must never become an upstream body field.
func TestCustomProviderRequestTimeoutAppliesAcrossReload(t *testing.T) {
	for _, transport := range []string{ProviderTransportOpenAIChatCompletions,
		ProviderTransportOpenAIResponses, ProviderTransportAnthropicMessages} {
		t.Run(transport, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				if _, sent := body["request_timeout_seconds"]; sent {
					t.Error("local request timeout was sent to upstream")
				}
				select {
				case <-r.Context().Done():
					return
				case <-time.After(1200 * time.Millisecond):
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, timeoutFixtureResponse(transport))
			}))
			defer server.Close()
			definition := validCustomDefinition(server.URL)
			definition.Transport = transport
			definition.AdvancedConfig = json.RawMessage(`{"request_timeout_seconds":1}`)
			settings := routeSettings{ProviderDefinitionsSettingKey: providerDefinitionSetting(t, definition, 1)}
			registry := New(func(string) (string, bool) { return "", false })
			if err := registry.LoadRouteSettings(t.Context(), settings); err != nil {
				t.Fatal(err)
			}
			ref := llm.ModelRef{Provider: definition.ID, Model: definition.DefaultModel}
			request := llm.ChatRequest{Messages: []llm.Message{{Role: "user", Content: "fixture"}}}
			response, err := registry.Router().ChatModelRef(t.Context(), ref, request)
			failure := llm.NormalizeProviderError(ref.Provider, err)
			if response != nil || failure == nil || failure.Kind != llm.OutcomeRetryable ||
				failure.Reason != llm.ProviderFailureNetwork || t.Context().Err() != nil ||
				errors.Is(failure, context.DeadlineExceeded) {
				t.Fatalf("configured total timeout lost classification: response=%+v err=%v", response, err)
			}
			definition.AdvancedConfig = json.RawMessage(`{"request_timeout_seconds":2}`)
			definition.Revision = 2
			encoded, err := EncodeProviderDefinitionCollection(ProviderDefinitionCollection{
				Version: ProviderDefinitionCollectionVersion, Revision: 2,
				Providers: []ProviderDefinition{definition},
			})
			if err != nil {
				t.Fatal(err)
			}
			settings[ProviderDefinitionsSettingKey] = encoded
			if _, err := registry.Reload(t.Context(), settings); err != nil {
				t.Fatal(err)
			}
			response, err = registry.Router().ChatModelRef(t.Context(), ref, request)
			if err != nil || response == nil || response.Text != "ok" {
				t.Fatalf("longer timeout failed after reload: response=%+v err=%v", response, err)
			}
		})
	}
}

func timeoutFixtureResponse(transport string) string {
	switch transport {
	case ProviderTransportOpenAIChatCompletions:
		return `{"model":"acme-code","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`
	case ProviderTransportOpenAIResponses:
		return `{"id":"resp_timeout","object":"response","status":"completed","model":"acme-code","output":[{"id":"msg_timeout","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}`
	case ProviderTransportAnthropicMessages:
		return `{"model":"acme-code","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":1}}`
	}
	return ""
}
