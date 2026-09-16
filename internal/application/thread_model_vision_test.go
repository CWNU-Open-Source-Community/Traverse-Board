package application_test

import (
	"path/filepath"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/store"
)

type declaredVisionProjectionProvider struct{ llm.MockProvider }

func (declaredVisionProjectionProvider) Name() string { return "selected-provider" }
func (declaredVisionProjectionProvider) DescribeVision(model string) llm.VisionCapability {
	if model == "selected-model" {
		return llm.VisionCapability{State: llm.VisionSupported, Source: "operator_declared"}
	}
	return llm.VisionCapability{State: llm.VisionUnknown, Source: "unknown"}
}

func TestThreadModelVisionProjectsExactSelectedRouteWithoutChangingActiveModel(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "model-vision.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_, run, err := application.NewRunService(st).Create(t.Context(), application.CreateRunRequest{Goal: "inspect route metadata", Profile: "code", Budget: domain.Budget{MaxTurns: 4}})
	if err != nil {
		t.Fatal(err)
	}
	thread, err := st.GetThreadByRun(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	registry := newMutableThreadModelRouteRegistry()
	registry.router.RegisterProvider(declaredVisionProjectionProvider{})
	service := application.NewThreadModelRouteService(st, registry)
	catalog, err := service.Catalog(t.Context())
	if err != nil || len(catalog.Routes) != 1 {
		t.Fatalf("catalog=%+v err=%v", catalog, err)
	}
	if got := catalog.Routes[0].VisionCapability; got.State != llm.VisionSupported || got.Source != "operator_declared" {
		t.Fatalf("catalog vision=%+v", got)
	}
	initial, err := service.Get(t.Context(), thread.ID)
	if err != nil || initial.VisionCapability.State != llm.VisionUnknown {
		t.Fatalf("initial=%+v err=%v", initial, err)
	}
	selected, err := service.Change(t.Context(), application.ChangeThreadModelRouteRequest{Version: domain.ThreadModelRouteControlProtocolVersion, ThreadID: thread.ID, Action: domain.ThreadModelRouteSelect, Provider: "selected-provider", Model: "selected-model", OperationKey: "select-exact-vision-model-0001", RequestedBy: "route_test_operator"})
	if err != nil {
		t.Fatal(err)
	}
	if selected.VisionCapability.State != llm.VisionSupported || !selected.ActiveRunUnchanged || selected.AppliesTo != "next_run" {
		t.Fatalf("selected=%+v", selected)
	}
	readback, err := service.Get(t.Context(), thread.ID)
	if err != nil || readback.VisionCapability != selected.VisionCapability {
		t.Fatalf("readback=%+v err=%v", readback, err)
	}
	unchanged, err := st.GetRun(t.Context(), run.ID)
	if err != nil || unchanged.Config.ModelRoute != run.Config.ModelRoute {
		t.Fatal("reading capability changed existing run")
	}
}
