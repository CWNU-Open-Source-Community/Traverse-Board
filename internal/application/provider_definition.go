package application

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/modelregistry"
)

const ProviderDefinitionControlProtocolVersion = "provider_definition_control.v1"

type ProviderDefinitionStore interface {
	modelregistry.RouteSettingReader
	modelregistry.RouteSettingWriter
	// CompareAndSwapProviderDefinitionCollection must validate every selected
	// Thread model-route preference against next and persist next in one storage
	// transaction. This is the shared atomic boundary with
	// ChangeThreadModelRoutePreference; a service-local mutex is not sufficient
	// when the two controls execute concurrently.
	CompareAndSwapProviderDefinitionCollection(context.Context, uint64,
		modelregistry.ProviderDefinitionCollection) error
}

type ProviderDefinitionRegistry interface {
	Reload(context.Context, modelregistry.RouteSettingReader) (modelregistry.ReloadResult, error)
	Generation() uint64
}

type selectedThreadModelRouteReader interface {
	ListSelectedThreadModelRoutePreferences(context.Context, string) (
		[]domain.ThreadModelRoutePreference, error)
}

type ProviderDefinitionUpsertRequest struct {
	Version                    string                           `json:"version"`
	ExpectedCollectionRevision uint64                           `json:"expected_collection_revision"`
	Definition                 modelregistry.ProviderDefinition `json:"definition"`
	Confirm                    bool                             `json:"confirm"`
}

type ProviderDefinitionDeleteRequest struct {
	Version                    string `json:"version"`
	ID                         string `json:"id"`
	ExpectedCollectionRevision uint64 `json:"expected_collection_revision"`
	ExpectedDefinitionRevision uint64 `json:"expected_definition_revision"`
	Confirm                    bool   `json:"confirm"`
}

type ProviderDefinitionMutationResult struct {
	ProtocolVersion    string                                     `json:"protocol_version"`
	Collection         modelregistry.ProviderDefinitionCollection `json:"collection"`
	Definition         modelregistry.ProviderDefinition           `json:"definition,omitempty"`
	DeletedID          string                                     `json:"deleted_id,omitempty"`
	RegistryReloaded   bool                                       `json:"registry_reloaded"`
	RegistryGeneration uint64                                     `json:"registry_generation"`
}

type ProviderDefinitionService struct {
	mu       sync.Mutex
	store    ProviderDefinitionStore
	registry ProviderDefinitionRegistry
}

func NewProviderDefinitionService(store ProviderDefinitionStore,
	registry ProviderDefinitionRegistry,
) (*ProviderDefinitionService, error) {
	if store == nil || registry == nil {
		return nil, errors.New("custom Provider definition store and Registry are required")
	}
	return &ProviderDefinitionService{store: store, registry: registry}, nil
}

