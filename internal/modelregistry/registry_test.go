package modelregistry

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"cyberagent-workbench/internal/llm"
)

type routeSettings map[string]string

func (s routeSettings) GetProviderSetting(_ context.Context, key string) (string, bool, error) {
	value, found := s[key]
	return value, found, nil
}

func (s routeSettings) SetProviderSetting(_ context.Context, key string, value string) error {
	s[key] = value
	return nil
}

type failingRouteSettings struct{}

func (failingRouteSettings) GetProviderSetting(context.Context, string) (string, bool, error) {
	return "", false, errors.New("route store unavailable")
}

type orderedRouteWriter struct {
	registry *Registry
	values   map[string]string
}

type blockingRouteWriter struct {
	mu            sync.Mutex
	values        []string
	firstEntered  chan struct{}
	secondEntered chan struct{}
	releaseFirst  chan struct{}
}

func (w *blockingRouteWriter) SetProviderSetting(_ context.Context, _ string,
	value string,
) error {
	w.mu.Lock()
	ordinal := len(w.values)
	w.values = append(w.values, value)
	w.mu.Unlock()
	if ordinal == 0 {
		close(w.firstEntered)
		<-w.releaseFirst
	} else if ordinal == 1 {
		close(w.secondEntered)
	}
	return nil
}

func (w *orderedRouteWriter) SetProviderSetting(_ context.Context, key string, value string) error {
	if current := w.registry.Router().Resolve("code"); current.Provider != "mock" ||
		current.Model != "mock-code" {
		return errors.New("route changed before durable setting")
	}
	w.values[key] = value
	return nil
}

func TestRegistryBuildsRedactedEnvironmentAvailabilityAndRoutes(t *testing.T) {
	secret := "sk-" + strings.Repeat("z", 48)
	values := map[string]string{
		"MIMO_API_KEY": secret,
		"MIMO_MODEL":   "mimo-test-model",
	}
	registry := New(func(name string) (string, bool) {
		value, found := values[name]
		return value, found
	})
	if err := registry.LoadRouteSettings(context.Background(), routeSettings{
		"route.code": "mimo/mimo-test-model",
	}); err != nil {
		t.Fatal(err)
	}
	snapshot := registry.Snapshot()
	if snapshot.ProtocolVersion != ProtocolVersion || len(snapshot.Providers) != 6 ||
		len(snapshot.Routes) != len(routeNames) {
		t.Fatalf("unexpected registry snapshot: %#v", snapshot)
	}
	var mimo ProviderAvailability
	for _, provider := range snapshot.Providers {
		if provider.Name == "mimo" {
			mimo = provider
		}
	}
	if mimo.Status != ProviderAvailable || mimo.CredentialSource != "environment" ||
		len(mimo.Models) != 1 || mimo.Models[0] != "mimo-test-model" {
		t.Fatalf("unexpected Mimo availability: %#v", mimo)
	}
	var ollama ProviderAvailability
	for _, provider := range snapshot.Providers {
		if provider.Name == "ollama" {
			ollama = provider
		}
	}
	// Without an explicit loopback endpoint the keyless provider stays off;
	// the registry must never default-enable a local daemon.
	if ollama.Status != ProviderNotConfigured || ollama.Kind != ProviderKindOllama ||
		len(ollama.Models) != 0 || ollama.CredentialSource != "none" {
		t.Fatalf("unexpected Ollama availability: %#v", ollama)
	}
	for _, route := range snapshot.Routes {
		if route.Name == "code" && (!route.Available || route.Provider != "mimo" ||
			route.Model != "mimo-test-model") {
			t.Fatalf("unexpected code route: %#v", route)
		}
	}
	if strings.Contains(strings.ToLower(strings.TrimSpace(snapshotText(snapshot))), secret) {
		t.Fatal("provider snapshot exposed an API key")
	}
}

