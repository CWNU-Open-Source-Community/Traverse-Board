package application_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/modelregistry"
	"cyberagent-workbench/internal/store"
)

func TestInitialExplicitModelRouteCreatesPreferenceAndSurvivesIntoSuccessor(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "initial-thread-model-route.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	workspace := store.WorkspaceRecord{ID: "workspace-initial-thread-model-route",
		Name: "initial thread model route", RootPath: t.TempDir(), CreatedAt: time.Now().UTC()}
	if err := st.SaveWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	created, err := application.NewControlledRunCreationService(st).Create(ctx,
		application.ControlledRunCreationRequest{
			Version: domain.RunCreationProtocolVersion, Goal: "retain the initially selected model",
			WorkspaceID: workspace.ID, Profile: "code",
			ModelRoute:   "selected-provider/selected-model",
			OperationKey: "initial-thread-model-route-create-0001",
			RequestedBy:  "route_test_operator",
		})
	if err != nil {
		t.Fatal(err)
	}
	threadRecord, err := st.GetThreadByRun(ctx, created.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	preference, found, err := st.GetThreadModelRoutePreference(ctx, threadRecord.ID)
	if err != nil || !found || !preference.Selected ||
		preference.Provider != "selected-provider" || preference.Model != "selected-model" {
		t.Fatalf("initial route preference=%+v found=%t err=%v", preference, found, err)
	}
	if _, err := application.NewRunService(st).Cancel(ctx, created.Run.ID); err != nil {
		t.Fatal(err)
	}
	successor, err := application.NewThreadService(st).
		WithModelRouteRegistry(newMutableThreadModelRouteRegistry()).Submit(ctx,
		application.SubmitThreadMessageRequest{
			Version: domain.ThreadMessageProtocolVersion, ThreadID: threadRecord.ID,
			Content:      "continue on the initially selected model",
			OperationKey: "initial-thread-model-route-successor-0001",
			RequestedBy:  "route_test_operator",
		})
	if err != nil {
		t.Fatal(err)
	}
	if !successor.SuccessorCreated ||
		successor.Run.Config.ModelRoute != "selected-provider/selected-model" ||
		successor.Session.Route != successor.Run.Config.ModelRoute {
		t.Fatalf("successor did not retain initial route: %+v", successor)
	}
}

type mutableThreadModelRouteRegistry struct {
	mu       sync.RWMutex
	router   *llm.Router
	snapshot modelregistry.Snapshot
}

func newMutableThreadModelRouteRegistry() *mutableThreadModelRouteRegistry {
	router := llm.NewRouter(llm.ModelRef{Provider: "global-provider", Model: "global-model"})
	router.SetRoute("code", llm.ModelRef{Provider: "global-provider", Model: "global-model"})
	return &mutableThreadModelRouteRegistry{
		router: router,
		snapshot: modelregistry.Snapshot{
			ProtocolVersion: modelregistry.ProtocolVersion,
			Generation:      1,
			Providers: []modelregistry.ProviderAvailability{{
				Name: "selected-provider", DisplayName: "Selected Provider",
				Kind:   modelregistry.ProviderKindOpenAICompatible,
				Status: modelregistry.ProviderAvailable, Models: []string{"selected-model"},
				CredentialSource: "test", NetworkRequired: true, Enabled: true,
				Harnesses: []modelregistry.HarnessAvailability{{
					ProtocolVersion: modelregistry.HarnessQualificationProtocolVersion,
					Model:           "selected-model", RootEligible: true,
					LatestQualificationStatus: modelregistry.QualificationStatusAvailable,
				}},
			}},
			Routes: []modelregistry.RouteAvailability{{
				Name: "code", Provider: "global-provider", Model: "global-model",
				Available: true, HarnessReady: true,
			}},
		},
	}
}

func (r *mutableThreadModelRouteRegistry) Snapshot() modelregistry.Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snapshot
}

func (r *mutableThreadModelRouteRegistry) Router() *llm.Router { return r.router }

func (r *mutableThreadModelRouteRegistry) makeSelectedRouteIneligible() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.snapshot.Generation++
	r.snapshot.Providers[0].Enabled = false
	r.snapshot.Providers[0].Status = modelregistry.ProviderNotConfigured
}

