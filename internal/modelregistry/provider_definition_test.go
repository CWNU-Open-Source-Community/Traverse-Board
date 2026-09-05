package modelregistry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cyberagent-workbench/internal/llm"
)

func validCustomDefinition(endpoint string) ProviderDefinition {
	return ProviderDefinition{
		Version: ProviderDefinitionVersion, ID: "acme-models", DisplayName: "Acme Models",
		Note: "Team account", WebsiteURL: "https://example.com", EndpointURL: endpoint,
		DefaultModel: "acme-code", Models: []string{"acme-code", "acme-fast"},
		Transport:                 ProviderTransportOpenAIChatCompletions,
		SearchMode:                ProviderSearchModeAuto,
		NativeWebSearchCapability: NativeWebSearchDeclaredUnverified,
		Enabled:                   true,
	}
}

func providerDefinitionSetting(t *testing.T, definition ProviderDefinition,
	collectionRevision uint64,
) string {
	t.Helper()
	definition.Revision = 1
	value, err := EncodeProviderDefinitionCollection(ProviderDefinitionCollection{
		Version: ProviderDefinitionCollectionVersion, Revision: collectionRevision,
		Providers: []ProviderDefinition{definition},
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestProviderDefinitionValidationAndSecretRejection(t *testing.T) {
	valid := validCustomDefinition("https://api.example.com/v1")
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*ProviderDefinition){
		"reserved id": func(value *ProviderDefinition) { value.ID = "openai" },
		"secret note": func(value *ProviderDefinition) {
			value.Note = "API_KEY=sk-" + strings.Repeat("a", 32)
		},
		"credential endpoint": func(value *ProviderDefinition) {
			value.EndpointURL = "https://user:password@example.com/v1"
		},
		"endpoint query": func(value *ProviderDefinition) {
			value.EndpointURL = "https://api.example.com/v1?api_key=value"
		},
		"insecure remote endpoint": func(value *ProviderDefinition) {
			value.EndpointURL = "http://api.example.com/v1"
		},
		"missing default": func(value *ProviderDefinition) { value.DefaultModel = "other" },
		"duplicate model": func(value *ProviderDefinition) {
			value.Models = []string{"acme-code", "acme-code"}
		},
		"unsupported transport": func(value *ProviderDefinition) { value.Transport = "unknown_transport" },
		"native without declaration": func(value *ProviderDefinition) {
			value.SearchMode = ProviderSearchModeProviderNative
			value.NativeWebSearchCapability = NativeWebSearchUnsupported
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid
			candidate.Models = append([]string(nil), valid.Models...)
			mutate(&candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatalf("unsafe definition was accepted: %#v", candidate)
			}
		})
	}

	encoded, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	raw := `{"version":"provider_definition_collection.v1","revision":1,"providers":[` +
		strings.TrimSuffix(string(encoded), "}") + `,"api_key":"sk-` +
		strings.Repeat("z", 32) + `"}]}`
	if _, err := DecodeProviderDefinitionCollection(raw); err == nil {
		t.Fatal("unknown embedded credential field was accepted")
	}
}

func TestProviderAdvancedConfigAllowsOnlyOwnedCredentialReferences(t *testing.T) {
	accepted := json.RawMessage(`{
		"env":{"ACME_API_TOKEN":{"$credential":"acme-models"}},
		"request_headers":{
			"Authorization":{"$credential":"acme-models","template":"Bearer ${secret}"},
			"X-Acme-Region":"west",
			"Model":"routing-hint"
		},
		"request_body":{"reasoning_effort":"high"},
		"model_mapping":{"primary":"acme-code"},
		"extensions":{"model":"extension-specific-model","tools":"extension-metadata"}
	}`)
	normalized, err := ValidateAndNormalizeProviderAdvancedConfig(accepted, "acme-models")
	if err != nil {
		t.Fatal(err)
	}
	if len(normalized) == 0 || strings.Contains(string(normalized), "sk-") {
		t.Fatalf("advanced config was not normalized safely: %s", normalized)
	}
	defaulted, err := ValidateAndNormalizeProviderAdvancedConfig(nil, "acme-models")
	if err != nil || string(defaulted) != `{}` {
		t.Fatalf("advanced config default=%s err=%v", defaulted, err)
	}

	for name, raw := range map[string]string{
		"raw API key":                    `{"env":{"ACME_API_KEY":"sk-abcdefghijklmnopqrstuvwxyz012345"}}`,
		"other credential":               `{"env":{"ACME_API_KEY":{"$credential":"other-provider"}}}`,
		"userinfo template":              `{"env":{"ACME_API_KEY":{"$credential":"acme-models","template":"https://user:${secret}@example.com"}}}`,
		"signed URL template":            `{"env":{"ACME_API_KEY":{"$credential":"acme-models","template":"https://example.com/?token=${secret}"}}}`,
		"raw authorization":              `{"request_headers":{"Authorization":"Bearer abcdef"}}`,
		"reserved host":                  `{"request_headers":{"Host":"api.example.com"}}`,
		"reserved accept":                `{"request_headers":{"Accept":"application/json"}}`,
		"reserved content type":          `{"request_headers":{"Content-Type":"application/json"}}`,
		"duplicate header case":          `{"request_headers":{"X-Region":"one","x-region":"two"}}`,
		"core model override":            `{"request_body":{"model":"other"}}`,
		"core tools override":            `{"request_body":{"tools":[]}}`,
		"core store override":            `{"request_body":{"store":true}}`,
		"core include override":          `{"request_body":{"include":["web_search_call.action.sources"]}}`,
		"core stream override":           `{"request_body":{"stream":false}}`,
		"root model override":            `{"model":"other"}`,
		"credential URL":                 `{"proxy":"https://user:password@example.com"}`,
		"credential query URL":           `{"callback":"https://example.com/?api_key=value"}`,
		"signed query URL":               `{"callback":"https://example.com/?x-amz-signature=value"}`,
		"duplicate key":                  `{"region":"one","region":"two"}`,
		"credential-only root":           `{"$credential":"acme-models"}`,
		"inline placeholder":             `{"value":"Bearer ${secret}"}`,
		"invalid env shape":              `{"env":["A=B"]}`,
		"invalid model map":              `{"model_mapping":{"acme-code":42}}`,
		"noncanonical runtime container": `{"Request_Headers":{"X-Region":"west"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ValidateAndNormalizeProviderAdvancedConfig(
				json.RawMessage(raw), "acme-models"); err == nil {
				t.Fatalf("unsafe advanced config was accepted: %s", raw)
			}
		})
	}
}

func TestDeepSeekResponsesNormalizationDefaultsToSafeToolContinuation(t *testing.T) {
	definition := validCustomDefinition("https://api.deepseek.com/responses")
	definition.ID = "official-deepseek"
	definition.DisplayName = "DeepSeek Official"
	definition.Transport = ProviderTransportOpenAIResponses
	definition.AdvancedConfig = json.RawMessage(`{}`)

	normalized, err := NormalizeProviderDefinition(definition)
	if err != nil {
		t.Fatal(err)
	}
	if string(normalized.AdvancedConfig) != `{"request_body":{"reasoning":{"effort":"none"}}}` {
		t.Fatalf("DeepSeek compatibility config=%s", normalized.AdvancedConfig)
	}

	definition.AdvancedConfig = json.RawMessage(
		`{"request_body":{"reasoning":{"effort":"high"}}}`)
	explicit, err := NormalizeProviderDefinition(definition)
	if err != nil {
		t.Fatal(err)
	}
	if string(explicit.AdvancedConfig) !=
		`{"request_body":{"reasoning":{"effort":"high"}}}` {
		t.Fatalf("explicit DeepSeek reasoning was overwritten: %s", explicit.AdvancedConfig)
	}

	definition.EndpointURL = "https://api.example.com/responses"
	definition.AdvancedConfig = json.RawMessage(`{}`)
	other, err := NormalizeProviderDefinition(definition)
	if err != nil {
		t.Fatal(err)
	}
	if string(other.AdvancedConfig) != `{}` {
		t.Fatalf("non-DeepSeek provider inherited compatibility config: %s", other.AdvancedConfig)
	}
}

func TestRegistryLoadsCustomProviderWithoutCredentialAsNotConfigured(t *testing.T) {
	definition := validCustomDefinition("https://api.example.com/v1")
	settings := routeSettings{ProviderDefinitionsSettingKey: providerDefinitionSetting(t, definition, 1)}
	registry, err := newRegistry(func(string) (string, bool) { return "", false },
		func(context.Context, string) (string, bool, error) { return "", false, nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.LoadRouteSettings(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	provider, found := providerByName(registry.Snapshot(), definition.ID)
	if !found || provider.Status != ProviderNotConfigured || !provider.Custom ||
		!provider.Enabled || provider.CredentialSource != "system" ||
		provider.Transport != ProviderTransportOpenAIChatCompletions ||
		provider.SearchMode != ProviderSearchModeAuto ||
		provider.NativeWebSearchCapability != NativeWebSearchDeclaredUnverified ||
		provider.NativeWebSearchRuntimeEnabled || provider.ConfigurationError ||
		len(provider.Harnesses) != 2 ||
		provider.Harnesses[0].QualificationStatus != llm.HarnessQualificationRequired {
		t.Fatalf("custom Provider did not fail closed without a key: %#v", provider)
	}
	if contains(registry.Router().ProviderNames(), definition.ID) {
		t.Fatal("unconfigured custom Provider was registered in the live Router")
	}
}

func TestRegistryCustomProviderRestartLoadAndHotReload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	definition := validCustomDefinition(server.URL + "/v1")
	settings := routeSettings{
		ProviderDefinitionsSettingKey: providerDefinitionSetting(t, definition, 1),
		"route.code":                  definition.ID + "/" + definition.DefaultModel,
	}
	credentials := func(_ context.Context, provider string) (string, bool, error) {
		if provider == definition.ID {
			return "custom-provider-key-0123456789", true, nil
		}
		return "", false, nil
	}
	registry, err := newRegistry(func(string) (string, bool) { return "", false }, credentials)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.LoadRouteSettings(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	assertConfiguredCustomProvider(t, registry, definition)

	restarted, err := newRegistry(func(string) (string, bool) { return "", false }, credentials)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.LoadRouteSettings(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	assertConfiguredCustomProvider(t, restarted, definition)

	hot, err := newRegistry(func(string) (string, bool) { return "", false }, credentials)
	if err != nil {
		t.Fatal(err)
	}
	result, err := hot.Reload(t.Context(), settings)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Reloaded || result.Generation != 2 || hot.Generation() != 2 {
		t.Fatalf("custom Provider hot reload generation=%#v", result)
	}
	assertConfiguredCustomProvider(t, hot, definition)
}

func TestRegistryLoadsCustomAnthropicMessagesTransport(t *testing.T) {
	definition := validCustomDefinition("https://api.example.com/anthropic")
	definition.ID = "custom-anthropic"
	definition.DisplayName = "Custom Anthropic"
	definition.Transport = ProviderTransportAnthropicMessages
	definition.SearchMode = ProviderSearchModeDisabled
	definition.NativeWebSearchCapability = NativeWebSearchUnsupported
	settings := routeSettings{ProviderDefinitionsSettingKey: providerDefinitionSetting(t, definition, 1)}
	registry, err := newRegistry(func(string) (string, bool) { return "", false },
		func(_ context.Context, provider string) (string, bool, error) {
			if provider == definition.ID {
				return "custom-anthropic-key-0123456789", true, nil
			}
			return "", false, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.LoadRouteSettings(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	provider, found := providerByName(registry.Snapshot(), definition.ID)
	if !found || provider.Kind != ProviderKindAnthropicCompatible ||
		provider.Status != ProviderAvailable || len(provider.Harnesses) != 2 ||
		provider.Harnesses[0].TransportProtocol != llm.HarnessTransportAnthropicMessages ||
		provider.Harnesses[0].QualificationStatus != llm.HarnessQualificationRequired ||
		provider.Harnesses[0].RootEligible {
		t.Fatalf("Anthropic custom Provider Harness projection=%#v", provider)
	}
}

func assertConfiguredCustomProvider(t *testing.T, registry *Registry,
	definition ProviderDefinition,
) {
	t.Helper()
	snapshot := registry.Snapshot()
	provider, found := providerByName(snapshot, definition.ID)
	if !found || provider.Status != ProviderAvailable || !provider.Custom ||
		provider.NativeWebSearchRuntimeEnabled ||
		!contains(registry.Router().ProviderNames(), definition.ID) {
		t.Fatalf("custom Provider was not registered safely: %#v", provider)
	}
	profile, err := registry.Router().HarnessProfile(llm.ModelRef{
		Provider: definition.ID, Model: definition.DefaultModel,
	})
	if err != nil {
		t.Fatal(err)
	}
	if profile.TransportProtocol != llm.HarnessTransportOpenAIChatCompletions ||
		profile.QualificationStatus != llm.HarnessQualificationRequired {
		t.Fatalf("custom Provider Harness was overstated: %#v", profile)
	}
	for _, route := range snapshot.Routes {
		if route.Name == "code" && (!route.Available || route.Provider != definition.ID ||
			route.Model != definition.DefaultModel || route.HarnessReady) {
			t.Fatalf("custom Provider route/Harness projection is wrong: %#v", route)
		}
	}
}

func providerByName(snapshot Snapshot, name string) (ProviderAvailability, bool) {
	for _, provider := range snapshot.Providers {
		if provider.Name == name {
			return provider, true
		}
	}
	return ProviderAvailability{}, false
}