func TestRegistryOllamaEnvironmentIsExplicitAndLoopbackOnly(t *testing.T) {
	newOllamaRegistry := func(values map[string]string) *Registry {
		return New(func(name string) (string, bool) {
			value, found := values[name]
			return value, found
		})
	}
	ollamaFrom := func(registry *Registry) ProviderAvailability {
		for _, provider := range registry.Snapshot().Providers {
			if provider.Name == "ollama" {
				return provider
			}
		}
		return ProviderAvailability{}
	}
	t.Run("explicit loopback endpoint enables the keyless provider", func(t *testing.T) {
		registry := newOllamaRegistry(map[string]string{
			"CYBERAGENT_OLLAMA_BASE_URL": "http://127.0.0.1:11434",
			"CYBERAGENT_OLLAMA_MODEL":    "llama3.2:3b",
		})
		ollama := ollamaFrom(registry)
		if ollama.Status != ProviderAvailable || ollama.Kind != ProviderKindOllama ||
			len(ollama.Models) != 1 || ollama.Models[0] != "llama3.2:3b" ||
			ollama.CredentialSource != "none" || !ollama.NetworkRequired ||
			len(ollama.Harnesses) != 1 ||
			ollama.Harnesses[0].TransportProtocol != llm.HarnessTransportOllamaChat {
			t.Fatalf("unexpected Ollama availability: %#v", ollama)
		}
	})
	t.Run("non-loopback endpoint is invalid configuration", func(t *testing.T) {
		registry := newOllamaRegistry(map[string]string{
			"CYBERAGENT_OLLAMA_BASE_URL": "http://192.168.1.20:11434",
			"CYBERAGENT_OLLAMA_MODEL":    "llama3.2:3b",
		})
		ollama := ollamaFrom(registry)
		if ollama.Status != ProviderInvalidConfiguration || !ollama.ConfigurationError {
			t.Fatalf("non-loopback endpoint was not rejected: %#v", ollama)
		}
	})
	t.Run("endpoint without model is invalid configuration", func(t *testing.T) {
		registry := newOllamaRegistry(map[string]string{
			"CYBERAGENT_OLLAMA_BASE_URL": "http://127.0.0.1:11434",
		})
		ollama := ollamaFrom(registry)
		if ollama.Status != ProviderInvalidConfiguration {
			t.Fatalf("endpoint without model was not rejected: %#v", ollama)
		}
	})
}

func TestRegistryOllamaRouteSelectionProbesCapabilities(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/tags":
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"models": []map[string]any{{"name": "tooler:7b", "model": "tooler:7b",
					"capabilities": []string{"completion"}}},
			})
		case "/api/show":
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"capabilities": []string{"completion", "tools"},
				"model_info": map[string]any{
					"llama.context_length": 8192.0,
				},
			})
		default:
			t.Errorf("unexpected path %s", request.URL.Path)
		}
	}))
	defer server.Close()
	registry := New(func(name string) (string, bool) {
		switch name {
		case "CYBERAGENT_OLLAMA_BASE_URL":
			return server.URL, true
		case "CYBERAGENT_OLLAMA_MODEL":
			return "tooler:7b", true
		default:
			return "", false
		}
	})
	selected, err := registry.SelectRoute(context.Background(), routeSettings{},
		"code", "ollama", "tooler:7b")
	if err != nil {
		t.Fatal(err)
	}
	if !selected.Available || selected.HarnessReady {
		// HarnessReady stays false until the full qualification flow runs;
		// the capability probe only establishes the native tool strategy.
		t.Fatalf("unexpected route selection: %#v", selected)
	}
	ref := llm.ModelRef{Provider: "ollama", Model: "tooler:7b"}
	profile, err := registry.Router().HarnessProfile(ref)
	if err != nil {
		t.Fatal(err)
	}
	if profile.ToolStrategy != llm.HarnessToolStrategyNative ||
		profile.JSONStrategy != llm.HarnessJSONStrategyNative ||
		profile.TransportProtocol != llm.HarnessTransportOllamaChat {
		t.Fatalf("probed harness profile is wrong: %#v", profile)
	}
	window := registry.Router().ContextWindow(ref)
	if window.WindowTokens != 8192 || window.Source != "ollama_probe" {
		t.Fatalf("probed context window was not registered: %#v", window)
	}
}


