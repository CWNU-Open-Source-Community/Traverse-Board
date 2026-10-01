package llm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

type metadataTestTransport func(*http.Request) (*http.Response, error)

func (f metadataTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestOpenAILocalDefaultReachesActualWireWithoutRunBudget(t *testing.T) {
	for _, transport := range []string{"chat", "responses"} {
		t.Run(transport, func(t *testing.T) {
			field := "max_tokens"
			if transport == "responses" {
				field = "max_output_tokens"
			}
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body[field] != float64(16384) {
					t.Errorf("default not sent on actual wire: %s=%v", field, body[field])
				}
				w.Header().Set("Content-Type", "application/json")
				if transport == "responses" {
					_, _ = w.Write([]byte(`{"id":"r","object":"response","status":"completed","model":"gpt-6.1-sol","output":[{"id":"m","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7}}`))
				} else {
					_, _ = w.Write([]byte(`{"id":"c","model":"gpt-6.1-sol","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`))
				}
			}))
			defer srv.Close()
			destination, _ := url.Parse(srv.URL)
			client := &http.Client{Transport: metadataTestTransport(func(r *http.Request) (*http.Response, error) {
				copied := r.Clone(r.Context())
				u := *r.URL
				u.Scheme, u.Host = destination.Scheme, destination.Host
				copied.URL = &u
				return http.DefaultTransport.RoundTrip(copied)
			})}
			var p Provider
			var err error
			if transport == "responses" {
				p, err = NewOpenAIResponsesProvider(OpenAIResponsesConfig{Name: "official-openai", BaseURL: "https://api.openai.com/v1/responses", APIKey: "synthetic-key", DefaultModel: "gpt-6.1-sol", HTTPClient: client})
			} else {
				p, err = NewOpenAICompatibleProvider(OpenAICompatibleConfig{Name: "official-openai", BaseURL: "https://api.openai.com/v1/chat/completions", APIKey: "synthetic-key", DefaultModel: "gpt-6.1-sol", HTTPClient: client})
			}
			if err != nil {
				t.Fatal(err)
			}
			ref := ModelRef{Provider: "official-openai", Model: "gpt-6.1-sol"}
			router := NewRouter(ref)
			router.RegisterProvider(p)
			req, err := router.PrepareModelRequest(ref, ChatRequest{Messages: []Message{{Role: "user", Content: "hello"}}})
			if err != nil {
				t.Fatal(err)
			}
			if req.AllowsDefaultOutput() {
				t.Fatal("local request default would be omitted")
			}
			// Same no-budget preparation branch used by RunSupervisor.
			window, _ := req.PreparedContextWindow()
			if req.MaxTokens <= 0 && !req.AllowsDefaultOutput() {
				req.MaxTokens = req.PlannedOutputTokens(window)
			}
			if _, err = router.ChatModelRef(t.Context(), ref, req); err != nil || calls != 1 {
				t.Fatalf("actual HTTP result err=%v calls=%d", err, calls)
			}
		})
	}
}

func TestOfficialOpenAIModelOutputMetadataAndOriginBinding(t *testing.T) {
	for _, transport := range []string{HarnessTransportOpenAIChatCompletions, HarnessTransportOpenAIResponses} {
		for _, model := range []string{"gpt-6-astra", "gpt-6.1-sol", "gpt-6-luna"} {
			w := httpModelContextWindow("https://api.openai.com/v1", model, transport, nil, false)
			if w.Validate() != nil || w.WindowTokens != 1_050_000 || w.DefaultOutputTokens != 16_384 || w.MaxOutputTokens != 128_000 || w.Source != "local_model_default" {
				t.Fatalf("metadata %s %s: %#v", transport, model, w)
			}
		}
	}
	for _, endpoint := range []string{"http://127.0.0.1:1234/v1/responses", "https://api.openai.com.example.org/v1/responses", "https://api.openai.com:8443/v1/responses", "https://api.openai.com/other/responses"} {
		if w := httpModelContextWindow(endpoint, "gpt-6-astra", HarnessTransportOpenAIResponses, nil, false); w.Source != "conservative_default" {
			t.Fatalf("untrusted origin received metadata: %s %#v", endpoint, w)
		}
	}
}
