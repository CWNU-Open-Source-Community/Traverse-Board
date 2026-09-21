package modelregistry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"cyberagent-workbench/internal/llm"
)

func TestResponsesBillingFailureStopsDiagnosticAndQualification(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"error":{"type":"server_error","code":"rate_limit_exceeded"}}`))
	}))
	defer server.Close()
	definition := validCustomDefinition(server.URL + "/v1/responses")
	definition.Transport = ProviderTransportOpenAIResponses
	settings := routeSettings{ProviderDefinitionsSettingKey: providerDefinitionSetting(t, definition, 1)}
	registry, err := newRegistry(func(string) (string, bool) { return "", false }, func(context.Context, string) (string, bool, error) { return "billing-test-credential", true, nil })
	if err != nil {
		t.Fatal(err)
	}
	if err = registry.LoadRouteSettings(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	diagnostic, err := registry.DiagnoseAndRecord(t.Context(), settings, definition.ID, definition.DefaultModel)
	if err != nil || diagnostic.Status != DiagnosticUnreachable || diagnostic.Outcome != string(llm.OutcomePermanent) || diagnostic.FailureReason != llm.ProviderFailureCapacity || diagnostic.Retryable || requests.Load() != 1 {
		t.Fatalf("diagnostic=%+v err=%v requests=%d", diagnostic, err, requests.Load())
	}
	qualified, err := registry.QualifyHarness(t.Context(), settings, definition.ID, definition.DefaultModel)
	if err != nil || qualified.Status != HarnessDiagnosticUnreachable || qualified.Outcome != string(llm.OutcomePermanent) || qualified.FailureReason != llm.ProviderFailureCapacity || qualified.Retryable || qualified.ModelCalls != 1 || qualified.SyntheticToolCalls != 0 || qualified.ToolExecuted || qualified.Harness.RootEligible || qualified.QualificationStatus != QualificationStatusCapacity || requests.Load() != 2 {
		t.Fatalf("qualification=%+v err=%v requests=%d", qualified, err, requests.Load())
	}
	if err = registry.LoadRouteSettings(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	saved, found := providerByName(registry.Snapshot(), definition.ID)
	if !found || len(saved.Harnesses) == 0 || saved.Harnesses[0].LatestQualificationStatus != QualificationStatusCapacity || saved.Harnesses[0].RootEligible {
		t.Fatalf("billing status was not retained: %+v", saved)
	}
}
