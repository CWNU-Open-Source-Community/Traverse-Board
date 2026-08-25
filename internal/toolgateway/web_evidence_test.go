package toolgateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/policy"
)

type webEvidenceExecutorStub struct {
	calls       int
	lastScope   WebEvidenceExecutionScope
	lastTool    ToolName
	lastPayload json.RawMessage
}

func (s *webEvidenceExecutorStub) ExecuteWebEvidence(_ context.Context,
	scope WebEvidenceExecutionScope, name ToolName, payload json.RawMessage,
) (WebEvidenceExecutionResult, error) {
	s.calls++
	s.lastScope = scope
	s.lastTool = name
	s.lastPayload = append(json.RawMessage(nil), payload...)
	return WebEvidenceExecutionResult{Content: `{"protocol_version":"web_fetch.v1"}`,
		Metadata: map[string]string{"source_id": "source-web-1", "citeable": "true"}}, nil
}

func testWebEvidenceCapabilityContext() WebEvidenceCapabilityContext {
	return WebEvidenceCapabilityContext{RunID: "run-web-1", MissionID: "mission-web-1",
		SessionID: "session-web-1", RootAgentID: "agent-root", WorkspaceID: "workspace-web-1",
		Surface: domain.ExecutionSurfaceCode, Phase: domain.ExecutionPhaseDeliver,
		Role: domain.AgentRoleRoot, Profile: domain.ProfileCode,
		PermissionMode:     domain.RunExecutionPermissionConservative,
		PermissionRevision: 1, ModeRevision: 1, NetworkMode: "allowlist",
		AllowedTargets: []string{"docs.example.com"}, ProviderAvailable: true,
		ProviderFingerprint: strings.Repeat("a", 64)}
}

