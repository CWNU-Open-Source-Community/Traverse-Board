package application

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/mcp"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/toolgateway"
)

type exactPermissionMCPClient struct {
	calls        int
	capabilities mcp.ScopedCapabilities
}

func (c *exactPermissionMCPClient) Capabilities(context.Context, string, string) (
	mcp.ScopedCapabilities, error,
) {
	return c.capabilities, nil
}

func TestSupervisorMCPAdvertisementFenceRejectsRevokedFullAndDebug(t *testing.T) {
	for _, permissionMode := range []domain.RunExecutionPermissionMode{
		domain.RunExecutionPermissionFullAccess,
		domain.RunExecutionPermissionDebug,
	} {
		t.Run(string(permissionMode), func(t *testing.T) {
			ctx := context.Background()
			state, runRecord, _, lease, _ := newCommandRuntimeTestRuntimeWithPermission(
				t, ctx, permissionMode)
			permission, err := state.GetRunExecutionPermission(ctx, runRecord.ID)
			if err != nil {
				t.Fatal(err)
			}
			authority := domain.NewExecutionPermissionRuntimeAuthority()
			capabilities := domain.ExecutionPermissionRuntimeCapabilities{
				OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
				DebugMaximumAccessEnabled: true, FullAccessRequiresRuntimeGrant: true,
				RuntimeAuthority: authority,
			}
			if permissionMode == domain.RunExecutionPermissionFullAccess {
				if _, err := authority.ActivateRunFullAccess(permission); err != nil {
					t.Fatal(err)
				}
			}
			fingerprint := strings.Repeat("a", 64)
			client := &exactPermissionMCPClient{capabilities: mcp.ScopedCapabilities{
				ProtocolVersion: mcp.ClientProtocolVersion, Generation: strings.Repeat("b", 64),
				Servers: []mcp.ScopedServerCapability{{ServerID: "docs", Name: "Documentation",
					CapabilityFingerprint: fingerprint, Tools: []mcp.RemoteTool{{
						Name: "lookup", InputSchema: json.RawMessage(`{"type":"object"}`),
					}}}},
			}}
			supervisor := NewRunSupervisor(state, nil, policy.NewDefaultChecker()).
				WithExecutionPermissionCapabilities(capabilities).
				WithMCPClient(client)
			turn, err := state.BeginSupervisorTurn(ctx, lease, "")
			if err != nil {
				t.Fatal(err)
			}
			advertisement, err := supervisor.supervisorMCPCapabilities(ctx, turn, permission)
			if err != nil || len(advertisement.Authority) == 0 {
				t.Fatalf("MCP advertisement=%#v err=%v", advertisement, err)
			}
			payload := json.RawMessage(`{"version":"mcp-client.v1","server_id":"docs","tool_name":"lookup","capability_fingerprint":"` + fingerprint + `","arguments":{}}`)
			prepared, err := prepareSupervisorToolCalls([]llm.ToolCall{{
				ID: "provider-mcp", Name: string(toolgateway.MCPToolCallTool), Arguments: payload,
			}}, runRecord.ID, turn.Checkpoint.NextTurn, 1, domain.ExecutionSurfaceCode,
				domain.ExecutionPhaseDeliver, permissionMode, false, false,
				supervisorToolOptions{MCP: advertisement})
			if err != nil || len(prepared) != 1 {
				t.Fatalf("prepared=%#v err=%v", prepared, err)
			}
			authority.RevokeRun(runRecord.ID)
			_, err = supervisor.invokeSupervisorTool(ctx, turn, domain.SupervisorToolCall{
				RunID: runRecord.ID, Turn: turn.Checkpoint.NextTurn,
				CallID: prepared[0].ID, ToolName: prepared[0].Name,
				PayloadJSON:   string(prepared[0].Arguments),
				AuthorityJSON: string(prepared[0].Authority),
			})
			if apperror.CodeOf(err) != apperror.CodeFailedPrecondition || client.calls != 0 {
				t.Fatalf("revoked advertised MCP call reached transport: calls=%d code=%s err=%v",
					client.calls, apperror.CodeOf(err), err)
			}
		})
	}
}

func (c *exactPermissionMCPClient) Invoke(context.Context, mcp.InvokeRequest) (
	mcp.ClientCallResult, error,
) {
	c.calls++
	return mcp.ClientCallResult{Content: `{"ok":true}`}, nil
}

