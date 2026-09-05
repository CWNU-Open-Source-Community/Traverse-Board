package toolgateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/policy"
)

type browserActionExecutorStub struct {
	calls       int
	lastScope   BrowserActionExecutionScope
	lastTool    ToolName
	lastPayload json.RawMessage
}

func (s *browserActionExecutorStub) ExecuteBrowserAction(_ context.Context,
	scope BrowserActionExecutionScope, name ToolName, payload json.RawMessage,
) (BrowserActionExecutionResult, error) {
	s.calls++
	s.lastScope = scope
	s.lastTool = name
	s.lastPayload = append(json.RawMessage(nil), payload...)
	return BrowserActionExecutionResult{Content: `{"protocol_version":"browser_status_result.v1","page":"AKIAABCDEFGHIJKLMNOP"}`,
		Metadata: map[string]string{"artifact_locator": "workspace:///.cyberagent-workbench/browser-artifacts/run-browser-1/image.png"}}, nil
}

func testBrowserActionCapabilityContext() BrowserActionCapabilityContext {
	return BrowserActionCapabilityContext{RunID: "run-browser-1",
		MissionID: "mission-browser-1", SessionID: "session-browser-1",
		RootAgentID: "agent-root", WorkspaceID: "workspace-browser-1",
		Surface: domain.ExecutionSurfaceCode, Phase: domain.ExecutionPhaseDeliver,
		Role: domain.AgentRoleRoot, Profile: domain.ProfileCode,
		PermissionMode: domain.RunExecutionPermissionFullAccess, ModeRevision: 3,
		PermissionSnapshotID: "permission-browser-1", PermissionRevision: 4,
		PermissionActivation: 5, RunAuthorizationFence: 6,
		FullCDPSessionID:            "full-cdp-browser-1",
		BrowserPermissionSnapshotID: "browser-permission-1",
		BrowserPermissionRevision:   7, TargetOrigin: "http://127.0.0.1:18080",
		Ready: true, RuntimeAvailable: true}
}

func TestBrowserActionDefinitionsAndPayloadsAreClosed(t *testing.T) {
	definitions := BrowserActionToolDefinitions()
	if len(definitions) != 6 {
		t.Fatalf("definitions=%#v", definitions)
	}
	for _, definition := range definitions {
		if definition.Class != ClassProcess || definition.Approval != ApprovalAutomatic ||
			!json.Valid(definition.InputSchema) ||
			!strings.Contains(string(definition.InputSchema), `"additionalProperties":false`) {
			t.Fatalf("definition=%#v", definition)
		}
		if class, found := ClassForTool(definition.Name); !found || class != ClassProcess {
			t.Fatalf("tool=%s class=%s found=%t", definition.Name, class, found)
		}
	}
	definitions[0].InputSchema[0] = '['
	if fresh, found := BrowserActionToolDefinition(BrowserStatusTool); !found ||
		!json.Valid(fresh.InputSchema) {
		t.Fatal("returned definition mutated the registry")
	}

	valid := map[ToolName]string{
		BrowserStatusTool:     `{"version":"browser_status.v1"}`,
		BrowserNavigateTool:   `{"version":"browser_navigate.v1","url":"http://127.0.0.1:18080/report"}`,
		BrowserSnapshotTool:   `{"version":"browser_snapshot.v1"}`,
		BrowserClickTool:      `{"version":"browser_click.v1","selector":"body:nth-of-type(1) > button:nth-of-type(2)"}`,
		BrowserTypeTool:       `{"version":"browser_type.v1","selector":"#search","value":"bounded input"}`,
		BrowserScreenshotTool: `{"version":"browser_screenshot.v1"}`,
	}
	for name, payload := range valid {
		canonical, err := NormalizeBrowserActionPayload(name, json.RawMessage(payload))
		if err != nil || !json.Valid(canonical) {
			t.Fatalf("tool=%s canonical=%s err=%v", name, canonical, err)
		}
	}

	secret := "s" + "k-" + strings.Repeat("x", 28)
	for _, test := range []struct {
		name    ToolName
		payload string
	}{
		{BrowserNavigateTool, `{"version":"browser_navigate.v1","url":"http://localhost:18080/"}`},
		{BrowserNavigateTool, `{"version":"browser_navigate.v1","url":"https://8.8.8.8/"}`},
		{BrowserNavigateTool, `{"version":"browser_navigate.v1","url":"http://127.0.0.1:18080/#fragment"}`},
		{BrowserNavigateTool, `{"version":"browser_navigate.v1","url":"http://user:secret@127.0.0.1:18080/"}`},
		{BrowserClickTool, `{"version":"browser_click.v1","selector":"button:not([disabled])"}`},
		{BrowserClickTool, `{"version":"browser_click.v1","selector":"div:nth-of-type(1)"}`},
		{BrowserTypeTool, `{"version":"browser_type.v1","selector":"#search","value":"` + secret + `"}`},
		{BrowserStatusTool, `{"version":"browser_status.v1","extra":true}`},
	} {
		if _, err := NormalizeBrowserActionPayload(test.name,
			json.RawMessage(test.payload)); err == nil {
			t.Fatalf("accepted invalid %s payload: %s", test.name, test.payload)
		}
	}
}

