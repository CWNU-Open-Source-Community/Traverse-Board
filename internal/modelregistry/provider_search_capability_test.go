package modelregistry

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func TestProviderWebSearchModePersistsAndLoadsWithoutNativeDeclaration(t *testing.T) {
	definition := validCustomDefinition("https://api.deepseek.com/responses")
	definition.ID = "official-deepseek"
	definition.DefaultModel = "deepseek-v4-flash"
	definition.Models = []string{"deepseek-v4-flash", "deepseek-v4-pro"}
	definition.Transport = ProviderTransportOpenAIResponses
	definition.SearchMode = ProviderSearchModeWeb
	definition.NativeWebSearchCapability = NativeWebSearchUnsupported
	definition.AdvancedConfig = json.RawMessage(`{"request_body":{"reasoning":{"effort":"none"}}}`)
	settings := routeSettings{
		ProviderDefinitionsSettingKey: providerDefinitionSetting(t, definition, 1),
		"route.code":                  definition.ID + "/" + definition.DefaultModel,
	}
	saved := settings[ProviderDefinitionsSettingKey]
	collection, err := ReadProviderDefinitions(t.Context(), settings)
	if err != nil || len(collection.Providers) != 1 {
		t.Fatalf("read saved definition: %v", err)
	}
	loaded := collection.Providers[0]
	if loaded.SearchMode != ProviderSearchModeWeb || loaded.NativeWebSearchCapability != NativeWebSearchUnsupported ||
		loaded.DefaultModel != definition.DefaultModel || !reflect.DeepEqual(loaded.Models, definition.Models) ||
		loaded.EndpointURL != definition.EndpointURL || string(loaded.AdvancedConfig) != string(definition.AdvancedConfig) {
		t.Fatalf("web mode altered the model Provider contract: %+v", loaded)
	}
	reencoded, err := EncodeProviderDefinitionCollection(collection)
	if err != nil || reencoded != saved || settings[ProviderDefinitionsSettingKey] != saved {
		t.Fatalf("definition round trip changed saved bytes: %v", err)
	}
	registry, err := newRegistry(func(string) (string, bool) { return "", false },
		func(_ context.Context, id string) (string, bool, error) {
			return "local-test-key", id == definition.ID, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.LoadRouteSettings(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	provider, found := providerByName(registry.Snapshot(), definition.ID)
	ref := registry.Router().Resolve("code")
	if !found || provider.Status != ProviderAvailable || provider.SearchMode != ProviderSearchModeWeb ||
		provider.NativeWebSearchCapability != NativeWebSearchUnsupported || provider.NativeWebSearchRuntimeEnabled ||
		ref.Provider != definition.ID || ref.Model != definition.DefaultModel {
		t.Fatal("web mode failed to preserve the configured ordinary model route")
	}
}

func TestProviderNativeWebSearchKnownUnsupportedUsesExactAPIHost(t *testing.T) {
	for _, tc := range []struct {
		name     string
		endpoint string
		want     bool
	}{
		{"official", "https://api.deepseek.com/responses", true},
		{"official_v1", "https://api.deepseek.com/v1/responses", true},
		{"case_and_dns_dot", "https://API.DEEPSEEK.COM.:443/responses", true},
		{"proxy", "https://proxy.example.com/deepseek/responses", false},
		{"suffix", "https://api.deepseek.com.example.com/responses", false},
		{"userinfo_is_not_host", "https://api.deepseek.com@proxy.example.com/responses", false},
		{"path_is_not_host", "https://proxy.example.com/api.deepseek.com/responses", false},
		{"invalid", "://api.deepseek.com", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			definition := validCustomDefinition(tc.endpoint)
			definition.ID = "official-deepseek"
			definition.Models = []string{"deepseek-v4-flash"}
			if got := ProviderNativeWebSearchKnownUnsupported(definition); got != tc.want {
				t.Fatalf("known unsupported=%t want=%t", got, tc.want)
			}
		})
	}
}

func TestRegistryProjectsUnsupportedNativeSearchWithoutChangingSavedRoute(t *testing.T) {
	for _, tc := range []struct {
		name       string
		endpoint   string
		capability string
	}{
		{"official", "https://api.deepseek.com/responses", NativeWebSearchUnsupported},
		{"proxy_declaration_preserved", "https://proxy.example.com/responses", NativeWebSearchDeclaredUnverified},
	} {
		t.Run(tc.name, func(t *testing.T) {
			definition := validCustomDefinition(tc.endpoint)
			definition.ID = "official-deepseek"
			definition.DefaultModel = "deepseek-v4-flash"
			definition.Models = []string{"deepseek-v4-flash", "deepseek-v4-pro"}
			definition.Transport = ProviderTransportOpenAIResponses
			definition.SearchMode = ProviderSearchModeProviderNative
			settings := routeSettings{
				ProviderDefinitionsSettingKey: providerDefinitionSetting(t, definition, 1),
				"route.code":                  definition.ID + "/" + definition.DefaultModel,
			}
			originalDefinition := settings[ProviderDefinitionsSettingKey]
			for range 2 { // A new registry after restart reads the same unchanged setting.
				registry, err := newRegistry(func(string) (string, bool) { return "", false },
					func(_ context.Context, id string) (string, bool, error) {
						return "local-test-key", id == definition.ID, nil
					})
				if err != nil {
					t.Fatal(err)
				}
				if err := registry.LoadRouteSettings(t.Context(), settings); err != nil {
					t.Fatal(err)
				}
				provider, found := providerByName(registry.Snapshot(), definition.ID)
				if !found || provider.Status != ProviderAvailable ||
					provider.SearchMode != ProviderSearchModeProviderNative ||
					provider.NativeWebSearchCapability != tc.capability || provider.NativeWebSearchRuntimeEnabled ||
					!reflect.DeepEqual(provider.Models, definition.Models) {
					t.Fatalf("unexpected provider projection: %+v", provider)
				}
				ref := registry.Router().Resolve("code")
				if ref.Provider != definition.ID || ref.Model != definition.DefaultModel ||
					settings[ProviderDefinitionsSettingKey] != originalDefinition ||
					settings["route.code"] != definition.ID+"/"+definition.DefaultModel {
					t.Fatal("capability projection changed the saved definition or model route")
				}
				persisted, err := ReadProviderDefinitions(t.Context(), settings)
				if err != nil || len(persisted.Providers) != 1 ||
					persisted.Providers[0].NativeWebSearchCapability != NativeWebSearchDeclaredUnverified ||
					persisted.Providers[0].SearchMode != ProviderSearchModeProviderNative {
					t.Fatal("capability projection rewrote the operator declaration")
				}
			}
		})
	}
}