func (s *ProviderDefinitionService) List(ctx context.Context) (
	modelregistry.ProviderDefinitionCollection, error,
) {
	if s == nil || s.store == nil {
		return modelregistry.ProviderDefinitionCollection{}, apperror.New(
			apperror.CodeFailedPrecondition, "custom Provider definition service is unavailable")
	}
	if ctx == nil {
		return modelregistry.ProviderDefinitionCollection{}, apperror.New(
			apperror.CodeInvalidArgument, "custom Provider definition context is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	collection, err := modelregistry.ReadProviderDefinitions(ctx, s.store)
	if err != nil {
		return modelregistry.ProviderDefinitionCollection{}, apperror.Wrap(
			apperror.CodeFailedPrecondition,
			"durable custom Provider definitions are unavailable or invalid", err)
	}
	return cloneProviderDefinitionCollection(collection), nil
}

func (s *ProviderDefinitionService) Upsert(ctx context.Context,
	request ProviderDefinitionUpsertRequest,
) (ProviderDefinitionMutationResult, error) {
	if s == nil || s.store == nil || s.registry == nil {
		return ProviderDefinitionMutationResult{}, apperror.New(
			apperror.CodeFailedPrecondition, "custom Provider definition service is unavailable")
	}
	if ctx == nil || request.Version != ProviderDefinitionControlProtocolVersion || !request.Confirm {
		return ProviderDefinitionMutationResult{}, apperror.New(
			apperror.CodeInvalidArgument, "custom Provider definition upsert request is invalid")
	}
	normalizedDefinition, err := modelregistry.NormalizeProviderDefinition(request.Definition)
	if err != nil {
		return ProviderDefinitionMutationResult{}, apperror.Wrap(
			apperror.CodeInvalidArgument, "custom Provider definition is invalid", err)
	}
	if err := normalizedDefinition.Validate(); err != nil {
		return ProviderDefinitionMutationResult{}, apperror.Wrap(
			apperror.CodeInvalidArgument, "custom Provider definition is invalid", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	collection, err := s.readLocked(ctx)
	if err != nil {
		return ProviderDefinitionMutationResult{}, err
	}
	if request.ExpectedCollectionRevision != collection.Revision {
		return ProviderDefinitionMutationResult{}, apperror.New(apperror.CodeConflict,
			"custom Provider definition collection revision is stale")
	}
	index := providerDefinitionIndex(collection.Providers, normalizedDefinition.ID)
	definition := cloneProviderDefinition(normalizedDefinition)
	if index < 0 {
		if definition.Revision != 0 {
			return ProviderDefinitionMutationResult{}, apperror.New(apperror.CodeConflict,
				"new custom Provider definition must use revision zero")
		}
		definition.Revision = 1
		collection.Providers = append(collection.Providers, definition)
	} else {
		current := collection.Providers[index]
		if definition.Revision == 0 || definition.Revision != current.Revision {
			return ProviderDefinitionMutationResult{}, apperror.New(apperror.CodeConflict,
				"custom Provider definition revision is stale")
		}
		if current.Revision >= modelregistry.MaxProviderDefinitionRevision {
			return ProviderDefinitionMutationResult{}, apperror.New(apperror.CodeResourceExhausted,
				"custom Provider definition revision is exhausted")
		}
		if err := s.ensureThreadPreferencesRemainValid(ctx, definition); err != nil {
			return ProviderDefinitionMutationResult{}, err
		}
		definition.Revision = current.Revision + 1
		collection.Providers[index] = definition
	}
	if collection.Revision >= modelregistry.MaxProviderDefinitionRevision {
		return ProviderDefinitionMutationResult{}, apperror.New(apperror.CodeResourceExhausted,
			"custom Provider definition collection revision is exhausted")
	}
	collection.Revision++
	sort.Slice(collection.Providers, func(i, j int) bool {
		return collection.Providers[i].ID < collection.Providers[j].ID
	})
	return s.persistAndReloadLocked(ctx, collection, definition, "")
}

func (s *ProviderDefinitionService) Delete(ctx context.Context,
	request ProviderDefinitionDeleteRequest,
) (ProviderDefinitionMutationResult, error) {
	if s == nil || s.store == nil || s.registry == nil {
		return ProviderDefinitionMutationResult{}, apperror.New(
			apperror.CodeFailedPrecondition, "custom Provider definition service is unavailable")
	}
	if ctx == nil || request.Version != ProviderDefinitionControlProtocolVersion || !request.Confirm ||
		request.ID != strings.TrimSpace(request.ID) ||
		!modelregistry.ValidCustomProviderID(request.ID) || request.ExpectedDefinitionRevision == 0 {
		return ProviderDefinitionMutationResult{}, apperror.New(
			apperror.CodeInvalidArgument, "custom Provider definition delete request is invalid")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	collection, err := s.readLocked(ctx)
	if err != nil {
		return ProviderDefinitionMutationResult{}, err
	}
	if request.ExpectedCollectionRevision != collection.Revision {
		return ProviderDefinitionMutationResult{}, apperror.New(apperror.CodeConflict,
			"custom Provider definition collection revision is stale")
	}
	index := providerDefinitionIndex(collection.Providers, request.ID)
	if index < 0 {
		return ProviderDefinitionMutationResult{}, apperror.New(apperror.CodeNotFound,
			"custom Provider definition was not found")
	}
	if collection.Providers[index].Revision != request.ExpectedDefinitionRevision {
		return ProviderDefinitionMutationResult{}, apperror.New(apperror.CodeConflict,
			"custom Provider definition revision is stale")
	}
	if reader, ok := s.store.(selectedThreadModelRouteReader); ok {
		preferences, readErr := reader.ListSelectedThreadModelRoutePreferences(ctx, request.ID)
		if readErr != nil {
			return ProviderDefinitionMutationResult{}, apperror.Wrap(
				apperror.CodeUnavailable, "Thread model route preferences could not be read", readErr)
		}
		if len(preferences) != 0 {
			return ProviderDefinitionMutationResult{}, apperror.New(
				apperror.CodeFailedPrecondition,
				"custom Provider is still selected by one or more Thread model routes")
		}
	}
	for _, route := range modelregistry.SupportedRouteNames() {
		value, found, readErr := s.store.GetProviderSetting(ctx, "route."+route)
		if readErr != nil {
			return ProviderDefinitionMutationResult{}, apperror.Wrap(
				apperror.CodeUnavailable, "model route status could not be read", readErr)
		}
		if !found {
			continue
		}
		ref, parseErr := llm.ParseModelRef(value)
		if parseErr == nil && ref.Provider == request.ID {
			return ProviderDefinitionMutationResult{}, apperror.New(
				apperror.CodeFailedPrecondition,
				fmt.Sprintf("custom Provider is still selected by the %s model route", route))
		}
	}
	collection.Providers = append(collection.Providers[:index], collection.Providers[index+1:]...)
	if collection.Revision >= modelregistry.MaxProviderDefinitionRevision {
		return ProviderDefinitionMutationResult{}, apperror.New(apperror.CodeResourceExhausted,
			"custom Provider definition collection revision is exhausted")
	}
	collection.Revision++
	return s.persistAndReloadLocked(ctx, collection, modelregistry.ProviderDefinition{}, request.ID)
}

func (s *ProviderDefinitionService) ensureThreadPreferencesRemainValid(ctx context.Context,
	definition modelregistry.ProviderDefinition,
) error {
	reader, ok := s.store.(selectedThreadModelRouteReader)
	if !ok {
		return nil
	}
	preferences, err := reader.ListSelectedThreadModelRoutePreferences(ctx, definition.ID)
	if err != nil {
		return apperror.Wrap(apperror.CodeUnavailable,
			"Thread model route preferences could not be read", err)
	}
	for _, preference := range preferences {
		retained := definition.Enabled
		if retained {
			retained = false
			for _, model := range definition.Models {
				if model == preference.Model {
					retained = true
					break
				}
			}
		}
		if !retained {
			return apperror.New(apperror.CodeFailedPrecondition,
				"custom Provider update would invalidate a selected Thread model route")
		}
	}
	return nil
}

func (s *ProviderDefinitionService) readLocked(ctx context.Context) (
	modelregistry.ProviderDefinitionCollection, error,
) {
	collection, err := modelregistry.ReadProviderDefinitions(ctx, s.store)
	if err != nil {
		return modelregistry.ProviderDefinitionCollection{}, apperror.Wrap(
			apperror.CodeFailedPrecondition,
			"durable custom Provider definitions are unavailable or invalid", err)
	}
	return collection, nil
}

func (s *ProviderDefinitionService) persistAndReloadLocked(ctx context.Context,
	collection modelregistry.ProviderDefinitionCollection,
	definition modelregistry.ProviderDefinition, deletedID string,
) (ProviderDefinitionMutationResult, error) {
	if collection.Revision == 0 {
		return ProviderDefinitionMutationResult{}, apperror.New(
			apperror.CodeInternal, "custom Provider definition mutation revision is invalid")
	}
	if err := s.store.CompareAndSwapProviderDefinitionCollection(ctx,
		collection.Revision-1, collection); err != nil {
		return ProviderDefinitionMutationResult{}, apperror.Normalize(err)
	}
	reload, err := s.registry.Reload(ctx, s.store)
	if err != nil {
		return ProviderDefinitionMutationResult{}, apperror.Wrap(apperror.CodeUnavailable,
			"custom Provider definitions were persisted but Registry reload was not applied", err)
	}
	if !reload.Reloaded || reload.ProtocolVersion != modelregistry.ReloadProtocolVersion ||
		reload.Generation == 0 || s.registry.Generation() < reload.Generation {
		return ProviderDefinitionMutationResult{}, apperror.New(apperror.CodeInternal,
			"custom Provider Registry reload returned an invalid generation")
	}
	return ProviderDefinitionMutationResult{
		ProtocolVersion: ProviderDefinitionControlProtocolVersion,
		Collection:      cloneProviderDefinitionCollection(collection),
		Definition:      cloneProviderDefinition(definition), DeletedID: deletedID,
		RegistryReloaded: true, RegistryGeneration: s.registry.Generation(),
	}, nil
}

func providerDefinitionIndex(definitions []modelregistry.ProviderDefinition, id string) int {
	for index := range definitions {
		if definitions[index].ID == id {
			return index
		}
	}
	return -1
}

func cloneProviderDefinitionCollection(source modelregistry.ProviderDefinitionCollection) modelregistry.ProviderDefinitionCollection {
	out := source
	out.Providers = make([]modelregistry.ProviderDefinition, len(source.Providers))
	for index := range source.Providers {
		out.Providers[index] = cloneProviderDefinition(source.Providers[index])
	}
	return out
}

func cloneProviderDefinition(source modelregistry.ProviderDefinition) modelregistry.ProviderDefinition {
	out := source
	out.Models = append([]string(nil), source.Models...)
	out.AdvancedConfig = append([]byte(nil), source.AdvancedConfig...)
	if out.Models == nil {
		out.Models = []string{}
	}
	return out
}
