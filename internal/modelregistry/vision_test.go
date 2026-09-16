package modelregistry

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cyberagent-workbench/internal/imageattachment"
	"cyberagent-workbench/internal/llm"
)

func TestModelVisionDeclarationIsExactAndNeverRequestCustomization(t *testing.T) {
	captured := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		captured <- body
		_, _ = w.Write([]byte(`{"model":"upstream-image","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`))
	}))
	defer server.Close()
	definition := validCustomDefinition(server.URL + "/v1/chat/completions")
	definition.AdvancedConfig = json.RawMessage(`{"model_mapping":{"acme-code":"upstream-image"},"model_capabilities":{"acme-code":{"vision":"supported"}}}`)
	if err := definition.Validate(); err != nil {
		t.Fatal(err)
	}
	runtime, err := NewProviderRequestRuntime(definition, nil)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := llm.NewOpenAICompatibleProvider(llm.OpenAICompatibleConfig{Name: definition.ID, BaseURL: definition.EndpointURL, DefaultModel: definition.DefaultModel, Runtime: runtime})
	if err != nil {
		t.Fatal(err)
	}
	router := llm.NewRouter(llm.ModelRef{Provider: definition.ID, Model: definition.DefaultModel})
	router.RegisterProvider(provider)
	registry := &Registry{router: router}
	if got := registry.DescribeVision(definition.ID, "acme-code"); got.State != llm.VisionSupported || got.Source != "operator_declared" {
		t.Fatalf("exact declaration lost: %+v", got)
	}
	for _, model := range []string{"upstream-image", "acme-code-extra", "gpt-4.1", "claude-image"} {
		if got := registry.DescribeVision(definition.ID, model); got.State != llm.VisionUnknown {
			t.Fatalf("capability guessed for %s: %+v", model, got)
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	metadata, err := imageattachment.Validate(encoded.Bytes(), "image/png", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = router.Chat(t.Context(), "default", llm.ChatRequest{Messages: []llm.Message{{Role: "user", Images: []llm.ImagePart{{MediaType: metadata.MIMEType, Data: encoded.Bytes(), SHA256: metadata.SHA256, Width: metadata.Width, Height: metadata.Height}}}}})
	if err != nil {
		t.Fatal(err)
	}
	body := <-captured
	if body["model"] != "upstream-image" {
		t.Fatal("exact local model mapping changed")
	}
	if _, found := body["model_capabilities"]; found {
		t.Fatal("declaration leaked into provider request")
	}
	before := runtime.BindingDigest()
	definition.AdvancedConfig = json.RawMessage(`{"model_mapping":{"acme-code":"upstream-image"},"model_capabilities":{"acme-code":{"vision":"unsupported"}}}`)
	next, err := NewProviderRequestRuntime(definition, nil)
	if err != nil {
		t.Fatal(err)
	}
	if next.BindingDigest() == before {
		t.Fatal("changed vision declaration reused old generation binding")
	}
	if got := next.(llm.VisionDescriber).DescribeVision("acme-code"); got.State != llm.VisionUnsupported {
		t.Fatal("explicit unsupported was ignored")
	}
	if !provider.SupportsVision("acme-code") {
		t.Fatal("immutable prior runtime was rewritten")
	}
}

func TestModelVisionDeclarationRejectsAmbiguousOrUnconfiguredMetadata(t *testing.T) {
	for _, raw := range []string{
		`{"model_capabilities":true}`,
		`{"model_capabilities":{"acme-code":{"vision":true}}}`,
		`{"model_capabilities":{"acme-code":{"vision":"maybe"}}}`,
		`{"model_capabilities":{"acme-code":{"vision":"supported","tools":true}}}`,
		`{"model_capabilities":{"not-configured":{"vision":"supported"}}}`,
		`{"Model_Capabilities":{"acme-code":{"vision":"supported"}}}`,
		`{"model_capabilities":{"acme-code":{"vision":"supported","vision":"unsupported"}}}`,
	} {
		definition := validCustomDefinition("http://127.0.0.1:18800/v1/chat/completions")
		definition.AdvancedConfig = json.RawMessage(raw)
		if err := definition.Validate(); err == nil {
			t.Fatalf("accepted ambiguous declaration %s", raw)
		}
		if _, err := NewProviderRequestRuntime(definition, nil); err == nil {
			t.Fatalf("runtime admitted ambiguous declaration %s", raw)
		}
	}
	for _, state := range []string{"supported", "unsupported", "unknown"} {
		definition := validCustomDefinition("http://127.0.0.1:18800/v1/chat/completions")
		definition.AdvancedConfig = json.RawMessage(`{"model_capabilities":{"acme-code":{"vision":"` + state + `"}}}`)
		if err := definition.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	definition := validCustomDefinition("http://127.0.0.1:18800/v1/chat/completions")
	runtime, err := NewProviderRequestRuntime(definition, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := runtime.(llm.VisionDescriber).DescribeVision(definition.DefaultModel); got.State != llm.VisionUnknown || strings.Contains(got.Source, "declared") {
		t.Fatal("missing capability became a support assertion")
	}
}
