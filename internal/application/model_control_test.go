package application

import (
	"context"
	"testing"

	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/modelregistry"
)

type modelRouteSettings map[string]string

func (s modelRouteSettings) SetProviderSetting(_ context.Context, key string, value string) error {
	s[key] = value
	return nil
}

func TestModelControlSelectsPersistedAvailableRoute(t *testing.T) {
	registry := modelregistry.New(nil)
	settings := modelRouteSettings{}
	service := NewModelControlService(registry, settings)
	selected, err := service.SelectRoute(context.Background(), SelectModelRouteRequest{
		Version: modelregistry.RouteControlProtocolVersion,
		Route:   "review", Provider: "mock", Model: "mock-fast",
	})
	if err != nil {
		t.Fatal(err)
	}
	if settings["route.review"] != "mock/mock-fast" || !selected.Available ||
		registry.Router().Resolve("review").Model != "mock-fast" {
		t.Fatalf("unexpected model route selection: %#v %#v", settings, selected)
	}
	if _, err := service.SelectRoute(context.Background(), SelectModelRouteRequest{
		Version: modelregistry.RouteControlProtocolVersion,
		Route:   "review", Provider: "mimo", Model: modelregistry.DefaultMimoModel,
	}); err == nil {
		t.Fatal("unconfigured Provider route was accepted")
	}
}

func TestModelControlRequiresExplicitDiagnosticConfirmation(t *testing.T) {
	service := NewModelControlService(modelregistry.New(nil), modelRouteSettings{})
	if _, err := service.Diagnose(context.Background(), DiagnoseProviderRequest{
		Version:  modelregistry.DiagnosticProtocolVersion,
		Provider: "mock", Model: "mock-fast",
	}); err == nil {
		t.Fatal("diagnostic without explicit confirmation was accepted")
	}
	result, err := service.Diagnose(context.Background(), DiagnoseProviderRequest{
		Version:  modelregistry.DiagnosticProtocolVersion,
		Provider: "mock", Model: "mock-fast", ConfirmDiagnostic: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != modelregistry.DiagnosticReachable ||
		result.ResponseContentReturned || result.ToolCalled {
		t.Fatalf("unexpected diagnostic result: %#v", result)
	}
}

func TestModelControlReturnsUnconfiguredProviderFacts(t *testing.T) {
	service := NewModelControlService(modelregistry.New(nil), modelRouteSettings{})
	diagnostic, err := service.Diagnose(context.Background(), DiagnoseProviderRequest{
		Version:  modelregistry.DiagnosticProtocolVersion,
		Provider: "openai", Model: modelregistry.DefaultOpenAIModel,
		ConfirmDiagnostic: true,
	})
	if err != nil || diagnostic.FailureReason != llm.ProviderFailureNotConfigured ||
		diagnostic.ModelCalled || diagnostic.NetworkRequestAttempted {
		t.Fatalf("unexpected unconfigured diagnostic: %#v err=%v", diagnostic, err)
	}
	qualification, err := service.QualifyHarness(context.Background(),
		QualifyModelHarnessRequest{
			Version:  modelregistry.HarnessQualificationProtocolVersion,
			Provider: "openai", Model: modelregistry.DefaultOpenAIModel,
			ConfirmQualification: true,
		})
	if err != nil || qualification.FailureReason != llm.ProviderFailureNotConfigured ||
		qualification.ModelCalls != 0 || qualification.NetworkRequestAttempted {
		t.Fatalf("unexpected unconfigured qualification: %#v err=%v", qualification, err)
	}
	if _, err := service.Diagnose(context.Background(), DiagnoseProviderRequest{
		Version:  modelregistry.DiagnosticProtocolVersion,
		Provider: "unknown", Model: modelregistry.DefaultOpenAIModel,
		ConfirmDiagnostic: true,
	}); err == nil {
		t.Fatal("unknown Provider diagnostic was accepted")
	}
}

func TestModelControlSeparatesHarnessQualificationFromConnectivity(t *testing.T) {
	service := NewModelControlService(modelregistry.New(nil), modelRouteSettings{})
	if _, err := service.QualifyHarness(context.Background(), QualifyModelHarnessRequest{
		Version:  modelregistry.HarnessQualificationProtocolVersion,
		Provider: "mock", Model: "mock-code",
	}); err == nil {
		t.Fatal("Harness qualification without explicit confirmation was accepted")
	}
	result, err := service.QualifyHarness(context.Background(), QualifyModelHarnessRequest{
		Version:  modelregistry.HarnessQualificationProtocolVersion,
		Provider: "mock", Model: "mock-code", ConfirmQualification: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != modelregistry.HarnessDiagnosticQualified ||
		result.ModelCalls != 0 || result.SyntheticToolCalls != 0 ||
		result.ToolExecuted || result.ResponseContentReturned ||
		!result.Harness.RootEligible {
		t.Fatalf("unexpected built-in Harness qualification: %#v", result)
	}
}
