package modelregistry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"cyberagent-workbench/internal/llm"
)

func TestModelOutputPolicyFollowsWireModelAndStaysLocal(t *testing.T) {
	captured := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		captured <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"wire-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`))
	}))
	defer server.Close()
	d := validCustomDefinition(server.URL + "/v1/chat/completions")
	d.AdvancedConfig = json.RawMessage(`{"model_mapping":{"acme-code":"wire-model"},"model_context_windows":{"wire-model":{"window_tokens":131072,"default_output_tokens":12000,"max_output_tokens":32000},"acme-code":{"window_tokens":32768,"default_output_tokens":1024,"max_output_tokens":4096}}}`)
	runtime, err := newProviderRequestRuntime(d, func(context.Context, string) (string, bool, error) { return "synthetic", true, nil })
	if err != nil {
		t.Fatal(err)
	}
	p, err := llm.NewOpenAICompatibleProvider(llm.OpenAICompatibleConfig{Name: d.ID, BaseURL: d.EndpointURL, DefaultModel: d.DefaultModel, Runtime: runtime, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	ref := llm.ModelRef{Provider: d.ID, Model: d.DefaultModel}
	router := llm.NewRouter(ref)
	router.RegisterProvider(p)
	w := router.ContextWindow(ref)
	if w.DefaultOutputTokens != 12000 || w.MaxOutputTokens != 32000 {
		t.Fatalf("used local alias limits: %+v", w)
	}
	req, err := router.PrepareModelRequest(ref, llm.ChatRequest{Messages: []llm.Message{{Role: "user", Content: "hello"}}})
	if err != nil {
		t.Fatal(err)
	}
	if req.AllowsDefaultOutput() {
		t.Fatal("explicit operator model default would be omitted")
	}
	req.MaxTokens = req.PlannedOutputTokens(w)
	if _, err := router.ChatModelRef(t.Context(), ref, req); err != nil {
		t.Fatal(err)
	}
	body := <-captured
	if body["model"] != "wire-model" || body["max_tokens"] != float64(12000) || body["model_context_windows"] != nil || body["model_mapping"] != nil {
		t.Fatalf("wrong actual wire body: %+v", body)
	}
}

func TestModelOutputPolicyRejectsUnsendableLimits(t *testing.T) {
	for _, raw := range []string{
		`{"m":{"window_tokens":2097152,"default_output_tokens":1100000,"max_output_tokens":1500000}}`,
		`{"m":{"window_tokens":4096,"default_output_tokens":8192,"max_output_tokens":8192}}`,
		`{"m":{"window_tokens":65536,"default_output_tokens":8192,"max_output_tokens":32000,"typo":1}}`,
	} {
		_, err := ValidateAndNormalizeProviderAdvancedConfig(json.RawMessage(`{"model_context_windows":`+raw+`}`), "acme-models")
		if err == nil {
			t.Fatalf("accepted invalid policy %s", raw)
		}
	}
}

func TestOfficialModelOutputDefaultIncludesConfiguredReasoning(t *testing.T) {
	for _, tc := range []struct {
		body string
		want int
	}{
		{`{}`, 65536},
		{`{"reasoning_effort":"max"}`, 131072},
		{`{"thinking":{"type":"disabled"},"reasoning_effort":"max"}`, 131072},
		{`{"reasoning_effort":"none"}`, 8192},
		{`{"reasoning_effort":"low"}`, 65536},
		{`{"reasoning_effort":"minimal"}`, 65536},
		{`{"reasoning_effort":"medium"}`, 65536},
		{`{"reasoning_effort":"xhigh"}`, 65536},
	} {
		d := validCustomDefinition("https://api.deepseek.com/v1/chat/completions")
		d.AdvancedConfig = json.RawMessage(`{"model_mapping":{"acme-code":"deepseek-v4-flash"},"request_body":` + tc.body + `}`)
		runtime, err := newProviderRequestRuntime(d, nil)
		if err != nil {
			t.Fatal(err)
		}
		p, err := llm.NewOpenAICompatibleProvider(llm.OpenAICompatibleConfig{Name: d.ID, BaseURL: d.EndpointURL, DefaultModel: d.DefaultModel, Runtime: runtime})
		if err != nil {
			t.Fatal(err)
		}
		w := p.ModelContextWindow(d.DefaultModel)
		if w.DefaultOutputTokens != tc.want {
			t.Errorf("body=%s window=%+v", tc.body, w)
		}
	}
}
