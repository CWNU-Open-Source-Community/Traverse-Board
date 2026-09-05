package store

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/modelregistry"
	"cyberagent-workbench/internal/runmutation"
)

func atomicityProviderDefinition(revision uint64) modelregistry.ProviderDefinition {
	return modelregistry.ProviderDefinition{
		Version: modelregistry.ProviderDefinitionVersion, Revision: revision,
		ID: "custom-atomic", DisplayName: "Atomic Provider",
		EndpointURL: "https://models.example.test/v1", DefaultModel: "model-one",
		Models:                    []string{"model-one", "model-two"},
		Transport:                 modelregistry.ProviderTransportOpenAIChatCompletions,
		SearchMode:                modelregistry.ProviderSearchModeDisabled,
		NativeWebSearchCapability: modelregistry.NativeWebSearchUnsupported,
		AdvancedConfig:            []byte(`{}`), Enabled: true,
	}
}

func atomicityCollection(revision uint64,
	definitions ...modelregistry.ProviderDefinition,
) modelregistry.ProviderDefinitionCollection {
	return modelregistry.ProviderDefinitionCollection{
		Version:  modelregistry.ProviderDefinitionCollectionVersion,
		Revision: revision, Providers: definitions,
	}
}

func threadModelRouteAtomicityFixture(t *testing.T, path string) (
	*SQLiteStore, domain.Thread,
) {
	t.Helper()
	state, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_, run, err := application.NewRunService(state).Create(t.Context(),
		application.CreateRunRequest{Goal: "route atomicity", Profile: "review",
			Budget: domain.Budget{MaxTurns: 2}})
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	threadRecord, err := state.GetThreadByRun(t.Context(), run.ID)
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	return state, threadRecord
}

func atomicitySelectMutation(threadID, operationKey string) domain.ThreadModelRouteMutation {
	return domain.ThreadModelRouteMutation{
		Version: domain.ThreadModelRouteControlProtocolVersion, ThreadID: threadID,
		Action: domain.ThreadModelRouteSelect, Provider: "custom-atomic", Model: "model-one",
		CustomProvider: true, ExpectedProviderDefinitionRevision: 1,
		OperationKey:       operationKey,
		RequestFingerprint: runmutation.Fingerprint("route-atomicity", operationKey),
		RequestedBy:        "test-operator", At: time.Now().UTC(),
	}
}

func TestThreadModelRouteSelectionRejectsStaleCustomProviderDefinition(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stale-definition.db")
	state, threadRecord := threadModelRouteAtomicityFixture(t, path)
	defer state.Close()
	ctx := t.Context()
	definition := atomicityProviderDefinition(1)
	if err := state.CompareAndSwapProviderDefinitionCollection(ctx, 0,
		atomicityCollection(1, definition)); err != nil {
		t.Fatal(err)
	}
	updated := definition
	updated.Revision = 2
	updated.Models = []string{"model-two"}
	updated.DefaultModel = "model-two"
	if err := state.CompareAndSwapProviderDefinitionCollection(ctx, 1,
		atomicityCollection(2, updated)); err != nil {
		t.Fatal(err)
	}
	_, err := state.ChangeThreadModelRoutePreference(ctx,
		atomicitySelectMutation(threadRecord.ID, "stale-definition-select"))
	if apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("stale custom Provider revision was persisted: %v", err)
	}
	if _, found, err := state.GetThreadModelRoutePreference(ctx, threadRecord.ID); err != nil || found {
		t.Fatalf("stale selection left a durable preference: found=%t err=%v", found, err)
	}
}

func TestProviderDefinitionCASRejectsInvalidatingSelectedThreadRoute(t *testing.T) {
	path := filepath.Join(t.TempDir(), "definition-guard.db")
	state, threadRecord := threadModelRouteAtomicityFixture(t, path)
	defer state.Close()
	ctx := t.Context()
	definition := atomicityProviderDefinition(1)
	if err := state.CompareAndSwapProviderDefinitionCollection(ctx, 0,
		atomicityCollection(1, definition)); err != nil {
		t.Fatal(err)
	}
	if _, err := state.ChangeThreadModelRoutePreference(ctx,
		atomicitySelectMutation(threadRecord.ID, "definition-guard-select")); err != nil {
		t.Fatal(err)
	}
	for name, next := range map[string]modelregistry.ProviderDefinitionCollection{
		"delete": atomicityCollection(2),
		"disable": func() modelregistry.ProviderDefinitionCollection {
			disabled := definition
			disabled.Revision = 2
			disabled.Enabled = false
			return atomicityCollection(2, disabled)
		}(),
		"remove model": func() modelregistry.ProviderDefinitionCollection {
			changed := definition
			changed.Revision = 2
			changed.Models = []string{"model-two"}
			changed.DefaultModel = "model-two"
			return atomicityCollection(2, changed)
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			if err := state.CompareAndSwapProviderDefinitionCollection(ctx, 1, next); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
				t.Fatalf("invalidating definition mutation was accepted: %v", err)
			}
		})
	}
	collection, err := modelregistry.ReadProviderDefinitions(ctx, state)
	if err != nil || collection.Revision != 1 || len(collection.Providers) != 1 {
		t.Fatalf("rejected mutation changed collection: %#v err=%v", collection, err)
	}
}

