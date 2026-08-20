package toolgateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/hooks"
	"cyberagent-workbench/internal/policy"
)

type mcpExecutorStub struct {
	calls int
	scope MCPExecutionScope
	input MCPToolCallPayload
}

func (s *mcpExecutorStub) ExecuteMCP(_ context.Context, scope MCPExecutionScope,
	payload MCPToolCallPayload,
) (MCPExecutionResult, error) {
	s.calls++
	s.scope = scope
	s.input = payload
	return MCPExecutionResult{Content: `{"text":"AKIAABCDEFGHIJKLMNOP"}`,
		Metadata: map[string]string{"remote": "untrusted"}}, nil
}

func TestMCPGatewayRequiresExactFencedScopeAndRedactsOutput(t *testing.T) {
	executor := &mcpExecutorStub{}
	fingerprint := strings.Repeat("a", 64)
	payload := json.RawMessage(`{"version":"mcp-client.v1","server_id":"docs","tool_name":"lookup","capability_fingerprint":"` + fingerprint + `","arguments":{"query":"bounded"}}`)
	gateway := New(nil, policy.NewDefaultChecker()).WithMCPExecutor(executor)
	call := ToolCall{Name: MCPToolCallTool, Payload: payload, RunID: "run-1",
		AgentID: "agent-1", SessionID: "session-1", WorkspaceID: "workspace-1", Surface: domain.ExecutionSurfaceCode,
		OperationKey: strings.Repeat("d", 64),
		Phase:        domain.ExecutionPhaseDeliver, Role: domain.AgentRoleRoot,
		PermissionMode: domain.RunExecutionPermissionFullAccess,
		LeaseID:        "lease-1", LeaseGeneration: 1, RequestedBy: "run_supervisor"}
	outcome, err := gateway.Invoke(t.Context(), call)
	if err != nil {
		t.Fatal(err)
	}
	if executor.calls != 1 || executor.scope.RunID != call.RunID ||
		executor.input.CapabilityFingerprint != fingerprint || outcome.Result == nil ||
		strings.Contains(outcome.Result.Stdout, "AKIAABCDEFGHIJKLMNOP") ||
		!strings.Contains(outcome.Result.Stdout, "[REDACTED:aws-access-key]") ||
		outcome.Result.Metadata["untrusted_output"] != "true" {
		t.Fatalf("MCP gateway did not fence or sanitize the call: %#v %#v", executor, outcome)
	}
	call.PermissionMode = domain.RunExecutionPermissionApproval
	if _, err := gateway.Invoke(t.Context(), call); err == nil {
		t.Fatal("MCP gateway accepted a call outside full-access permission")
	}
}

func TestMCPGatewayRestrictedHookCanDenyBeforeTransport(t *testing.T) {
	executor := &mcpExecutorStub{}
	engine := hooks.NewEngine(nil)
	if err := engine.Replace([]hooks.Registration{{PluginID: "guard",
		PluginFingerprint: strings.Repeat("b", 64), Declaration: hooks.Declaration{
			ProtocolVersion: hooks.ProtocolVersion, ID: "deny-mcp", Event: hooks.PreTool,
			Action: hooks.ActionDeny, FailurePolicy: hooks.FailureDeny, TimeoutMillis: 100,
			ToolNames: []string{string(MCPToolCallTool)}, Message: "MCP disabled by policy",
		}}}); err != nil {
		t.Fatal(err)
	}
	payload := json.RawMessage(`{"version":"mcp-client.v1","server_id":"docs","tool_name":"lookup","capability_fingerprint":"` + strings.Repeat("a", 64) + `","arguments":{}}`)
	outcome, err := New(nil, policy.NewDefaultChecker()).WithMCPExecutor(executor).
		WithLifecycleHooks(engine).Invoke(t.Context(), ToolCall{Name: MCPToolCallTool,
		Payload: payload, RunID: "run-1", AgentID: "agent-1", SessionID: "session-1", WorkspaceID: "workspace-1",
		OperationKey: strings.Repeat("d", 64),
		Surface:      domain.ExecutionSurfaceCode, Phase: domain.ExecutionPhaseDeliver,
		Role: domain.AgentRoleRoot, PermissionMode: domain.RunExecutionPermissionFullAccess,
		LeaseID: "lease-1", LeaseGeneration: 1, RequestedBy: "run_supervisor"})
	if err != nil || outcome.Decision.Allowed || executor.calls != 0 ||
		strings.Contains(outcome.Decision.Reason, "MCP disabled by policy") {
		t.Fatalf("restricted pre-tool hook did not deny before MCP transport: %#v err=%v", outcome, err)
	}
}

func TestMCPPayloadRejectsSecretMaterial(t *testing.T) {
	payload := json.RawMessage(`{"version":"mcp-client.v1","server_id":"docs","tool_name":"lookup","capability_fingerprint":"` + strings.Repeat("a", 64) + `","arguments":{"api_key":"sk-abcdefghijklmnopqrstuvwxyz"}}`)
	if _, _, err := NormalizeMCPToolPayload(payload); err == nil {
		t.Fatal("secret-like material was accepted in MCP arguments")
	}
}
