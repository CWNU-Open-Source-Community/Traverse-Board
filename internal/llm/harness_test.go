package llm

import (
	"strings"
	"testing"
	"time"
)

func TestAnthropicHarnessRequiresExactModelQualification(t *testing.T) {
	provider, err := NewAnthropicCompatibleProvider(AnthropicCompatibleConfig{
		Name: "test", BaseURL: "https://example.invalid/anthropic",
		APIKey: "test-secret", DefaultModel: "model-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(ModelRef{Provider: "test", Model: "model-a"})
	router.RegisterProvider(provider)
	ref := ModelRef{Provider: "test", Model: "model-a"}
	request := ChatRequest{
		Tools:    []ToolSpec{{Name: "echo", Parameters: []byte(`{"type":"object"}`)}},
		JSONMode: true,
	}
	_, profile, prepareErr := router.PrepareHarnessRequest(
		ref, HarnessWorkloadRoot, request)
	if prepareErr == nil ||
		profile.QualificationStatus != HarnessQualificationRequired {
		t.Fatalf("unqualified request unexpectedly passed: profile=%#v err=%v", profile, prepareErr)
	}
	if !strings.Contains(prepareErr.Error(), "not qualified for streamed tool calling") {
		t.Fatalf("unqualified provider did not return an actionable tool diagnostic: %v", prepareErr)
	}

	base, err := router.HarnessProfile(ref)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := router.SetHarnessQualification(ref, HarnessQualification{
		ProtocolVersion:    ModelHarnessProtocolVersion,
		BindingDigest:      base.BindingDigest,
		ToolCallsQualified: true, ToolResultsQualified: true,
		StrictJSONQualified: true, StreamingQualified: true,
		QualifiedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	prepared, profile, err := router.PrepareHarnessRequest(
		ref, HarnessWorkloadRoot, request)
	if err != nil {
		t.Fatal(err)
	}
	if profile.QualificationStatus != HarnessQualificationVerified ||
		prepared.JSONMode || len(prepared.Tools) != 1 ||
		prepared.Metadata["harness_transport"] != HarnessTransportAnthropicMessages ||
		prepared.Metadata["harness_json_strategy"] != HarnessJSONStrategyPrompt {
		t.Fatalf("unexpected prepared request/profile: request=%#v profile=%#v",
			prepared, profile)
	}

	other, err := router.HarnessProfile(ModelRef{Provider: "test", Model: "model-b"})
	if err != nil {
		t.Fatal(err)
	}
	if other.QualificationStatus != HarnessQualificationRequired {
		t.Fatalf("qualification escaped its exact model binding: %#v", other)
	}
}

func TestHarnessPreparationMinimizesNoToolWorkloads(t *testing.T) {
	router := NewDefaultRouter()
	ref := ModelRef{Provider: "mock", Model: "mock-fast"}
	request := ChatRequest{
		Tools: []ToolSpec{{Name: "must_not_escape",
			Parameters: []byte(`{"type":"object"}`)}},
		JSONMode: false,
	}
	prepared, profile, err := router.PrepareHarnessRequest(
		ref, HarnessWorkloadSpecialist, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Tools) != 0 || !prepared.JSONMode ||
		profile.QualificationStatus != HarnessQualificationTrusted ||
		prepared.Metadata["harness_workload"] != string(HarnessWorkloadSpecialist) {
		t.Fatalf("no-tool workload was not minimized: request=%#v profile=%#v",
			prepared, profile)
	}
}

func TestLegacyProviderContractRemainsSourceCompatible(t *testing.T) {
	router := NewRouter(ModelRef{Provider: "capture", Model: "legacy"})
	router.RegisterProvider(&capturingProvider{name: "capture"})
	profile, err := router.HarnessProfile(ModelRef{Provider: "capture", Model: "legacy"})
	if err != nil {
		t.Fatal(err)
	}
	if profile.TransportProtocol != HarnessTransportProviderContract ||
		profile.QualificationStatus != HarnessQualificationTrusted {
		t.Fatalf("legacy Provider did not receive its compatibility profile: %#v", profile)
	}
}

func TestExpiredHarnessQualificationFailsClosed(t *testing.T) {
	provider, err := NewAnthropicCompatibleProvider(AnthropicCompatibleConfig{
		Name: "test", BaseURL: "https://example.invalid/anthropic",
		APIKey: "test-secret", DefaultModel: "model-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(ModelRef{Provider: "test", Model: "model-a"})
	router.RegisterProvider(provider)
	ref := ModelRef{Provider: "test", Model: "model-a"}
	base, err := router.HarnessProfile(ref)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	err = router.SetHarnessQualification(ref, HarnessQualification{
		ProtocolVersion:    ModelHarnessProtocolVersion,
		BindingDigest:      base.BindingDigest,
		ToolCallsQualified: true, ToolResultsQualified: true,
		StrictJSONQualified: true, StreamingQualified: true,
		QualifiedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour),
	})
	if err == nil {
		t.Fatal("expired model Harness qualification was accepted")
	}
}

var _ Provider = (*capturingProvider)(nil)