func TestRegistryBootstrapsSystemCredentialWithoutProjectingIt(t *testing.T) {
	secret := "system-provider-key-0123456789"
	registry, err := newRegistry(func(string) (string, bool) { return "", false },
		func(_ context.Context, provider string) (string, bool, error) {
			if provider == "mimo" {
				return secret, true, nil
			}
			return "", false, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := registry.Snapshot()
	var mimo ProviderAvailability
	for _, provider := range snapshot.Providers {
		if provider.Name == "mimo" {
			mimo = provider
		}
	}
	if mimo.Status != ProviderAvailable || mimo.CredentialSource != "system" ||
		!contains(registry.Router().ProviderNames(), "mimo") ||
		strings.Contains(snapshotText(snapshot), secret) {
		t.Fatalf("system credential bootstrap violated its projection boundary: %#v", mimo)
	}
}

func TestRegistryContainsSystemCredentialReadFailureToOneProvider(t *testing.T) {
	registry, err := newRegistry(func(string) (string, bool) { return "", false },
		func(_ context.Context, provider string) (string, bool, error) {
			if provider == "mimo" {
				return "", false, errors.New("credential store unavailable")
			}
			return "", false, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := registry.Snapshot()
	for _, provider := range snapshot.Providers {
		if provider.Name == "mimo" &&
			(provider.Status != ProviderInvalidConfiguration ||
				provider.CredentialSource != "system" || !provider.ConfigurationError) {
			t.Fatalf("system credential failure escaped its Provider boundary: %#v", provider)
		}
	}
	if !contains(registry.Router().ProviderNames(), "mock") {
		t.Fatal("system credential failure disabled the local Provider")
	}
}

func TestRegistryReloadAtomicallyAdvancesGenerationAndContainsProviders(t *testing.T) {
	var mu sync.RWMutex
	credentials := map[string]string{}
	registry, err := newRegistry(func(string) (string, bool) { return "", false },
		func(ctx context.Context, provider string) (string, bool, error) {
			if err := ctx.Err(); err != nil {
				return "", false, err
			}
			mu.RLock()
			value, found := credentials[provider]
			mu.RUnlock()
			return value, found, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if registry.Generation() != 1 || registry.Snapshot().Generation != 1 {
		t.Fatalf("initial registry generation=%d", registry.Generation())
	}
	mu.Lock()
	credentials["mimo"] = "reload-provider-key-0123456789"
	mu.Unlock()
	result, err := registry.Reload(t.Context(), routeSettings{
		"route.code": "mimo/" + DefaultMimoModel,
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := registry.Snapshot()
	if result.ProtocolVersion != ReloadProtocolVersion || !result.Reloaded ||
		result.Generation != 2 || snapshot.Generation != 2 ||
		registry.Router().Resolve("code").Provider != "mimo" ||
		!contains(registry.Router().ProviderNames(), "mock") ||
		!contains(registry.Router().ProviderNames(), "mimo") {
		t.Fatalf("registry reload did not install one generation: result=%#v snapshot=%#v",
			result, snapshot)
	}
	mu.Lock()
	delete(credentials, "mimo")
	mu.Unlock()
	result, err = registry.Reload(t.Context(), routeSettings{
		"route.code": "mimo/" + DefaultMimoModel,
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot = registry.Snapshot()
	if result.Generation != 3 || snapshot.Generation != 3 ||
		contains(registry.Router().ProviderNames(), "mimo") ||
		!contains(registry.Router().ProviderNames(), "mock") {
		t.Fatalf("registry removal widened or disabled unrelated Providers: %#v", snapshot)
	}
	for _, route := range snapshot.Routes {
		if route.Name == "code" && route.Available {
			t.Fatalf("route remained available after its Provider was removed: %#v", route)
		}
	}
}

func TestRegistryReloadFailureLeavesCurrentGenerationUntouched(t *testing.T) {
	registry := New(nil)
	before := registry.Snapshot()
	if _, err := registry.Reload(t.Context(), failingRouteSettings{}); err == nil {
		t.Fatal("failed route reload was reported as success")
	}
	after := registry.Snapshot()
	if after.Generation != before.Generation ||
		registry.Router().Resolve("code") != (llm.ModelRef{Provider: "mock", Model: "mock-code"}) ||
		len(after.Providers) != len(before.Providers) {
		t.Fatalf("failed reload changed the active generation: before=%#v after=%#v",
			before, after)
	}
}

func TestRegistryReloadCredentialReadFailureKeepsCurrentGeneration(t *testing.T) {
	fail := false
	registry, err := newRegistry(func(string) (string, bool) { return "", false },
		func(_ context.Context, provider string) (string, bool, error) {
			if fail && provider == "mimo" {
				return "", false, errors.New("credential manager unavailable")
			}
			if provider == "mimo" {
				return "credential-key-0123456789", true, nil
			}
			return "", false, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	before := registry.Snapshot()
	fail = true
	if _, err := registry.Reload(t.Context(), routeSettings{}); err == nil {
		t.Fatal("credential read failure was installed as a new generation")
	}
	after := registry.Snapshot()
	if after.Generation != before.Generation ||
		!contains(registry.Router().ProviderNames(), "mimo") {
		t.Fatalf("credential read failure replaced active generation: before=%#v after=%#v",
			before, after)
	}
}

func TestRegistryProjectsNoCredentialSourceWhenUnconfigured(t *testing.T) {
	registry := New(func(string) (string, bool) { return "", false })
	for _, provider := range registry.Snapshot().Providers {
		if provider.Name != "mock" && provider.CredentialSource != "none" {
			t.Fatalf("unconfigured Provider projected a false credential source: %#v", provider)
		}
	}
}

func TestRegistryMarksInvalidAndUnavailableConfigurationWithoutRegisteringIt(t *testing.T) {
	values := map[string]string{
		"DEEPSEEK_API_KEY": " invalid-key ",
	}
	registry := New(func(name string) (string, bool) {
		value, found := values[name]
		return value, found
	})
	snapshot := registry.Snapshot()
	for _, provider := range snapshot.Providers {
		if provider.Name == "deepseek" &&
			(provider.Status != ProviderInvalidConfiguration || !provider.ConfigurationError) {
			t.Fatalf("invalid provider was not projected safely: %#v", provider)
		}
	}
	if contains(registry.Router().ProviderNames(), "deepseek") {
		t.Fatal("invalid provider was registered")
	}
}

func TestRegistryNeverProjectsSecretLikeModelOrRouteIdentifiers(t *testing.T) {
	secret := "sk-" + strings.Repeat("q", 48)
	registry := New(func(name string) (string, bool) {
		values := map[string]string{
			"MIMO_API_KEY": "provider-key-for-model-redaction-test",
			"MIMO_MODEL":   secret,
		}
		value, found := values[name]
		return value, found
	})
	registry.Router().SetRoute("code", llm.ModelRef{Provider: "mock", Model: secret})
	snapshot := registry.Snapshot()
	if strings.Contains(snapshotText(snapshot), secret) {
		t.Fatal("model availability projected a secret-like model identifier")
	}
	for _, provider := range snapshot.Providers {
		if provider.Name == "mimo" && (provider.Status != ProviderInvalidConfiguration ||
			!provider.ConfigurationError || len(provider.Models) != 0) {
			t.Fatalf("secret-like model configuration was not rejected: %#v", provider)
		}
	}
	for _, route := range snapshot.Routes {
		if route.Name == "code" && (route.Model != "redacted" || route.Available) {
			t.Fatalf("secret-like route was not closed: %#v", route)
		}
	}
}

func TestRegistryRouteSettingFailureIsReturned(t *testing.T) {
	registry := New(nil)
	if err := registry.LoadRouteSettings(context.Background(), failingRouteSettings{}); err == nil {
		t.Fatal("expected route setting failure")
	}
}

func TestRegistrySelectRoutePersistsBeforeConcurrentRouterUpdate(t *testing.T) {
	registry := New(nil)
	writer := &orderedRouteWriter{registry: registry, values: map[string]string{}}
	selected, err := registry.SelectRoute(context.Background(), writer,
		"code", "mock", "mock-fast")
	if err != nil {
		t.Fatal(err)
	}
	if writer.values["route.code"] != "mock/mock-fast" || !selected.Available ||
		selected.Name != "code" || selected.Provider != "mock" || selected.Model != "mock-fast" {
		t.Fatalf("unexpected persisted route selection: %#v %#v", writer.values, selected)
	}
	if current := registry.Router().Resolve("code"); current.Provider != "mock" ||
		current.Model != "mock-fast" {
		t.Fatalf("router was not updated: %#v", current)
	}
	if _, err := registry.SelectRoute(context.Background(), writer,
		"unknown", "mock", "mock-fast"); err == nil {
		t.Fatal("unsupported route was accepted")
	}
	if _, err := registry.SelectRoute(context.Background(), writer,
		"code", "missing", "model"); err == nil {
		t.Fatal("unavailable Provider was accepted")
	}
}

func TestRegistrySelectRouteSerializesDurableAndMemoryUpdates(t *testing.T) {
	registry := New(nil)
	writer := &blockingRouteWriter{firstEntered: make(chan struct{}),
		secondEntered: make(chan struct{}), releaseFirst: make(chan struct{})}
	errorsSeen := make(chan error, 2)
	go func() {
		_, err := registry.SelectRoute(context.Background(), writer,
			"code", "mock", "mock-fast")
		errorsSeen <- err
	}()
	<-writer.firstEntered
	secondStarted := make(chan struct{})
	go func() {
		close(secondStarted)
		_, err := registry.SelectRoute(context.Background(), writer,
			"code", "mock", "mock-code")
		errorsSeen <- err
	}()
	<-secondStarted
	select {
	case <-writer.secondEntered:
		t.Fatal("second route persistence overtook the first in-memory update")
	case <-time.After(50 * time.Millisecond):
	}
	close(writer.releaseFirst)
	for range 2 {
		if err := <-errorsSeen; err != nil {
			t.Fatal(err)
		}
	}
	writer.mu.Lock()
	values := append([]string(nil), writer.values...)
	writer.mu.Unlock()
	if len(values) != 2 || values[0] != "mock/mock-fast" ||
		values[1] != "mock/mock-code" {
		t.Fatalf("route persistence order=%#v", values)
	}
	if current := registry.Router().Resolve("code"); current.Provider != "mock" ||
		current.Model != "mock-code" {
		t.Fatalf("durable and in-memory order diverged: %#v", current)
	}
}

func TestRegistryDiagnosticReturnsOnlyBoundedConnectivityFacts(t *testing.T) {
	registry := New(nil)
	result, err := registry.Diagnose(context.Background(), "mock", "mock-fast")
	if err != nil {
		t.Fatal(err)
	}
	if result.ProtocolVersion != DiagnosticProtocolVersion ||
		result.Status != DiagnosticReachable || result.Outcome != string(llm.OutcomeSuccess) ||
		result.FailureReason != llm.ProviderFailureNone ||
		result.Provider != "mock" || result.Model != "mock-fast" ||
		result.NetworkRequestAttempted || !result.ModelCalled || result.ToolCalled ||
		result.ResponseContentReturned || result.DurationMillis < 0 {
		t.Fatalf("unexpected diagnostic projection: %#v", result)
	}
	unconfigured, err := registry.Diagnose(context.Background(), "openai", DefaultOpenAIModel)
	if err != nil || unconfigured.Status != DiagnosticUnreachable ||
		unconfigured.Outcome != string(llm.OutcomePermanent) ||
		unconfigured.FailureReason != llm.ProviderFailureNotConfigured ||
		unconfigured.NetworkRequestAttempted || unconfigured.ModelCalled ||
		unconfigured.ToolCalled || unconfigured.ResponseContentReturned {
		t.Fatalf("unexpected unconfigured Provider diagnostic: %#v err=%v", unconfigured, err)
	}
	if _, err := registry.Diagnose(context.Background(), "unknown", DefaultOpenAIModel); err == nil {
		t.Fatal("unknown Provider diagnostic was accepted")
	}
}

func TestRegistryRegistersOpenAIEnvironmentAndSystemCredential(t *testing.T) {
	values := map[string]string{
		"CYBERAGENT_OPENAI_API_KEY": "test-openai-key-0123456789",
		"CYBERAGENT_OPENAI_MODEL":   "openai-test-model",
	}
	registry := New(func(name string) (string, bool) {
		value, found := values[name]
		return value, found
	})
	snapshot := registry.Snapshot()
	var openai ProviderAvailability
	for _, provider := range snapshot.Providers {
		if provider.Name == "openai" {
			openai = provider
		}
	}
	if openai.Kind != ProviderKindOpenAICompatible || openai.Status != ProviderAvailable ||
		openai.CredentialSource != "environment" || len(openai.Models) != 1 ||
		openai.Models[0] != "openai-test-model" || len(openai.Harnesses) != 1 ||
		openai.Harnesses[0].TransportProtocol != llm.HarnessTransportOpenAIChatCompletions ||
		!contains(registry.Router().ProviderNames(), "openai") ||
		strings.Contains(snapshotText(snapshot), values["CYBERAGENT_OPENAI_API_KEY"]) {
		t.Fatalf("unexpected OpenAI registry projection: %#v", openai)
	}

	registry, err := newRegistry(func(string) (string, bool) { return "", false },
		func(_ context.Context, provider string) (string, bool, error) {
			if provider == "openai" {
				return "system-openai-key-0123456789", true, nil
			}
			return "", false, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	foundSystemOpenAI := false
	for _, provider := range registry.Snapshot().Providers {
		if provider.Name == "openai" {
			foundSystemOpenAI = true
			if provider.Status != ProviderAvailable || provider.CredentialSource != "system" ||
				!contains(registry.Router().ProviderNames(), "openai") {
				t.Fatalf("system OpenAI credential did not register: %#v", provider)
			}
		}
	}
	if !foundSystemOpenAI {
		t.Fatal("system OpenAI Provider was not projected")
	}
}

func TestUnconfiguredHarnessQualificationIsContentFree(t *testing.T) {
	registry := New(nil)
	result, err := registry.QualifyHarness(context.Background(), routeSettings{},
		"openai", DefaultOpenAIModel)
	if err != nil || result.Status != HarnessDiagnosticUnreachable ||
		result.Outcome != string(llm.OutcomePermanent) ||
		result.FailureReason != llm.ProviderFailureNotConfigured ||
		result.NetworkRequestAttempted || result.ModelCalls != 0 ||
		result.SyntheticToolCalls != 0 || result.ToolExecuted ||
		result.ResponseContentReturned ||
		result.Harness.TransportProtocol != llm.HarnessTransportOpenAIChatCompletions ||
		result.Harness.Model != DefaultOpenAIModel {
		t.Fatalf("unexpected unconfigured Harness qualification: %#v err=%v", result, err)
	}
}

func TestDeepSeekDiagnosticDisablesThinkingWithoutChangingOtherProviders(t *testing.T) {
	var thinking struct {
		Type string `json:"type"`
	}
	maxTokens := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter,
		request *http.Request,
	) {
		var body struct {
			MaxTokens int `json:"max_tokens"`
			Thinking  *struct {
				Type string `json:"type"`
			} `json:"thinking"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		if body.Thinking != nil {
			thinking.Type = body.Thinking.Type
		}
		maxTokens = body.MaxTokens
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{
			"id":"msg_diagnostic","type":"message","role":"assistant",
			"model":"deepseek-test","content":[{"type":"text","text":"ok"}],
			"usage":{"input_tokens":3,"output_tokens":1}
		}`))
	}))
	defer server.Close()
	values := map[string]string{
		"DEEPSEEK_API_KEY":  "test-provider-key-0123456789",
		"DEEPSEEK_BASE_URL": server.URL,
		"DEEPSEEK_MODEL":    "deepseek-test",
	}
	registry := New(func(name string) (string, bool) {
		value, found := values[name]
		return value, found
	})
	result, err := registry.Diagnose(t.Context(), "deepseek", "deepseek-test")
	if err != nil {
		t.Fatal(err)
	}
	if thinking.Type != "disabled" || maxTokens != diagnosticMaxTokens ||
		result.Status != DiagnosticReachable ||
		result.Outcome != string(llm.OutcomeSuccess) {
		t.Fatalf("DeepSeek diagnostic compatibility failed: thinking=%#v max_tokens=%d result=%#v",
			thinking, maxTokens, result)
	}
}

func TestHarnessQualificationIsSyntheticExactAndDurable(t *testing.T) {
	settings := routeSettings{}
	registry := registryWithQualificationProvider("a")
	before := registry.Snapshot()
	harness := before.Providers[len(before.Providers)-1].Harnesses[0]
	if harness.QualificationStatus != llm.HarnessQualificationRequired ||
		harness.RootEligible {
		t.Fatalf("unqualified model was projected as ready: %#v", harness)
	}
	result, err := registry.QualifyHarness(context.Background(), settings,
		"qualification-test", "model")
	if err != nil {
		t.Fatal(err)
	}
	if result.ProtocolVersion != HarnessQualificationProtocolVersion ||
		result.Status != HarnessDiagnosticQualified ||
		result.Outcome != string(llm.OutcomeSuccess) ||
		result.FailureReason != llm.ProviderFailureNone ||
		result.ModelCalls != 2 || result.SyntheticToolCalls != 1 ||
		result.ToolExecuted || result.ResponseContentReturned ||
		!result.Harness.RootEligible ||
		result.Harness.QualificationStatus != llm.HarnessQualificationVerified {
		t.Fatalf("unexpected Harness qualification result: %#v", result)
	}
	if len(settings) != 2 {
		// One persisted Harness qualification plus one qualification-status
		// projection.
		t.Fatalf("qualification persistence count=%d", len(settings))
	}

	restarted := registryWithQualificationProvider("a")
	if err := restarted.LoadRouteSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	restored, err := restarted.Router().HarnessProfile(llm.ModelRef{
		Provider: "qualification-test", Model: "model",
	})
	if err != nil {
		t.Fatal(err)
	}
	if restored.QualificationStatus != llm.HarnessQualificationVerified {
		t.Fatalf("qualification was not restored: %#v", restored)
	}

	changed := registryWithQualificationProvider("b")
	if err := changed.LoadRouteSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	stale, err := changed.Router().HarnessProfile(llm.ModelRef{
		Provider: "qualification-test", Model: "model",
	})
	if err != nil {
		t.Fatal(err)
	}
	if stale.QualificationStatus != llm.HarnessQualificationRequired {
		t.Fatalf("qualification escaped its transport binding: %#v", stale)
	}
}

func TestHarnessQualificationRejectsResponseModelDrift(t *testing.T) {
	settings := routeSettings{}
	registry := registryWithQualificationResponseModel("a", "different-model")
	result, err := registry.QualifyHarness(context.Background(), settings,
		"qualification-test", "model")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != HarnessDiagnosticIncompatible ||
		result.Outcome != string(llm.OutcomeInvalidResponse) ||
		result.FailureReason != llm.ProviderFailureProtocolIncompatible ||
		result.QualificationStatus != QualificationStatusProtocolMismatch ||
		result.ModelCalls != 1 || result.Harness.RootEligible ||
		len(settings) != 1 {
		t.Fatalf("response identity drift was accepted: result=%#v settings=%#v",
			result, settings)
	}
}

func TestCollectHarnessProbeStreamPreservesExpiredContextWhenStreamCloses(t *testing.T) {
	router := llm.NewDefaultRouter()
	provider := &closedProbeProvider{}
	router.RegisterProvider(provider)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	for attempt := 0; attempt < 32; attempt++ {
		_, err := collectHarnessProbeStream(ctx, router, llm.ModelRef{
			Provider: provider.Name(), Model: "model",
		}, llm.ChatRequest{})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("closed stream attempt %d returned %#v", attempt, err)
		}
	}
}

type closedProbeProvider struct{}

func (*closedProbeProvider) Name() string { return "closed-probe" }

func (*closedProbeProvider) ListModels(context.Context) ([]llm.ModelInfo, error) {
	return []llm.ModelInfo{{ID: "model", Provider: "closed-probe"}}, nil
}

func (*closedProbeProvider) Chat(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
	return nil, errors.New("closed probe does not support non-streaming chat")
}

func (*closedProbeProvider) StreamChat(context.Context,
	llm.ChatRequest,
) (<-chan llm.ChatChunk, error) {
	chunks := make(chan llm.ChatChunk)
	close(chunks)
	return chunks, nil
}

func (*closedProbeProvider) SupportsTools(string) bool    { return false }
func (*closedProbeProvider) SupportsVision(string) bool   { return false }
func (*closedProbeProvider) SupportsJSONMode(string) bool { return false }

type qualificationProvider struct {
	binding       string
	responseModel string
}

func (*qualificationProvider) Name() string { return "qualification-test" }

func (*qualificationProvider) ListModels(context.Context) ([]llm.ModelInfo, error) {
	return []llm.ModelInfo{{ID: "model", Provider: "qualification-test"}}, nil
}

func (p *qualificationProvider) Chat(ctx context.Context,
	request llm.ChatRequest,
) (*llm.ChatResponse, error) {
	stream, err := p.StreamChat(ctx, request)
	if err != nil {
		return nil, err
	}
	var text strings.Builder
	for chunk := range stream {
		text.WriteString(chunk.Text)
		if chunk.Done {
			return &llm.ChatResponse{Text: text.String(), ToolCalls: chunk.ToolCalls,
				Usage: *chunk.Usage, Provider: chunk.Provider, Model: chunk.Model}, nil
		}
	}
	return nil, errors.New("qualification test stream ended")
}

func (p *qualificationProvider) StreamChat(_ context.Context,
	request llm.ChatRequest,
) (<-chan llm.ChatChunk, error) {
	if request.MaxTokens != harnessProbeMaxTokens {
		return nil, errors.New("unexpected qualification token budget")
	}
	response := &llm.ChatResponse{
		Provider: p.Name(), Model: p.responseModel,
		Usage: llm.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2},
	}
	switch request.Metadata["phase"] {
	case "tool_call":
		words := strings.Fields(request.Messages[len(request.Messages)-1].Content)
		nonce := strings.TrimSuffix(words[len(words)-1], ".")
		response.ToolCalls = []llm.ToolCall{{
			ID: "probe-call", Name: "prayu_harness_echo",
			Arguments: json.RawMessage(`{"nonce":"` + nonce + `"}`),
		}}
	case "tool_result_and_json":
		var result harnessProbeResponse
		for _, message := range request.Messages {
			for _, toolResult := range message.ToolResults {
				_ = json.Unmarshal([]byte(toolResult.Content), &result)
			}
		}
		encoded, _ := json.Marshal(harnessProbeResponse{
			Version: HarnessProbeProtocolVersion, Status: "ok", Nonce: result.Nonce,
		})
		response.Text = string(encoded)
	default:
		return nil, errors.New("unexpected qualification phase")
	}
	chunks := make(chan llm.ChatChunk, 2)
	if response.Text != "" {
		chunks <- llm.ChatChunk{Text: response.Text}
	}
	chunks <- llm.FinalChatChunk(response)
	close(chunks)
	return chunks, nil
}

func (*qualificationProvider) SupportsTools(string) bool    { return true }
func (*qualificationProvider) SupportsVision(string) bool   { return false }
func (*qualificationProvider) SupportsJSONMode(string) bool { return false }

func (p *qualificationProvider) DescribeModelHarness(string) llm.ModelHarness {
	return llm.ModelHarness{
		ProtocolVersion:     llm.ModelHarnessProtocolVersion,
		TransportProtocol:   llm.HarnessTransportAnthropicMessages,
		ToolStrategy:        llm.HarnessToolStrategyNative,
		JSONStrategy:        llm.HarnessJSONStrategyPrompt,
		QualificationStatus: llm.HarnessQualificationRequired,
		BindingDigest:       strings.Repeat(p.binding, 64),
	}
}

func registryWithQualificationProvider(binding string) *Registry {
	return registryWithQualificationResponseModel(binding, "model")
}

func registryWithQualificationResponseModel(binding string, responseModel string) *Registry {
	registry := New(nil)
	provider := &qualificationProvider{binding: binding, responseModel: responseModel}
	registry.router.RegisterProvider(provider)
	registry.providers = append(registry.providers, ProviderAvailability{
		Name: provider.Name(), Kind: ProviderKindAnthropicCompatible,
		Status: ProviderAvailable, Models: []string{"model"},
		CredentialSource: "none", NetworkRequired: true,
	})
	registry.available[provider.Name()] = struct{}{}
	sort.Slice(registry.providers, func(i, j int) bool {
		return registry.providers[i].Name < registry.providers[j].Name
	})
	return registry
}

func contains(values []string, value string) bool {
	for _, current := range values {
		if current == value {
			return true
		}
	}
	return false
}

func snapshotText(snapshot Snapshot) string {
	var builder strings.Builder
	for _, provider := range snapshot.Providers {
		builder.WriteString(provider.Name)
		builder.WriteString(provider.Status)
		builder.WriteString(strings.Join(provider.Models, ","))
	}
	for _, route := range snapshot.Routes {
		builder.WriteString(route.Name)
		builder.WriteString(route.Provider)
		builder.WriteString(route.Model)
	}
	return builder.String()
}
