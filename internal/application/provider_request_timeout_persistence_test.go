package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/modelregistry"
	"cyberagent-workbench/internal/store"
)

func TestProviderRequestTimeoutPersistsAndRejectsInvalidUpdate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if _, sent := body["request_timeout_seconds"]; sent {
			t.Error("persisted local timeout was sent upstream")
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(1200 * time.Millisecond):
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"fixture-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`)
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "provider-timeout.db")
	state, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if state != nil {
			_ = state.Close()
		}
	})
	lookup := func(string) (string, bool) { return "", false }
	registry := modelregistry.New(lookup)
	service, err := application.NewProviderDefinitionService(state, registry)
	if err != nil {
		t.Fatal(err)
	}
	definition := modelregistry.ProviderDefinition{
		Version: modelregistry.ProviderDefinitionVersion, ID: "custom-timeout",
		DisplayName: "Timeout fixture", EndpointURL: server.URL,
		DefaultModel: "fixture-model", Models: []string{"fixture-model"},
		Transport:                 modelregistry.ProviderTransportOpenAIChatCompletions,
		SearchMode:                modelregistry.ProviderSearchModeDisabled,
		NativeWebSearchCapability: modelregistry.NativeWebSearchUnsupported,
		AdvancedConfig:            json.RawMessage(`{"request_timeout_seconds":1}`), Enabled: true,
	}
	created, err := service.Upsert(t.Context(), application.ProviderDefinitionUpsertRequest{
		Version:    application.ProviderDefinitionControlProtocolVersion,
		Definition: definition, Confirm: true,
	})
	if err != nil || !created.RegistryReloaded {
		t.Fatalf("save/reload failed: result=%+v err=%v", created, err)
	}
	generation := registry.Generation()
	definition = created.Definition
	definition.AdvancedConfig = json.RawMessage(`{"request_timeout_seconds":0}`)
	if _, err := service.Upsert(t.Context(), application.ProviderDefinitionUpsertRequest{
		Version:                    application.ProviderDefinitionControlProtocolVersion,
		ExpectedCollectionRevision: 1, Definition: definition, Confirm: true,
	}); apperror.CodeOf(err) != apperror.CodeInvalidArgument {
		t.Fatalf("invalid timeout reached persistence: %v", err)
	}
	if registry.Generation() != generation {
		t.Fatal("rejected timeout update reloaded the Registry")
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	state, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	registry = modelregistry.New(lookup)
	if err := registry.LoadRouteSettings(t.Context(), state); err != nil {
		t.Fatal(err)
	}
	service, err = application.NewProviderDefinitionService(state, registry)
	if err != nil {
		t.Fatal(err)
	}
	listed, err := service.List(t.Context())
	if err != nil || listed.Revision != 1 || len(listed.Providers) != 1 ||
		listed.Providers[0].Revision != 1 || string(listed.Providers[0].AdvancedConfig) != `{"request_timeout_seconds":1}` {
		t.Fatalf("saved timeout/revision changed after rejection or reopen: collection=%+v err=%v", listed, err)
	}
	ref := llm.ModelRef{Provider: definition.ID, Model: definition.DefaultModel}
	request := llm.ChatRequest{Messages: []llm.Message{{Role: "user", Content: "fixture"}}}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	response, err := registry.Router().ChatModelRef(ctx, ref, request)
	failure := llm.NormalizeProviderError(ref.Provider, err)
	if response != nil || failure == nil || failure.Kind != llm.OutcomeRetryable ||
		failure.Reason != llm.ProviderFailureNetwork || ctx.Err() != nil || errors.Is(failure, context.DeadlineExceeded) {
		t.Fatalf("reopened timeout was not applied: response=%+v err=%v ctx=%v", response, err, ctx.Err())
	}
	definition = listed.Providers[0]
	definition.AdvancedConfig = json.RawMessage(`{"request_timeout_seconds":2}`)
	updated, err := service.Upsert(t.Context(), application.ProviderDefinitionUpsertRequest{
		Version:                    application.ProviderDefinitionControlProtocolVersion,
		ExpectedCollectionRevision: 1, Definition: definition, Confirm: true,
	})
	if err != nil || !updated.RegistryReloaded || updated.Collection.Revision != 2 || updated.Definition.Revision != 2 {
		t.Fatalf("timeout update/reload failed: result=%+v err=%v", updated, err)
	}
	response, err = registry.Router().ChatModelRef(t.Context(), ref, request)
	if err != nil || response == nil || response.Text != "ok" {
		t.Fatalf("updated timeout did not apply to next request: response=%+v err=%v", response, err)
	}
}