func TestWebEvidenceDefinitionsAndPayloadsAreClosed(t *testing.T) {
	definitions := WebEvidenceToolDefinitions()
	if len(definitions) != 3 {
		t.Fatalf("definitions=%#v", definitions)
	}
	for _, definition := range definitions {
		if definition.Class != ClassNetworkRead || definition.Approval != ApprovalAutomatic ||
			!json.Valid(definition.InputSchema) ||
			!strings.Contains(string(definition.InputSchema), `"additionalProperties":false`) {
			t.Fatalf("definition=%#v", definition)
		}
		if class, found := ClassForTool(definition.Name); !found || class != ClassNetworkRead {
			t.Fatalf("tool=%s class=%s found=%t", definition.Name, class, found)
		}
	}
	definitions[0].InputSchema[0] = '['
	fresh, found := WebEvidenceToolDefinition(WebSearchTool)
	if !found || !json.Valid(fresh.InputSchema) {
		t.Fatal("returned definition mutated the registry")
	}

	valid := map[ToolName]json.RawMessage{
		WebSearchTool:   json.RawMessage("{\"version\":\"web_search.v1\",\"query\":\" public\\n  spec \",\"limit\":3}"),
		WebFetchTool:    json.RawMessage(`{"version":"web_fetch.v1","source_id":" source-1 "}`),
		WebCitationTool: json.RawMessage(`{"version":"web_citation.v1","source_id":"source-1","snapshot_id":"snapshot-1","claim":" verified   claim ","span_start":0,"span_end":8}`),
	}
	for name, payload := range valid {
		canonical, err := NormalizeWebEvidencePayload(name, payload)
		if err != nil || !json.Valid(canonical) || strings.Contains(string(canonical), `" public`) ||
			strings.Contains(string(canonical), "  ") || strings.Contains(string(canonical), `\\n`) {
			t.Fatalf("tool=%s canonical=%s err=%v", name, canonical, err)
		}
	}
	canonicalURL, err := NormalizeWebEvidencePayload(WebFetchTool,
		json.RawMessage(`{"version":"web_fetch.v1","url":" HTTPS://DOCS.Example.com:443/a/../report#section "}`))
	if err != nil || string(canonicalURL) !=
		`{"version":"web_fetch.v1","url":"https://docs.example.com/report"}` {
		t.Fatalf("canonical fetch URL=%s err=%v", canonicalURL, err)
	}
	for _, test := range []struct {
		name    ToolName
		payload string
	}{
		{WebSearchTool, `{"version":"web_search.v1","query":"x","limit":1,"authority":true}`},
		{WebSearchTool, `{"version":"web_search.v0","query":"x","limit":1}`},
		{WebFetchTool, `{"version":"web_fetch.v1","source_id":"source-1","url":"https://docs.example.com/"}`},
		{WebFetchTool, `{"version":"web_fetch.v1"}`},
		{WebFetchTool, `{"version":"web_fetch.v1","url":"https://user:secret@docs.example.com/report"}`},
		{WebFetchTool, `{"version":"web_fetch.v1","url":"https://127.0.0.1/private"}`},
		{WebFetchTool, `{"version":"web_fetch.v1","url":"https://docs.example.com/report?access_token=secret"}`},
		{WebFetchTool, "{\"version\":\"web_fetch.v1\",\"source_id\":\"source\\u0001bad\"}"},
		{WebCitationTool, `{"version":"web_citation.v1","source_id":"source-1","snapshot_id":"snapshot-1","claim":"x","span_start":4,"span_end":4}`},
		{WebCitationTool, "{\"version\":\"web_citation.v1\",\"source_id\":\"source-1\",\"snapshot_id\":\"snapshot-1\",\"claim\":\"bad\\u0001claim\"}"},
		{WebCitationTool, `{"version":"web_citation.v1","source_id":"source-1","snapshot_id":"snapshot-1","claim":"x","span_start":4}`},
	} {
		if _, err := NormalizeWebEvidencePayload(test.name, json.RawMessage(test.payload)); err == nil {
			t.Fatalf("accepted invalid %s payload: %s", test.name, test.payload)
		}
	}
	secret := "s" + "k-" + strings.Repeat("x", 28)
	for name, payload := range map[ToolName]any{
		WebSearchTool: map[string]any{"version": "web_search.v1", "query": secret, "limit": 1},
		WebFetchTool:  map[string]any{"version": "web_fetch.v1", "source_id": secret},
		WebCitationTool: map[string]any{"version": "web_citation.v1", "source_id": "source-1",
			"snapshot_id": "snapshot-1", "claim": secret},
	} {
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := NormalizeWebEvidencePayload(name, raw); err == nil {
			t.Fatalf("accepted credential-looking %s payload", name)
		}
	}
}

func TestWebEvidenceCapabilityAndAuthorityFailClosed(t *testing.T) {
	scope := testWebEvidenceCapabilityContext()
	available := WebEvidenceCapabilitySnapshot(scope)
	if !available.Available || !available.SearchAvailable || available.Generation == "" {
		t.Fatalf("available=%#v", available)
	}

	disabledScope := scope
	disabledScope.NetworkMode = "disabled"
	disabledScope.AllowedTargets = nil
	disabled := WebEvidenceCapabilitySnapshot(disabledScope)
	if disabled.Available || disabled.SearchAvailable ||
		!strings.Contains(disabled.Refusal, "web_evidence_network_disabled") {
		t.Fatalf("disabled=%#v", disabled)
	}
	invalid := scope
	invalid.AllowedTargets = []string{"localhost"}
	if snapshot := WebEvidenceCapabilitySnapshot(invalid); snapshot.Available ||
		!strings.Contains(snapshot.Refusal, "authority is invalid") {
		t.Fatalf("invalid network=%#v", snapshot)
	}
	specialistScope := scope
	specialistScope.Role = domain.AgentRoleSpecialist
	if specialist := WebEvidenceCapabilitySnapshot(specialistScope); specialist.Available {
		t.Fatalf("specialist=%#v", specialist)
	}
	withoutProvider := scope
	withoutProvider.ProviderAvailable = false
	withoutProvider.ProviderFingerprint = ""
	if snapshot := WebEvidenceCapabilitySnapshot(withoutProvider); !snapshot.Available || snapshot.SearchAvailable {
		t.Fatalf("without provider=%#v", snapshot)
	}
	invalidProvider := scope
	invalidProvider.ProviderFingerprint = ""
	if snapshot := WebEvidenceCapabilitySnapshot(invalidProvider); snapshot.Available ||
		!strings.Contains(snapshot.Refusal, "Provider binding") {
		t.Fatalf("invalid Provider binding=%#v", snapshot)
	}
	changedTarget := scope
	changedTarget.AllowedTargets = []string{"other.example.com"}
	if WebEvidenceCapabilitySnapshot(changedTarget).Generation == available.Generation {
		t.Fatal("target drift did not rotate the capability generation")
	}
	changedProvider := scope
	changedProvider.ProviderFingerprint = strings.Repeat("b", 64)
	if WebEvidenceCapabilitySnapshot(changedProvider).Generation == available.Generation {
		t.Fatal("Provider drift did not rotate the capability generation")
	}

	authority, err := NewWebEvidenceCallAuthority(scope)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeWebEvidenceCallAuthority(authority)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeWebEvidenceCallAuthority(encoded)
	if err != nil || decoded.Generation != available.Generation ||
		decoded.PermissionRevision != scope.PermissionRevision {
		t.Fatalf("decoded=%#v err=%v", decoded, err)
	}
	authority.AllowedTargets = []string{"changed.example.com"}
	if err := authority.Validate(); err == nil {
		t.Fatal("authority survived target drift without a new generation")
	}
	if _, err := DecodeWebEvidenceCallAuthority(append(encoded, []byte(` {}`)...)); err == nil {
		t.Fatal("authority accepted trailing JSON")
	}
}