func TestMCPExecutorRequiresExactLiveFullAccessAndRunFence(t *testing.T) {
	ctx := context.Background()
	state, runRecord, _, lease, _ := newCommandRuntimeTestRuntime(t, ctx)
	permission, err := state.GetRunExecutionPermission(ctx, runRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	authority := domain.NewExecutionPermissionRuntimeAuthority()
	capabilities := domain.ExecutionPermissionRuntimeCapabilities{
		OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
		FullAccessRequiresRuntimeGrant: true, RuntimeAuthority: authority,
	}
	client := &exactPermissionMCPClient{}
	executor, err := NewMCPClientToolExecutor(client, state, capabilities)
	if err != nil {
		t.Fatal(err)
	}
	scope := exactPermissionMCPScope(runRecord, lease, permission)
	payload := toolgateway.MCPToolCallPayload{Version: toolgateway.MCPClientToolProtocolVersion,
		ServerID: "docs", ToolName: "lookup",
		CapabilityFingerprint: string(make([]byte, 64)), Arguments: json.RawMessage(`{}`)}
	if _, err := executor.ExecuteMCP(ctx, scope, payload); err == nil || client.calls != 0 {
		t.Fatalf("cold persisted Full Access reached MCP transport: calls=%d err=%v",
			client.calls, err)
	}
	grant, err := authority.ActivateRunFullAccess(permission)
	if err != nil {
		t.Fatal(err)
	}
	fence, err := authority.IssueRunAuthorizationFence(runRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	scope.PermissionGeneration = grant.Generation
	scope.RunAuthorizationFence = fence
	if _, err := executor.ExecuteMCP(ctx, scope, payload); err != nil || client.calls != 1 {
		t.Fatalf("live exact Full Access did not reach MCP transport: calls=%d err=%v",
			client.calls, err)
	}
	authority.RevokeRun(runRecord.ID)
	if _, err := executor.ExecuteMCP(ctx, scope, payload); err == nil || client.calls != 1 {
		t.Fatalf("revoked Full Access authority reached MCP transport: calls=%d err=%v",
			client.calls, err)
	}
}

func TestMCPExecutorDebugUsesRunFence(t *testing.T) {
	ctx := context.Background()
	state, runRecord, _, lease, _ := newCommandRuntimeTestRuntimeWithPermission(t,
		ctx, domain.RunExecutionPermissionDebug)
	permission, err := state.GetRunExecutionPermission(ctx, runRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	authority := domain.NewExecutionPermissionRuntimeAuthority()
	capabilities := domain.ExecutionPermissionRuntimeCapabilities{
		OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
		DebugMaximumAccessEnabled: true, FullAccessRequiresRuntimeGrant: true,
		RuntimeAuthority: authority,
	}
	client := &exactPermissionMCPClient{}
	executor, err := NewMCPClientToolExecutor(client, state, capabilities)
	if err != nil {
		t.Fatal(err)
	}
	fence, err := authority.IssueRunAuthorizationFence(runRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	scope := exactPermissionMCPScope(runRecord, lease, permission)
	scope.RunAuthorizationFence = fence
	payload := toolgateway.MCPToolCallPayload{Version: toolgateway.MCPClientToolProtocolVersion,
		ServerID: "docs", ToolName: "lookup",
		CapabilityFingerprint: string(make([]byte, 64)), Arguments: json.RawMessage(`{}`)}
	if _, err := executor.ExecuteMCP(ctx, scope, payload); err != nil || client.calls != 1 {
		t.Fatalf("Debug did not inherit fenced MCP execution: calls=%d err=%v",
			client.calls, err)
	}
	authority.RevokeRun(runRecord.ID)
	if _, err := executor.ExecuteMCP(ctx, scope, payload); err == nil || client.calls != 1 {
		t.Fatalf("revoked Debug fence reached MCP transport: calls=%d err=%v",
			client.calls, err)
	}
}

func exactPermissionMCPScope(runRecord domain.Run, lease domain.RunExecutionLease,
	permission domain.RunExecutionPermissionSnapshot,
) toolgateway.MCPExecutionScope {
	return toolgateway.MCPExecutionScope{
		InvocationID: "mcp-exact-permission-invocation", RunID: runRecord.ID,
		MissionID: runRecord.MissionID, WorkspaceID: "workspace-command-runtime-app",
		Surface: domain.ExecutionSurfaceCode, Phase: domain.ExecutionPhaseDeliver,
		Role: domain.AgentRoleRoot, PermissionMode: permission.Mode,
		PermissionSnapshotID: permission.ID, PermissionRevision: permission.Revision,
		LeaseID: lease.LeaseID, LeaseGeneration: lease.Generation,
		RequestedBy: "run_supervisor", PolicyDecision: toolgateway.Decision{
			Allowed: true, Approval: toolgateway.ApprovalAutomatic,
			Risk: "high", Reason: "test",
		},
	}
}
