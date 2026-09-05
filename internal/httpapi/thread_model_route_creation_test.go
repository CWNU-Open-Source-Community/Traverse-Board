package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
)

type threadCreationModelRouteCatalogStub struct {
	catalog application.ModelRouteCatalog
}

func (s threadCreationModelRouteCatalogStub) Catalog(context.Context) (
	application.ModelRouteCatalog, error,
) {
	return s.catalog, nil
}

func (threadCreationModelRouteCatalogStub) Get(context.Context, string) (
	application.ThreadModelRouteView, error,
) {
	return application.ThreadModelRouteView{}, nil
}

func (threadCreationModelRouteCatalogStub) Change(context.Context,
	application.ChangeThreadModelRouteRequest,
) (application.ThreadModelRouteView, error) {
	return application.ThreadModelRouteView{}, nil
}

func TestThreadHTTPCreationAtomicallyPinsExplicitProviderModelAndReplays(t *testing.T) {
	fixture := newAPIFixture(t)
	controller := threadCreationModelRouteCatalogStub{catalog: application.ModelRouteCatalog{
		ProtocolVersion: application.ModelRouteCatalogProtocolVersion, Generation: 1,
		Routes: []application.ModelRouteCatalogItem{
			{ProviderID: "provider-one", ProviderName: "Provider One",
				Model: "model-one", Enabled: true, HarnessReady: true, Selectable: true},
			{ProviderID: "provider-two", ProviderName: "Provider Two",
				Model: "model-two", Enabled: true, HarnessReady: true, Selectable: true},
		},
	}}
	api, err := New(fixture.store, Config{AccessToken: testAccessToken,
		ControlToken: testControlToken, RunCreationEnabled: true,
		SessionMessageEnabled: true, ThreadModelRouteController: controller,
		AppVersion: "thread-explicit-model-route-test"})
	if err != nil {
		t.Fatal(err)
	}
	request := ThreadCreationControlRequestView{
		Version: domain.ThreadCreationProtocolVersion,
		Goal:    "pin the first Thread model route", WorkspaceID: fixture.workspace.ID,
		Profile: "code", Provider: "provider-one", Model: "model-one",
	}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	direct, err := application.NewControlledRunCreationService(fixture.store).Create(
		t.Context(), application.ControlledRunCreationRequest{
			Version: domain.RunCreationProtocolVersion, Goal: request.Goal,
			WorkspaceID: request.WorkspaceID, Profile: request.Profile,
			ModelRoute:   "provider-one/model-one",
			OperationKey: "thread-explicit-model-route-direct-0001",
			RequestedBy:  "http_thread_operator",
		})
	if err != nil {
		t.Fatalf("direct controlled creation with an explicit route failed: %v", err)
	}
	if direct.Run.Config.ModelRoute != "provider-one/model-one" ||
		direct.Session.Route != direct.Run.Config.ModelRoute {
		t.Fatalf("direct controlled creation route binding drifted: %+v", direct)
	}
	const operationKey = "thread-explicit-model-route-create-0001"
	response := performSessionMessageRequest(t, api, http.MethodPost, ThreadCollectionPath,
		testControlToken, operationKey, "application/json", bytes.NewReader(body))
	var created ThreadCreationControlView
	decodeDataStatus(t, response, http.StatusAccepted, &created)
	const route = "provider-one/model-one"
	storedRun, runErr := fixture.store.GetRun(t.Context(), created.Run.ID)
	storedSession, sessionErr := fixture.store.GetSession(t.Context(), created.Session.ID)
	storedThread, threadErr := fixture.store.GetThreadByRun(t.Context(), created.Run.ID)
	preference, preferenceFound, preferenceErr :=
		fixture.store.GetThreadModelRoutePreference(t.Context(), storedThread.ID)
	if created.Replayed || created.Run.Config.ModelRoute != route ||
		created.Session.Route != route || runErr != nil || sessionErr != nil || threadErr != nil ||
		preferenceErr != nil || !preferenceFound || !preference.Selected ||
		preference.Provider != "provider-one" || preference.Model != "model-one" ||
		storedRun.Config.ModelRoute != route || storedSession.Route != route ||
		storedRun.SessionID != storedSession.ID || storedThread.ActiveRunID != storedRun.ID {
		t.Fatalf("explicit route was not committed with initial Thread/Run/Session/preference: created=%+v run=%+v session=%+v thread=%+v preference=%+v found=%t errors=%v/%v/%v/%v",
			created, storedRun, storedSession, storedThread, preference, preferenceFound,
			runErr, sessionErr, threadErr, preferenceErr)
	}

	replayResponse := performSessionMessageRequest(t, api, http.MethodPost,
		ThreadCollectionPath, testControlToken, operationKey, "application/json",
		bytes.NewReader(body))
	var replayed ThreadCreationControlView
	decodeDataStatus(t, replayResponse, http.StatusAccepted, &replayed)
	if !replayed.Replayed || replayed.Thread.ID != created.Thread.ID ||
		replayed.Run.ID != created.Run.ID || replayed.Session.ID != created.Session.ID ||
		replayed.Run.Config.ModelRoute != route || replayed.Session.Route != route {
		t.Fatalf("same-key same-route replay changed the initial binding: %+v", replayed)
	}

	request.Provider, request.Model = "provider-two", "model-two"
	changedBody, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	conflict := performSessionMessageRequest(t, api, http.MethodPost, ThreadCollectionPath,
		testControlToken, operationKey, "application/json", bytes.NewReader(changedBody))
	assertAPIError(t, conflict, http.StatusConflict, "CONFLICT")
	bindings, err := fixture.store.ListThreadRuns(t.Context(), created.Thread.ID)
	if err != nil || len(bindings) != 1 || bindings[0].RunID != created.Run.ID {
		t.Fatalf("same-key different-route conflict created extra durable state: bindings=%+v err=%v",
			bindings, err)
	}
}