func TestThreadModelRouteSelectionAndResetMaterializeOnlyIntoSuccessors(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "thread-model-route.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	runs := application.NewRunService(st)
	mission, predecessor, err := runs.Create(ctx, application.CreateRunRequest{
		Goal: "route a durable Thread", Profile: "code",
		Budget: domain.Budget{MaxTurns: 4},
	})
	if err != nil {
		t.Fatal(err)
	}
	threadRecord, err := st.GetThreadByRun(ctx, predecessor.ID)
	if err != nil {
		t.Fatal(err)
	}
	registry := newMutableThreadModelRouteRegistry()
	routes := application.NewThreadModelRouteService(st, registry)
	selected, err := routes.Change(ctx, application.ChangeThreadModelRouteRequest{
		Version: domain.ThreadModelRouteControlProtocolVersion, ThreadID: threadRecord.ID,
		Action: domain.ThreadModelRouteSelect, Provider: "selected-provider",
		Model: "selected-model", OperationKey: "select-successor-route-0001",
		RequestedBy: "route_test_operator",
	})
	if err != nil {
		t.Fatal(err)
	}
	if selected.AppliesTo != "next_run" || !selected.ActiveRunUnchanged ||
		predecessor.Config.ModelRoute != "code" {
		t.Fatalf("selection mutated the current Run or reported the wrong boundary: %+v run=%+v",
			selected, predecessor)
	}
	predecessor, err = runs.Start(ctx, predecessor.ID)
	if err == nil {
		predecessor, err = runs.Complete(ctx, predecessor.ID)
	}
	if err != nil {
		t.Fatal(err)
	}

	threads := application.NewThreadService(st).WithModelRouteRegistry(registry)
	selectedSuccessor, err := threads.Submit(ctx, application.SubmitThreadMessageRequest{
		Version: domain.ThreadMessageProtocolVersion, ThreadID: threadRecord.ID,
		Content:      "continue with the selected model",
		OperationKey: "selected-route-successor-message-0001",
		RequestedBy:  "route_test_operator",
	})
	if err != nil {
		t.Fatal(err)
	}
	const selectedRoute = "selected-provider/selected-model"
	if !selectedSuccessor.SuccessorCreated ||
		selectedSuccessor.PredecessorRunID != predecessor.ID ||
		selectedSuccessor.Run.Config.ModelRoute != selectedRoute ||
		selectedSuccessor.Session.Route != selectedRoute {
		t.Fatalf("selected route was not atomically bound to successor Run and Session: %+v",
			selectedSuccessor)
	}
	selectedSuccessor.Run, err = runs.Cancel(ctx, selectedSuccessor.Run.ID)
	if err != nil {
		t.Fatal(err)
	}

	reset, err := routes.Change(ctx, application.ChangeThreadModelRouteRequest{
		Version: domain.ThreadModelRouteControlProtocolVersion, ThreadID: threadRecord.ID,
		Action: domain.ThreadModelRouteReset, OperationKey: "reset-successor-route-0001",
		RequestedBy: "route_test_operator",
	})
	if err != nil {
		t.Fatal(err)
	}
	if reset.Provider != "global-provider" || reset.Model != "global-model" ||
		reset.Source != "default" {
		t.Fatalf("reset did not resolve the Mission profile's global route: %+v", reset)
	}
	defaultSuccessor, err := threads.Submit(ctx, application.SubmitThreadMessageRequest{
		Version: domain.ThreadMessageProtocolVersion, ThreadID: threadRecord.ID,
		Content:      "continue with the profile default",
		OperationKey: "reset-route-successor-message-0001",
		RequestedBy:  "route_test_operator",
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved := registry.Router().Resolve(defaultSuccessor.Run.Config.ModelRoute)
	if !defaultSuccessor.SuccessorCreated ||
		defaultSuccessor.PredecessorRunID != selectedSuccessor.Run.ID ||
		defaultSuccessor.Run.Config.ModelRoute != string(mission.Profile) ||
		defaultSuccessor.Session.Route != string(mission.Profile) ||
		resolved != (llm.ModelRef{Provider: "global-provider", Model: "global-model"}) {
		t.Fatalf("reset tombstone did not restore the Mission profile/global route: result=%+v resolved=%+v",
			defaultSuccessor, resolved)
	}
}

func TestThreadSuccessorFailsClosedWhenSelectedRouteBecomesIneligible(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "thread-model-route-ineligible.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	runs := application.NewRunService(st)
	_, predecessor, err := runs.Create(ctx, application.CreateRunRequest{
		Goal: "fail closed after Registry drift", Profile: "code",
		Budget: domain.Budget{MaxTurns: 4},
	})
	if err != nil {
		t.Fatal(err)
	}
	threadRecord, err := st.GetThreadByRun(ctx, predecessor.ID)
	if err != nil {
		t.Fatal(err)
	}
	registry := newMutableThreadModelRouteRegistry()
	if _, err := application.NewThreadModelRouteService(st, registry).Change(ctx,
		application.ChangeThreadModelRouteRequest{
			Version: domain.ThreadModelRouteControlProtocolVersion, ThreadID: threadRecord.ID,
			Action: domain.ThreadModelRouteSelect, Provider: "selected-provider",
			Model: "selected-model", OperationKey: "select-before-registry-drift-0001",
			RequestedBy: "route_test_operator",
		}); err != nil {
		t.Fatal(err)
	}
	predecessor, err = runs.Cancel(ctx, predecessor.ID)
	if err != nil {
		t.Fatal(err)
	}
	registry.makeSelectedRouteIneligible()

	_, err = application.NewThreadService(st).WithModelRouteRegistry(registry).Submit(ctx,
		application.SubmitThreadMessageRequest{
			Version: domain.ThreadMessageProtocolVersion, ThreadID: threadRecord.ID,
			Content:      "must not create an ineligible successor",
			OperationKey: "ineligible-route-successor-message-0001",
			RequestedBy:  "route_test_operator",
		})
	if apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("ineligible selected route did not fail closed: %v", err)
	}
	after, getErr := st.GetThread(ctx, threadRecord.ID)
	bindings, listErr := st.ListThreadRuns(ctx, threadRecord.ID)
	if getErr != nil || listErr != nil || after.ActiveRunID != "" ||
		after.LastRunID != predecessor.ID || len(bindings) != 1 {
		t.Fatalf("failed successor changed durable Thread state: thread=%+v bindings=%+v getErr=%v listErr=%v",
			after, bindings, getErr, listErr)
	}
}

func TestThreadModelRouteCatalogNormalizesHarnessUnavailableReasons(t *testing.T) {
	registry := &mutableThreadModelRouteRegistry{
		router: llm.NewDefaultRouter(),
		snapshot: modelregistry.Snapshot{
			ProtocolVersion: modelregistry.ProtocolVersion,
			Generation:      7,
			Providers: []modelregistry.ProviderAvailability{
				{
					Name: "official-deepseek", DisplayName: "DeepSeek",
					Kind:   modelregistry.ProviderKindAnthropicCompatible,
					Status: modelregistry.ProviderAvailable, Models: []string{
						"deepseek-v4-flash", "deepseek-v4-pro",
					},
					CredentialSource: "system", NetworkRequired: true, Enabled: true,
					Harnesses: []modelregistry.HarnessAvailability{{
						ProtocolVersion:     modelregistry.HarnessQualificationProtocolVersion,
						Model:               "deepseek-v4-flash",
						QualificationStatus: llm.HarnessQualificationRequired,
					}},
				},
				{
					Name: "temporarily-unavailable", DisplayName: "Unavailable Provider",
					Kind: modelregistry.ProviderKindLocal, Status: "unavailable",
					Models: []string{"local-model"}, CredentialSource: "none",
					Enabled: true, Harnesses: []modelregistry.HarnessAvailability{{
						ProtocolVersion: modelregistry.HarnessQualificationProtocolVersion,
						Model:           "local-model", RootEligible: true,
						LatestQualificationStatus: modelregistry.QualificationStatusAvailable,
					}},
				},
			},
		},
	}

	catalog, err := application.NewThreadModelRouteService(nil, registry).Catalog(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	reasons := make(map[string]string, len(catalog.Routes))
	for _, route := range catalog.Routes {
		reasons[route.ProviderID+"/"+route.Model] = route.UnavailableReason
	}
	for route, want := range map[string]string{
		"official-deepseek/deepseek-v4-flash": "harness_qualification_required",
		"official-deepseek/deepseek-v4-pro":   "harness_qualification_required",
		"temporarily-unavailable/local-model": "provider_unavailable",
	} {
		if got := reasons[route]; got != want {
			t.Errorf("route %s unavailable reason=%q, want %q", route, got, want)
		}
	}
}
