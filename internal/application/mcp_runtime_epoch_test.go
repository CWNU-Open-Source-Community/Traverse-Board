package application

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/mcp"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/toolgateway"
)

type epochMCPClient struct {
	calls        int
	onInvoke     func()
	capabilities mcp.ScopedCapabilities
}

func (c *epochMCPClient) Capabilities(context.Context, string, string) (mcp.ScopedCapabilities, error) {
	return c.capabilities, nil
}

func (c *epochMCPClient) Invoke(context.Context, mcp.InvokeRequest) (mcp.ClientCallResult, error) {
	c.calls++
	if c.onInvoke != nil {
		c.onInvoke()
	}
	return mcp.ClientCallResult{Content: "remote result"}, nil
}

func epochMCPPayload() toolgateway.MCPToolCallPayload {
	return toolgateway.MCPToolCallPayload{Version: toolgateway.MCPClientToolProtocolVersion,
		ServerID: "docs", ToolName: "lookup", CapabilityFingerprint: strings.Repeat("a", 64),
		Arguments: json.RawMessage(`{}`)}
}

func TestMCPRuntimeEpochRejectsRestartWithCollidingFence(t *testing.T) {
	for _, scenario := range []struct {
		name string
		mode domain.RunExecutionPermissionMode
	}{
		{"dynamic_full", domain.RunExecutionPermissionFull},
		{"ask", domain.RunExecutionPermissionAsk},
		{"auto", domain.RunExecutionPermissionAuto},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			ctx := context.Background()
			state, run, _, lease, _ := newMCPApprovalModeRuntime(t, ctx, scenario.mode)
			permission, err := state.GetRunExecutionPermission(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			oldAuthority := domain.NewExecutionPermissionRuntimeAuthority()
			newAuthority := domain.NewExecutionPermissionRuntimeAuthority()
			if oldAuthority.RuntimeEpoch() == "" || newAuthority.RuntimeEpoch() == "" || oldAuthority.RuntimeEpoch() == newAuthority.RuntimeEpoch() {
				t.Fatal("test requires distinct nonempty runtime epochs")
			}
			generation := uint64(0)
			if scenario.mode == domain.RunExecutionPermissionFull {
				oldGrant, err := oldAuthority.ActivateRunFullAccess(permission)
				if err != nil {
					t.Fatal(err)
				}
				newGrant, err := newAuthority.ActivateRunFullAccess(permission)
				if err != nil {
					t.Fatal(err)
				}
				if oldGrant.Generation != newGrant.Generation {
					t.Fatal("test requires colliding grant generations")
				}
				generation = newGrant.Generation
			}
			oldFence, err := oldAuthority.IssueRunAuthorizationFence(run.ID)
			if err != nil {
				t.Fatal(err)
			}
			newFence, err := newAuthority.IssueRunAuthorizationFence(run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if oldFence != newFence || !newAuthority.AllowsRunAuthorizationFence(run.ID, oldFence) {
				t.Fatal("test must reproduce numeric fence collision after restart")
			}
			capabilities := domain.ExecutionPermissionRuntimeCapabilities{
				OperatorApprovalEnabled: true, DangerFullAccessEnabled: true, RuntimeAuthority: newAuthority,
			}
			client := &epochMCPClient{}
			executor, err := NewMCPClientToolExecutor(client, state, capabilities)
			if err != nil {
				t.Fatal(err)
			}
			scope := exactPermissionMCPScope(run, lease, permission)
			scope.PermissionGeneration = generation
			scope.RunAuthorizationFence = oldFence
			for _, epoch := range []string{oldAuthority.RuntimeEpoch(), "", " " + newAuthority.RuntimeEpoch(), newAuthority.RuntimeEpoch() + " "} {
				scope.PermissionRuntimeEpoch = epoch
				result, err := executor.ExecuteMCP(ctx, scope, epochMCPPayload())
				if apperror.CodeOf(err) != apperror.CodeConflict || client.calls != 0 || result.Content != "" {
					t.Fatalf("stale epoch reached transport: calls=%d result=%#v err=%v", client.calls, result, err)
				}
			}
			scope.PermissionRuntimeEpoch = newAuthority.RuntimeEpoch()
			result, err := executor.ExecuteMCP(ctx, scope, epochMCPPayload())
			if err != nil || client.calls != 1 || result.Content != "remote result" {
				t.Fatalf("current exact epoch failed: calls=%d result=%#v err=%v", client.calls, result, err)
			}
		})
	}
}

