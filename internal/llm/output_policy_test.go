package llm

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOptionalOutputBudgetReachesHTTPWithoutImplicit1024(t *testing.T) {
	for _, transport := range []string{"chat", "responses", "ollama"} {
		for _, limit := range []int{0, 128} {
			t.Run(fmt.Sprintf("%s/%d", transport, limit), func(t *testing.T) {
				field := "max_tokens"
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
						http.Error(w, "invalid JSON", 400)
						return
					}
					if transport == "responses" {
						field = "max_output_tokens"
					}
					if transport == "ollama" {
						field = "num_predict"
						body, _ = body["options"].(map[string]any)
					}
					value, present := body[field]
					if limit == 0 && present {
						t.Errorf("unspecified output must be omitted, got %s=%v", field, value)
					}
					if limit > 0 && (!present || value != float64(limit)) {
						t.Errorf("explicit output limit changed: %s=%v", field, value)
					}
					w.Header().Set("Content-Type", "application/json")
					switch transport {
					case "chat":
						_, _ = w.Write([]byte(`{"id":"c","model":"test-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`))
					case "responses":
						_, _ = w.Write([]byte(`{"id":"r","object":"response","status":"completed","model":"test-model","output":[{"id":"m","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7}}`))
					case "ollama":
						_, _ = w.Write([]byte(`{"model":"test-model","message":{"role":"assistant","content":"ok"},"done":true,"done_reason":"stop","prompt_eval_count":5,"eval_count":2}`))
					}
				}))
				defer server.Close()
				var provider Provider
				var err error
				switch transport {
				case "chat":
					provider, err = NewOpenAICompatibleProvider(OpenAICompatibleConfig{Name: "test", BaseURL: server.URL, APIKey: "synthetic", DefaultModel: "test-model", HTTPClient: server.Client()})
				case "responses":
					provider, err = NewOpenAIResponsesProvider(OpenAIResponsesConfig{Name: "test", BaseURL: server.URL, APIKey: "synthetic", DefaultModel: "test-model", HTTPClient: server.Client()})
				case "ollama":
					provider, err = NewOllamaProvider(OllamaConfig{BaseURL: server.URL, HTTPClient: server.Client()})
				}
				if err != nil {
					t.Fatal(err)
				}
				_, err = provider.Chat(t.Context(), ChatRequest{Model: "test-model", MaxTokens: limit, Messages: []Message{{Role: "user", Content: "hello"}}})
				if err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestOfficialDeepSeekOutputUsesModelDefault(t *testing.T) {
	provider, err := NewAnthropicCompatibleProvider(AnthropicCompatibleConfig{Name: "deepseek", BaseURL: "https://api.deepseek.com/anthropic", APIKey: "synthetic", DefaultModel: "deepseek-v4-flash", DisableThinking: true})
	if err != nil {
		t.Fatal(err)
	}
	wire, err := provider.toRequest("deepseek-v4-flash", ChatRequest{Model: "deepseek-v4-flash", Messages: []Message{{Role: "user", Content: "hello"}}})
	if err != nil {
		t.Fatal(err)
	}
	if wire.MaxTokens != 8192 {
		t.Fatalf("known non-thinking model default=%d, want 8192", wire.MaxTokens)
	}
	router := NewRouter(ModelRef{Provider: "deepseek", Model: "deepseek-v4-flash"})
	router.RegisterProvider(provider)
	window := router.ContextWindow(router.Resolve("code"))
	if window.MaxOutputTokens <= 4096 || window.DefaultOutputTokens != 8192 {
		t.Fatalf("official model still uses generic limits: %+v", window)
	}
}