func TestWebEvidenceGatewayRequiresFencedRootAndMarksOutputUntrusted(t *testing.T) {
	tracked := newTrackedStructuredStore()
	executor := &webEvidenceExecutorStub{}
	gateway := New(tracked, policy.NewDefaultChecker()).WithWebEvidenceExecutor(executor)
	capability := testWebEvidenceCapabilityContext()
	call := ToolCall{Name: WebFetchTool,
		Payload:      json.RawMessage(`{"version":"web_fetch.v1","url":"https://docs.example.com/report"}`),
		OperationKey: "web-fetch-operation", RunID: capability.RunID,
		MissionID: capability.MissionID, SessionID: capability.SessionID,
		WorkspaceID: capability.WorkspaceID, AgentID: capability.RootAgentID,
		Surface: capability.Surface, Phase: capability.Phase, Role: capability.Role,
		Profile: capability.Profile, PermissionMode: capability.PermissionMode,
		PermissionRevision: capability.PermissionRevision, ModeRevision: capability.ModeRevision,
		CapabilityGeneration: WebEvidenceCapabilitySnapshot(capability).Generation,
		RequestedBy:          "run_supervisor", LeaseID: "lease-web-1", LeaseGeneration: 1}

	outcome, err := gateway.Invoke(t.Context(), call)
	if err != nil || executor.calls != 1 || executor.lastTool != WebFetchTool ||
		executor.lastScope.RunID != capability.RunID ||
		executor.lastScope.CapabilityGeneration != call.CapabilityGeneration ||
		string(executor.lastPayload) != string(call.Payload) || outcome.Execution == nil ||
		outcome.Execution.Backend != "web_evidence" || outcome.Result == nil ||
		outcome.Result.Status != StatusCompleted || outcome.Result.Metadata["untrusted_output"] != "true" ||
		outcome.Result.Metadata["source_id"] != "source-web-1" ||
		outcome.Call.OperationKey != "" || outcome.Call.LeaseID != "" ||
		string(outcome.Call.Payload) != `{"redacted":true}` {
		t.Fatalf("outcome=%#v executor=%#v err=%v", outcome, executor, err)
	}

	unfenced := call
	unfenced.LeaseID = ""
	unfenced.LeaseGeneration = 0
	if _, err := gateway.Invoke(t.Context(), unfenced); err == nil {
		t.Fatal("unfenced Web fetch reached the Gateway")
	}
	if executor.calls != 1 || tracked.chargeCount() != 1 {
		t.Fatalf("calls=%d charges=%d", executor.calls, tracked.chargeCount())
	}
}