func TestThreadRouteSelectAndProviderDeleteShareAtomicBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent-route-definition.db")
	first, threadRecord := threadModelRouteAtomicityFixture(t, path)
	defer first.Close()
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	ctx := context.Background()
	if err := first.CompareAndSwapProviderDefinitionCollection(ctx, 0,
		atomicityCollection(1, atomicityProviderDefinition(1))); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		<-start
		_, err := first.ChangeThreadModelRoutePreference(ctx,
			atomicitySelectMutation(threadRecord.ID, "concurrent-select"))
		results <- err
	}()
	go func() {
		defer workers.Done()
		<-start
		results <- second.CompareAndSwapProviderDefinitionCollection(ctx, 1,
			atomicityCollection(2))
	}()
	close(start)
	workers.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
			continue
		}
		if code := apperror.CodeOf(apperror.Normalize(err)); code != apperror.CodeConflict && code != apperror.CodeFailedPrecondition {
			t.Fatalf("unexpected concurrent mutation failure: %v", err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("atomic boundary allowed %d successful conflicting mutations", succeeded)
	}

	collection, err := modelregistry.ReadProviderDefinitions(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	preference, found, err := first.GetThreadModelRoutePreference(ctx, threadRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(collection.Providers) == 0 && found && preference.Selected {
		t.Fatalf("durable preference points to deleted custom Provider: %#v", preference)
	}
	if len(collection.Providers) == 1 && (!found || !preference.Selected) {
		t.Fatalf("Provider survived without the concurrently selected preference")
	}
}

func TestInitialThreadRouteAndProviderInvalidationShareAtomicBoundary(t *testing.T) {
	for name, nextCollection := range map[string]func(modelregistry.ProviderDefinition) modelregistry.ProviderDefinitionCollection{
		"delete": func(modelregistry.ProviderDefinition) modelregistry.ProviderDefinitionCollection {
			return atomicityCollection(2)
		},
		"disable": func(definition modelregistry.ProviderDefinition) modelregistry.ProviderDefinitionCollection {
			definition.Revision = 2
			definition.Enabled = false
			return atomicityCollection(2, definition)
		},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "initial-route-definition.db")
			first, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer first.Close()
			second, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer second.Close()
			ctx := context.Background()
			definition := atomicityProviderDefinition(1)
			if err := first.CompareAndSwapProviderDefinitionCollection(ctx, 0,
				atomicityCollection(1, definition)); err != nil {
				t.Fatal(err)
			}
			workspace := WorkspaceRecord{ID: "workspace-initial-route-" + name,
				Name: "initial route " + name, RootPath: t.TempDir(), CreatedAt: time.Now().UTC()}
			if err := first.SaveWorkspace(ctx, workspace); err != nil {
				t.Fatal(err)
			}

			start := make(chan struct{})
			createResults := make(chan application.ControlledRunCreationResult, 1)
			createErrors := make(chan error, 1)
			definitionErrors := make(chan error, 1)
			go func() {
				<-start
				created, createErr := application.NewControlledRunCreationService(first).Create(ctx,
					application.ControlledRunCreationRequest{
						Version: domain.RunCreationProtocolVersion,
						Goal:    "atomically pin initial custom Provider", WorkspaceID: workspace.ID,
						Profile: "code", ModelRoute: "custom-atomic/model-one",
						CustomModelProvider: true, ExpectedProviderDefinitionRevision: 1,
						OperationKey: "initial-route-create-" + name,
						RequestedBy:  "test-operator",
					})
				createResults <- created
				createErrors <- createErr
			}()
			go func() {
				<-start
				definitionErrors <- second.CompareAndSwapProviderDefinitionCollection(
					ctx, 1, nextCollection(definition))
			}()
			close(start)
			created := <-createResults
			createErr := <-createErrors
			definitionErr := <-definitionErrors

			if (createErr == nil) == (definitionErr == nil) {
				t.Fatalf("expected exactly one conflicting mutation to succeed: create=%v definition=%v",
					createErr, definitionErr)
			}
			collection, err := modelregistry.ReadProviderDefinitions(ctx, first)
			if err != nil {
				t.Fatal(err)
			}
			if createErr == nil {
				threadRecord, err := first.GetThreadByRun(ctx, created.Run.ID)
				if err != nil {
					t.Fatal(err)
				}
				preference, found, err := first.GetThreadModelRoutePreference(ctx, threadRecord.ID)
				if err != nil || !found || !preference.Selected ||
					preference.Provider != definition.ID || preference.Model != "model-one" ||
					len(collection.Providers) != 1 || !collection.Providers[0].Enabled {
					t.Fatalf("successful creation was not protected: preference=%+v found=%t collection=%+v err=%v",
						preference, found, collection, err)
				}
				if apperror.CodeOf(apperror.Normalize(definitionErr)) != apperror.CodeFailedPrecondition {
					t.Fatalf("Provider invalidation failed with unexpected error: %v", definitionErr)
				}
				return
			}
			if len(collection.Providers) != 0 && collection.Providers[0].Enabled {
				t.Fatalf("successful invalidation did not persist: %+v", collection)
			}
			if code := apperror.CodeOf(apperror.Normalize(createErr)); code != apperror.CodeConflict && code != apperror.CodeFailedPrecondition {
				t.Fatalf("initial creation failed with unexpected error: %v", createErr)
			}
			operation, found, err := first.GetRunCreationOperation(ctx,
				runmutation.RunCreationOperationDigest("initial-route-create-"+name))
			if err != nil || found {
				t.Fatalf("failed creation left a Run operation: operation=%+v found=%t err=%v",
					operation, found, err)
			}
		})
	}
}
