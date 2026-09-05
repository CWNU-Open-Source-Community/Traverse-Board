package application

import (
	"context"
	"sync"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/credential"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/modelregistry"
)

type providerDefinitionMemoryStore struct {
	mu          sync.RWMutex
	values      map[string]string
	preferences map[string][]domain.ThreadModelRoutePreference
}

func newProviderDefinitionMemoryStore() *providerDefinitionMemoryStore {
	return &providerDefinitionMemoryStore{values: make(map[string]string),
		preferences: make(map[string][]domain.ThreadModelRoutePreference)}
}

func (s *providerDefinitionMemoryStore) GetProviderSetting(_ context.Context,
	key string,
) (string, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, found := s.values[key]
	return value, found, nil
}

func (s *providerDefinitionMemoryStore) SetProviderSetting(_ context.Context,
	key string, value string,
) error {
	s.mu.Lock()
	s.values[key] = value
	s.mu.Unlock()
	return nil
}

func (s *providerDefinitionMemoryStore) CompareAndSwapProviderDefinitionCollection(
	_ context.Context, expectedRevision uint64,
	next modelregistry.ProviderDefinitionCollection,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := modelregistry.EmptyProviderDefinitionCollection()
	if encoded, found := s.values[modelregistry.ProviderDefinitionsSettingKey]; found {
		decoded, err := modelregistry.DecodeProviderDefinitionCollection(encoded)
		if err != nil {
			return err
		}
		current = decoded
	}
	if current.Revision != expectedRevision {
		return apperror.New(apperror.CodeConflict,
			"custom Provider definition collection revision is stale")
	}
	customIDs := make(map[string]struct{}, len(current.Providers)+len(next.Providers))
	nextByID := make(map[string]modelregistry.ProviderDefinition, len(next.Providers))
	for _, definition := range current.Providers {
		customIDs[definition.ID] = struct{}{}
	}
	for _, definition := range next.Providers {
		customIDs[definition.ID] = struct{}{}
		nextByID[definition.ID] = definition
	}
	for provider, preferences := range s.preferences {
		if _, custom := customIDs[provider]; !custom {
			continue
		}
		definition, retained := nextByID[provider]
		for _, preference := range preferences {
			if !preference.Selected {
				continue
			}
			modelRetained := false
			for _, model := range definition.Models {
				if model == preference.Model {
					modelRetained = true
					break
				}
			}
			if !retained || !definition.Enabled || !modelRetained {
				return apperror.New(apperror.CodeFailedPrecondition,
					"custom Provider definition mutation would invalidate a selected Thread model route")
			}
		}
	}
	encoded, err := modelregistry.EncodeProviderDefinitionCollection(next)
	if err != nil {
		return err
	}
	s.values[modelregistry.ProviderDefinitionsSettingKey] = encoded
	return nil
}

func (s *providerDefinitionMemoryStore) ListSelectedThreadModelRoutePreferences(
	_ context.Context, provider string,
) ([]domain.ThreadModelRoutePreference, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]domain.ThreadModelRoutePreference(nil), s.preferences[provider]...), nil
}

func (s *providerDefinitionMemoryStore) selectThreadModelRoute(provider, model string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.preferences[provider] = []domain.ThreadModelRoutePreference{{
		ProtocolVersion: domain.ThreadModelRouteProtocolVersion,
		ThreadID:        "thread-provider-reference", Selected: true,
		Provider: provider, Model: model, UpdatedAt: time.Now().UTC(),
	}}
}

func customProviderDefinition(id string) modelregistry.ProviderDefinition {
	return modelregistry.ProviderDefinition{
		Version: modelregistry.ProviderDefinitionVersion,
		ID:      id, DisplayName: "Custom " + id, Note: "Team Provider",
		WebsiteURL: "https://example.com", EndpointURL: "https://api.example.com/v1",
		DefaultModel: "code-model", Models: []string{"code-model", "fast-model"},
		Transport:                 modelregistry.ProviderTransportOpenAIChatCompletions,
		SearchMode:                modelregistry.ProviderSearchModeSearXNG,
		NativeWebSearchCapability: modelregistry.NativeWebSearchUnsupported,
		Enabled:                   true,
	}
}