func TestThreadHTTPCreationResolvesStaleProfileDefaultToSelectableModel(t *testing.T) {
	fixture := newAPIFixture(t)
	controller := threadCreationModelRouteCatalogStub{catalog: application.ModelRouteCatalog{
		ProtocolVersion: application.ModelRouteCatalogProtocolVersion, Generation: 2,
		Routes: []application.ModelRouteCatalogItem{
			{ProviderID: "legacy-deepseek", ProviderName: "Legacy DeepSeek",
				Model: "deepseek-v4-flash", Enabled: true, HarnessReady: false,
				Selectable: false, DefaultForRoutes: []string{"code"}},
			{ProviderID: "official-deepseek", ProviderName: "DeepSeek",
				Model: "deepseek-chat", Enabled: true, HarnessReady: true, Selectable: true},
		},
	}}
	api, err := New(fixture.store, Config{AccessToken: testAccessToken,
		ControlToken: testControlToken, RunCreationEnabled: true,
		SessionMessageEnabled: true, ThreadModelRouteController: controller,
		AppVersion: "thread-default-model-route-test"})
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(ThreadCreationControlRequestView{
		Version: domain.ThreadCreationProtocolVersion,
		Goal:    "create with a live default model", WorkspaceID: fixture.workspace.ID,
		Profile: "code",
	})
	if err != nil {
		t.Fatal(err)
	}
	response := performSessionMessageRequest(t, api, http.MethodPost, ThreadCollectionPath,
		testControlToken, "thread-live-default-route-create-0001", "application/json",
		bytes.NewReader(body))
	var created ThreadCreationControlView
	decodeDataStatus(t, response, http.StatusAccepted, &created)
	if created.Run.Config.ModelRoute != "official-deepseek/deepseek-chat" ||
		created.Session.Route != created.Run.Config.ModelRoute {
		t.Fatalf("new Thread retained stale profile route: %+v", created)
	}
	preference, found, err := fixture.store.GetThreadModelRoutePreference(
		t.Context(), created.Thread.ID)
	if err != nil || !found || !preference.Selected ||
		preference.Provider != "official-deepseek" || preference.Model != "deepseek-chat" {
		t.Fatalf("resolved default was not pinned: preference=%+v found=%t err=%v",
			preference, found, err)
	}
}