func TestMCPRuntimeEpochLegacyHostCannotActivateNewFull(t *testing.T) {
	ctx := context.Background()
	state, run, _, lease, _ := newMCPApprovalModeRuntime(t, ctx)
	permission, err := state.GetRunExecutionPermission(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	client := &epochMCPClient{}
	executor, err := NewMCPClientToolExecutor(client, state, domain.ExecutionPermissionRuntimeCapabilities{
		OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	scope := exactPermissionMCPScope(run, lease, permission)
	for _, fenced := range []struct {
		epoch string
		fence uint64
	}{{"other-process", 0}, {"", 1}, {"other-process", 1}} {
		scope.PermissionRuntimeEpoch, scope.RunAuthorizationFence = fenced.epoch, fenced.fence
		if _, err := executor.ExecuteMCP(ctx, scope, epochMCPPayload()); err == nil || client.calls != 0 {
			t.Fatalf("legacy host accepted runtime-bound call: calls=%d err=%v", client.calls, err)
		}
	}
	scope.PermissionRuntimeEpoch, scope.RunAuthorizationFence = "", 0
	if _, err := executor.ExecuteMCP(ctx, scope, epochMCPPayload()); err == nil || client.calls != 0 {
		t.Fatalf("unfenced legacy host activated new Full: calls=%d err=%v", client.calls, err)
	}
}

func TestMCPRuntimeEpochRechecksRevocationAfterDispatchWithoutRetry(t *testing.T) {
	ctx := context.Background()
	state, run, _, lease, _ := newMCPApprovalModeRuntime(t, ctx)
	permission, err := state.GetRunExecutionPermission(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	authority := domain.NewExecutionPermissionRuntimeAuthority()
	grant, err := authority.ActivateRunFullAccess(permission)
	if err != nil {
		t.Fatal(err)
	}
	fence, err := authority.IssueRunAuthorizationFence(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	client := &epochMCPClient{onInvoke: func() { authority.RevokeRun(run.ID) }}
	executor, err := NewMCPClientToolExecutor(client, state, domain.ExecutionPermissionRuntimeCapabilities{
		OperatorApprovalEnabled: true, DangerFullAccessEnabled: true, RuntimeAuthority: authority,
	})
	if err != nil {
		t.Fatal(err)
	}
	scope := exactPermissionMCPScope(run, lease, permission)
	scope.PermissionGeneration = grant.Generation
	scope.PermissionRuntimeEpoch, scope.RunAuthorizationFence = authority.RuntimeEpoch(), fence
	for attempt := 0; attempt < 2; attempt++ {
		result, err := executor.ExecuteMCP(ctx, scope, epochMCPPayload())
		if apperror.CodeOf(err) != apperror.CodeConflict || result.Content != "" || client.calls != 1 {
			t.Fatalf("revoked result was accepted or dispatch repeated: calls=%d result=%#v err=%v", client.calls, result, err)
		}
	}
}

func TestMCPRuntimeEpochRevocationPersistsFailedReceiptOnResume(t *testing.T) {
	ctx := t.Context()
	f := newMCPOperationApprovalFixture(t, domain.RunExecutionPermissionFull, false, false)
	state, turn, capabilities := f.st, f.turn, f.capabilities
	authority := capabilities.RuntimeAuthority
	decoded, err := mcp.DecodeSupervisorCallAuthority(json.RawMessage(f.call.AuthorityJSON))
	if err != nil || decoded.PermissionRuntimeEpoch != authority.RuntimeEpoch() {
		t.Fatal("missing durable runtime epoch", err)
	}
	client := &epochMCPClient{onInvoke: func() { authority.RevokeRun(f.call.RunID) }}
	supervisor := NewAgentRunner(state, nil, policy.NewDefaultChecker()).WithExecutionPermissionCapabilities(capabilities).WithMCPClient(client)
	rounds, err := state.ListSupervisorToolRounds(ctx, turn.Checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	for resume := 0; resume < 2; resume++ {
		var waiting bool
		rounds, waiting, err = supervisor.resumeSupervisorTools(ctx, turn, rounds)
		if err != nil || waiting || len(rounds) != 1 || len(rounds[0].Calls) != 1 || client.calls != 1 {
			t.Fatalf("resume=%d calls=%d rounds=%#v waiting=%v err=%v", resume, client.calls, rounds, waiting, err)
		}
		call := rounds[0].Calls[0]
		if call.Status != domain.SupervisorToolFailed || call.ErrorCode != string(apperror.CodeConflict) ||
			!strings.Contains(call.ResultJSON, "was dispatched") || strings.Contains(call.ResultJSON, "remote result") {
			t.Fatalf("dispatch evidence or failed receipt was lost: %#v", call)
		}
	}
}