func TestProviderDefinitionServiceUpsertReloadAndRevisionControl(t *testing.T) {
	settings := newProviderDefinitionMemoryStore()
	registry := modelregistry.New(nil)
	service, err := NewProviderDefinitionService(settings, registry)
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.Upsert(t.Context(), ProviderDefinitionUpsertRequest{
		Version: ProviderDefinitionControlProtocolVersion, ExpectedCollectionRevision: 0,
		Definition: customProviderDefinition("custom-acme"), Confirm: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !created.RegistryReloaded || created.RegistryGeneration != 2 ||
		created.Collection.Revision != 1 || created.Definition.Revision != 1 ||
		len(created.Collection.Providers) != 1 || string(created.Definition.AdvancedConfig) != `{}` {
		t.Fatalf("custom Provider create result=%#v", created)
	}
	provider := created.Definition
	provider.DisplayName = "Custom Acme Updated"
	updated, err := service.Upsert(t.Context(), ProviderDefinitionUpsertRequest{
		Version: ProviderDefinitionControlProtocolVersion, ExpectedCollectionRevision: 1,
		Definition: provider, Confirm: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Collection.Revision != 2 || updated.Definition.Revision != 2 ||
		updated.RegistryGeneration != 3 {
		t.Fatalf("custom Provider update result=%#v", updated)
	}
	if _, err := service.Upsert(t.Context(), ProviderDefinitionUpsertRequest{
		Version: ProviderDefinitionControlProtocolVersion, ExpectedCollectionRevision: 1,
		Definition: updated.Definition, Confirm: true,
	}); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("stale collection revision was accepted: %v", err)
	}

	listed, err := service.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	listed.Providers[0].Models[0] = "tampered"
	relisted, err := service.List(t.Context())
	if err != nil || relisted.Providers[0].Models[0] != "code-model" {
		t.Fatalf("List exposed mutable service state: %#v err=%v", relisted, err)
	}
}

func TestProviderDefinitionServiceSerializesConcurrentCollectionCAS(t *testing.T) {
	settings := newProviderDefinitionMemoryStore()
	registry := modelregistry.New(nil)
	service, err := NewProviderDefinitionService(settings, registry)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, id := range []string{"custom-one", "custom-two"} {
		id := id
		go func() {
			<-start
			_, err := service.Upsert(t.Context(), ProviderDefinitionUpsertRequest{
				Version:                    ProviderDefinitionControlProtocolVersion,
				ExpectedCollectionRevision: 0,
				Definition:                 customProviderDefinition(id), Confirm: true,
			})
			results <- err
		}()
	}
	close(start)
	successes, conflicts := 0, 0
	for index := 0; index < 2; index++ {
		err := <-results
		if err == nil {
			successes++
		} else if apperror.CodeOf(err) == apperror.CodeConflict {
			conflicts++
		} else {
			t.Fatalf("unexpected concurrent mutation error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("CAS outcomes successes=%d conflicts=%d", successes, conflicts)
	}
	collection, err := service.List(t.Context())
	if err != nil || collection.Revision != 1 || len(collection.Providers) != 1 {
		t.Fatalf("concurrent collection=%#v err=%v", collection, err)
	}
}

func TestProviderDefinitionDeleteFailsWhileRouteReferencesProvider(t *testing.T) {
	settings := newProviderDefinitionMemoryStore()
	registry := modelregistry.New(nil)
	service, err := NewProviderDefinitionService(settings, registry)
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.Upsert(t.Context(), ProviderDefinitionUpsertRequest{
		Version: ProviderDefinitionControlProtocolVersion, ExpectedCollectionRevision: 0,
		Definition: customProviderDefinition("custom-route"), Confirm: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := settings.SetProviderSetting(t.Context(), "route.code", "custom-route/code-model"); err != nil {
		t.Fatal(err)
	}
	request := ProviderDefinitionDeleteRequest{
		Version: ProviderDefinitionControlProtocolVersion, ID: "custom-route",
		ExpectedCollectionRevision: created.Collection.Revision,
		ExpectedDefinitionRevision: created.Definition.Revision, Confirm: true,
	}
	if _, err := service.Delete(t.Context(), request); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("route-bound custom Provider was deleted: %v", err)
	}
	if err := settings.SetProviderSetting(t.Context(), "route.code", "mock/mock-code"); err != nil {
		t.Fatal(err)
	}
	deleted, err := service.Delete(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.DeletedID != "custom-route" || deleted.Collection.Revision != 2 ||
		len(deleted.Collection.Providers) != 0 || !deleted.RegistryReloaded {
		t.Fatalf("custom Provider delete result=%#v", deleted)
	}
}

func TestProviderDefinitionMutationProtectsSelectedThreadModelRoutes(t *testing.T) {
	settings := newProviderDefinitionMemoryStore()
	registry := modelregistry.New(nil)
	service, err := NewProviderDefinitionService(settings, registry)
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.Upsert(t.Context(), ProviderDefinitionUpsertRequest{
		Version: ProviderDefinitionControlProtocolVersion, ExpectedCollectionRevision: 0,
		Definition: customProviderDefinition("custom-thread-route"), Confirm: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	settings.selectThreadModelRoute("custom-thread-route", "code-model")

	disabled := created.Definition
	disabled.Enabled = false
	if _, err := service.Upsert(t.Context(), ProviderDefinitionUpsertRequest{
		Version:                    ProviderDefinitionControlProtocolVersion,
		ExpectedCollectionRevision: created.Collection.Revision,
		Definition:                 disabled, Confirm: true,
	}); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("Provider selected by a Thread was disabled: %v", err)
	}
	removedModel := created.Definition
	removedModel.Models = []string{"fast-model"}
	removedModel.DefaultModel = "fast-model"
	if _, err := service.Upsert(t.Context(), ProviderDefinitionUpsertRequest{
		Version:                    ProviderDefinitionControlProtocolVersion,
		ExpectedCollectionRevision: created.Collection.Revision,
		Definition:                 removedModel, Confirm: true,
	}); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("Provider update removed a Thread-selected model: %v", err)
	}
	if _, err := service.Delete(t.Context(), ProviderDefinitionDeleteRequest{
		Version: ProviderDefinitionControlProtocolVersion, ID: created.Definition.ID,
		ExpectedCollectionRevision: created.Collection.Revision,
		ExpectedDefinitionRevision: created.Definition.Revision, Confirm: true,
	}); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("Provider selected by a Thread was deleted: %v", err)
	}

	retained := created.Definition
	retained.Note = "metadata update retains the selected model"
	updated, err := service.Upsert(t.Context(), ProviderDefinitionUpsertRequest{
		Version:                    ProviderDefinitionControlProtocolVersion,
		ExpectedCollectionRevision: created.Collection.Revision,
		Definition:                 retained, Confirm: true,
	})
	if err != nil || updated.Definition.Revision != created.Definition.Revision+1 ||
		updated.Collection.Revision != created.Collection.Revision+1 {
		t.Fatalf("safe Provider update retaining selected Thread route failed: result=%+v err=%v",
			updated, err)
	}
}

func TestCustomProviderCredentialChangeHotReloadsConfiguredProvider(t *testing.T) {
	for _, name := range []string{
		"MIMO_API_KEY", "DEEPSEEK_API_KEY", "CYBERAGENT_ANTHROPIC_API_KEY",
		"CYBERAGENT_OPENAI_API_KEY", "CYBERAGENT_OLLAMA_BASE_URL",
	} {
		t.Setenv(name, "")
	}
	settings := newProviderDefinitionMemoryStore()
	credentials := credential.NewMemoryStore()
	registry, err := modelregistry.NewFromEnvironmentWithCredentials(credentials)
	if err != nil {
		t.Fatal(err)
	}
	definitionService, err := NewProviderDefinitionService(settings, registry)
	if err != nil {
		t.Fatal(err)
	}
	created, err := definitionService.Upsert(t.Context(), ProviderDefinitionUpsertRequest{
		Version: ProviderDefinitionControlProtocolVersion, ExpectedCollectionRevision: 0,
		Definition: customProviderDefinition("custom-live"), Confirm: true,
	})
	if err != nil || created.RegistryGeneration != 2 {
		t.Fatalf("custom Provider definition create=%#v err=%v", created, err)
	}
	if status := providerStatus(registry.Snapshot(), "custom-live"); status != modelregistry.ProviderNotConfigured {
		t.Fatalf("credential-free custom Provider status=%q", status)
	}
	credentialService := NewProviderCredentialService(credentials).
		WithRegistryReload(registry, settings)
	changed, err := credentialService.Change(t.Context(), ChangeProviderCredentialRequest{
		Version: credential.ProtocolVersion, Provider: "custom-live",
		Action: ProviderCredentialSet, Secret: "custom-live-secret-0123456789", Confirm: true,
	})
	if err != nil || !changed.Configured || !changed.RegistryReloaded ||
		changed.RegistryGeneration != 3 {
		t.Fatalf("custom Provider credential hot reload=%#v err=%v", changed, err)
	}
	if status := providerStatus(registry.Snapshot(), "custom-live"); status != modelregistry.ProviderAvailable {
		t.Fatalf("configured custom Provider status=%q", status)
	}
}

func providerStatus(snapshot modelregistry.Snapshot, provider string) string {
	for _, current := range snapshot.Providers {
		if current.Name == provider {
			return current.Status
		}
	}
	return "missing"
}