func TestBrowserActionCapabilityAndAuthorityFailClosed(t *testing.T) {
	scope := testBrowserActionCapabilityContext()
	available := BrowserActionCapabilitySnapshot(scope)
	if !available.Available || available.Generation == "" ||
		available.FullCDPSessionID != scope.FullCDPSessionID {
		t.Fatalf("available=%#v", available)
	}

	for name, mutate := range map[string]func(*BrowserActionCapabilityContext){
		"workspace permission": func(value *BrowserActionCapabilityContext) {
			value.PermissionMode = domain.RunExecutionPermissionWorkspaceAccess
		},
		"not ready": func(value *BrowserActionCapabilityContext) { value.Ready = false },
		"specialist": func(value *BrowserActionCapabilityContext) {
			value.Role = domain.AgentRoleSpecialist
		},
		"no fence": func(value *BrowserActionCapabilityContext) { value.RunAuthorizationFence = 0 },
		"hostname origin": func(value *BrowserActionCapabilityContext) {
			value.TargetOrigin = "http://localhost:18080"
		},
	} {
		changed := scope
		mutate(&changed)
		if snapshot := BrowserActionCapabilitySnapshot(changed); snapshot.Available {
			t.Fatalf("%s unexpectedly available: %#v", name, snapshot)
		}
	}
	changedFence := scope
	changedFence.RunAuthorizationFence++
	if BrowserActionCapabilitySnapshot(changedFence).Generation == available.Generation {
		t.Fatal("runtime fence drift did not rotate the capability generation")
	}
	changedSession := scope
	changedSession.FullCDPSessionID = "full-cdp-browser-2"
	if BrowserActionCapabilitySnapshot(changedSession).Generation == available.Generation {
		t.Fatal("session drift did not rotate the capability generation")
	}

	authority, err := NewBrowserActionCallAuthority(scope)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeBrowserActionCallAuthority(authority)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeBrowserActionCallAuthority(encoded)
	if err != nil || decoded.Generation != available.Generation ||
		decoded.RunAuthorizationFence != scope.RunAuthorizationFence {
		t.Fatalf("decoded=%#v err=%v", decoded, err)
	}
	authority.BrowserPermissionRevision++
	if err := authority.Validate(); err == nil {
		t.Fatal("authority survived browser permission drift without a new generation")
	}
	if _, err := DecodeBrowserActionCallAuthority(append(encoded, []byte(` {}`)...)); err == nil {
		t.Fatal("authority accepted trailing JSON")
	}
}

func TestBrowserActionGatewayRequiresExactFencedScopeAndMarksOutputUntrusted(t *testing.T) {
	executor := &browserActionExecutorStub{}
	gateway := New(newTrackedStructuredStore(), policy.NewDefaultChecker()).
		WithBrowserActionExecutor(executor)
	capability := testBrowserActionCapabilityContext()
	call := ToolCall{Name: BrowserStatusTool,
		Payload:      json.RawMessage(`{"version":"browser_status.v1"}`),
		OperationKey: "browser-status-operation", RunID: capability.RunID,
		MissionID: capability.MissionID, SessionID: capability.SessionID,
		WorkspaceID: capability.WorkspaceID, AgentID: capability.RootAgentID,
		Surface: capability.Surface, Phase: capability.Phase, Role: capability.Role,
		Profile: capability.Profile, PermissionMode: capability.PermissionMode,
		ModeRevision:                capability.ModeRevision,
		PermissionSnapshotID:        capability.PermissionSnapshotID,
		PermissionRevision:          capability.PermissionRevision,
		PermissionGeneration:        capability.PermissionActivation,
		RunAuthorizationFence:       capability.RunAuthorizationFence,
		CapabilityGeneration:        BrowserActionCapabilitySnapshot(capability).Generation,
		BrowserActionSessionID:      capability.FullCDPSessionID,
		BrowserPermissionSnapshotID: capability.BrowserPermissionSnapshotID,
		BrowserPermissionRevision:   capability.BrowserPermissionRevision,
		RequestedBy:                 "run_supervisor", LeaseID: "lease-browser-1", LeaseGeneration: 1}

	outcome, err := gateway.Invoke(t.Context(), call)
	if err != nil || executor.calls != 1 || executor.lastTool != BrowserStatusTool ||
		executor.lastScope.FullCDPSessionID != capability.FullCDPSessionID ||
		executor.lastScope.RunAuthorizationFence != capability.RunAuthorizationFence ||
		outcome.Execution == nil || outcome.Execution.Backend != "full_cdp_browser" ||
		outcome.Result == nil || outcome.Result.Metadata["untrusted_output"] != "true" ||
		outcome.Result.Metadata["artifact_locator"] == "" ||
		strings.Contains(outcome.Result.Stdout, "AKIAABCDEFGHIJKLMNOP") ||
		outcome.Call.OperationKey != "" || outcome.Call.PermissionSnapshotID != "" ||
		outcome.Call.PermissionGeneration != 0 || outcome.Call.RunAuthorizationFence != 0 ||
		outcome.Call.BrowserActionSessionID != "" ||
		string(outcome.Call.Payload) != `{"redacted":true}` {
		t.Fatalf("outcome=%#v executor=%#v err=%v", outcome, executor, err)
	}

	unfenced := call
	unfenced.RunAuthorizationFence = 0
	if _, err := gateway.Invoke(t.Context(), unfenced); err == nil {
		t.Fatal("browser action without a runtime fence reached the executor")
	}
	if executor.calls != 1 {
		t.Fatalf("executor calls=%d", executor.calls)
	}
}
