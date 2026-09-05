package application

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/mcp"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/toolgateway"
)

type sensitivePersistenceMCPClient struct {
	content string
}

func (s sensitivePersistenceMCPClient) Capabilities(context.Context, string, string) (
	mcp.ScopedCapabilities, error,
) {
	return mcp.ScopedCapabilities{}, nil
}

func (s sensitivePersistenceMCPClient) Invoke(context.Context, mcp.InvokeRequest) (
	mcp.ClientCallResult, error,
) {
	return mcp.ClientCallResult{Content: s.content}, nil
}

func TestSupervisorMCPPreparePersistsExactDurableAuthority(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	state, err := store.Open(filepath.Join(home, "supervisor-mcp-persist.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	workspace := store.WorkspaceRecord{ID: "workspace-mcp-persist", Name: "mcp-persist",
		RootPath: home}
	if err := state.SaveWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	mission, created, err := NewRunService(state).Create(ctx, CreateRunRequest{
		Goal: "persist a prepared MCP call", Profile: "code",
		Surface: string(domain.ExecutionSurfaceCode), Phase: string(domain.ExecutionPhaseDeliver),
		WorkspaceID: workspace.ID, Budget: domain.Budget{MaxTurns: 4, MaxToolCalls: 8},
	})
	if err != nil {
		t.Fatal(err)
	}
	executionCapabilities := domain.ExecutionPermissionRuntimeCapabilities{
		OperatorApprovalEnabled: true, DangerFullAccessEnabled: true}
	permissionResult, err := NewRunExecutionPermissionService(state,
		executionCapabilities).Change(ctx,
		ChangeRunExecutionPermissionRequest{RunID: created.ID,
			Mode:         string(domain.RunExecutionPermissionFullAccess),
			OperationKey: "supervisor-mcp-full-access", RequestedBy: "test_operator",
			Reason: "exercise exact durable MCP authority", ConfirmDangerFullAccess: true})
	if err != nil {
		t.Fatal(err)
	}
	run, err := NewRunService(state).Start(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	acquired, err := state.AcquireRunExecutionLease(ctx, domain.AcquireRunExecutionLeaseRequest{
		RunID: run.ID, OwnerID: "supervisor-mcp-persistence-test", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := state.BeginSupervisorTurn(ctx, acquired.Lease, "use reviewed MCP capability")
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := strings.Repeat("a", 64)
	capabilities := mcp.ScopedCapabilities{ProtocolVersion: mcp.ClientProtocolVersion,
		Generation: strings.Repeat("b", 64), Servers: []mcp.ScopedServerCapability{{
			ServerID: "docs", Name: "Documentation", CapabilityFingerprint: fingerprint,
			Tools: []mcp.RemoteTool{{Name: "lookup", Description: "Look up one document.",
				InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["query"],"properties":{"query":{"type":"string"}}}`)}},
		}}}
	authority, err := mcp.EncodeSupervisorCallAuthority(mcp.SupervisorCallAuthority{
		Version: mcp.SupervisorCallAuthorityVersion,
		RunID:   run.ID, MissionID: mission.ID, WorkspaceID: workspace.ID,
		PermissionSnapshotID: permissionResult.Permission.ID,
		PermissionRevision:   permissionResult.Permission.Revision,
		PermissionMode:       permissionResult.Permission.Mode,
	})
	if err != nil {
		t.Fatal(err)
	}
	rawCall := llm.ToolCall{ID: "provider-mcp-lookup", Name: string(toolgateway.MCPToolCallTool),
		Arguments: json.RawMessage(`{"version":"mcp-client.v1","server_id":"docs","tool_name":"lookup","capability_fingerprint":"` + fingerprint + `","arguments":{"query":"status"}}`)}
	prepared, err := prepareSupervisorToolCalls([]llm.ToolCall{rawCall}, run.ID,
		turn.Checkpoint.NextTurn, 1, domain.ExecutionSurfaceCode, domain.ExecutionPhaseDeliver,
		domain.RunExecutionPermissionFullAccess, false, false,
		supervisorToolOptions{MCP: supervisorMCPTools{
			Capabilities: capabilities, Authority: append(json.RawMessage(" \n"), authority...)}})
	if err != nil || len(prepared) != 1 || string(prepared[0].Authority) != " \n"+string(authority) {
		t.Fatalf("prepared MCP call=%#v err=%v", prepared, err)
	}
	attempt := llm.ModelAttempt{Number: 1, TransportAttempt: 1, MaxAttempts: 1,
		Provider: "test", Model: "model"}
	if inserted, err := state.RecordSupervisorModelStarted(ctx, turn.Checkpoint, attempt); err != nil || !inserted {
		t.Fatalf("record model start: inserted=%t err=%v", inserted, err)
	}
	attempt.Outcome = llm.OutcomeSuccess
	checkpoint, err := state.RecordSupervisorModelCompleted(ctx, turn.Checkpoint, attempt,
		llm.ChatResponse{Provider: "test", Model: "model",
			Usage:     llm.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2},
			ToolCalls: prepared})
	if err != nil {
		t.Fatalf("persist prepared MCP call: %v", err)
	}
	rounds, err := state.ListSupervisorToolRounds(ctx, checkpoint)
	if err != nil || len(rounds) != 1 || len(rounds[0].Calls) != 1 {
		t.Fatalf("durable MCP rounds=%#v err=%v", rounds, err)
	}
	storedAuthority := json.RawMessage(rounds[0].Calls[0].AuthorityJSON)
	decoded, err := mcp.DecodeSupervisorCallAuthority(storedAuthority)
	if err != nil || string(storedAuthority) != string(authority) ||
		decoded.RunID != run.ID || decoded.MissionID != mission.ID ||
		decoded.WorkspaceID != workspace.ID ||
		decoded.PermissionSnapshotID != permissionResult.Permission.ID {
		t.Fatalf("stored MCP authority=%s decoded=%#v err=%v", storedAuthority, decoded, err)
	}

	passwordCanary := "MCP_RESULT_PASSWORD_CANARY_SHORT_PHRASE"
	authCanary := "MCP_RESULT_AUTH_CANARY_ORDINARY_PHRASE"
	safeResult := `{"password":"` + passwordCanary + `",` +
		`"nested":{"auth_header":"` + authCanary + `"},"status":"created"}`
	supervisor := NewRunSupervisor(state, nil, policy.NewDefaultChecker()).
		WithExecutionPermissionCapabilities(executionCapabilities).
		WithMCPClient(sensitivePersistenceMCPClient{content: safeResult})
	if inserted, err := state.RecordSupervisorToolExecutionStarted(ctx, checkpoint,
		prepared[0].ID); err != nil || !inserted {
		t.Fatalf("record MCP execution start inserted=%t err=%v", inserted, err)
	}
	storedCall := rounds[0].Calls[0]
	result, err := supervisor.invokeSupervisorTool(ctx, turn, storedCall)
	if err != nil {
		t.Fatalf("invoke MCP through Supervisor and Gateway: %v", err)
	}
	if _, replayed, err := state.RecordSupervisorToolResult(ctx, checkpoint,
		result); err != nil || replayed {
		t.Fatalf("persist sanitized MCP result replayed=%t err=%v", replayed, err)
	}
	persisted, err := state.ListSupervisorToolRounds(ctx, checkpoint)
	if err != nil || len(persisted) != 1 || len(persisted[0].Calls) != 1 {
		t.Fatalf("read persisted MCP result=%#v err=%v", persisted, err)
	}
	durableResult := persisted[0].Calls[0].ResultJSON
	if strings.Contains(durableResult, passwordCanary) ||
		strings.Contains(durableResult, authCanary) ||
		!strings.Contains(durableResult, "[REDACTED:sensitive-field]") ||
		!strings.Contains(durableResult, `\"status\":\"created\"`) {
		t.Fatalf("MCP result crossed its durable boundary unsafely: %s", durableResult)
	}
}
