package application

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/toolgateway"
)

// Existing MCP acceptance fixtures use the real v2 writer. Legacy permission
// data remains covered by reader/migration tests, never by a hidden old writer.
func newMCPApprovalModeRuntime(t *testing.T, ctx context.Context, modes ...domain.RunExecutionPermissionMode) (*store.SQLiteStore, domain.Run, domain.AgentNode, domain.RunExecutionLease, domain.ExecutionPermissionRuntimeCapabilities) {
	t.Helper()
	mode := domain.RunExecutionPermissionFull
	if len(modes) > 0 {
		mode = modes[0]
	}
	state, err := store.Open(filepath.Join(t.TempDir(), "mcp-runtime.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	workspace := store.WorkspaceRecord{ID: "workspace-command-runtime-app", Name: "MCP runtime", RootPath: t.TempDir()}
	if err = state.SaveWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	runs := NewRunService(state)
	_, run, err := runs.Create(ctx, CreateRunRequest{Goal: "exercise owned MCP runtime", Profile: "code", WorkspaceID: workspace.ID, Budget: domain.Budget{MaxTurns: 4, MaxTokens: 1000, MaxToolCalls: 8}})
	if err != nil {
		t.Fatal(err)
	}
	capabilities := domain.ExecutionPermissionRuntimeCapabilities{OperatorApprovalEnabled: true, DangerFullAccessEnabled: true, FullAccessRequiresRuntimeGrant: true, RuntimeAuthority: domain.NewExecutionPermissionRuntimeAuthority()}
	if mode != domain.RunExecutionPermissionAsk {
		if _, err = NewRunExecutionPermissionService(state, capabilities).Change(ctx, ChangeRunExecutionPermissionRequest{RunID: run.ID, Mode: string(mode), ConfirmFull: mode == domain.RunExecutionPermissionFull, OperationKey: "mcp-runtime-mode-0001", RequestedBy: "operator"}); err != nil {
			t.Fatal(err)
		}
	}
	run, err = runs.Start(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	root, found, err := state.GetRootAgent(ctx, run.ID)
	if err != nil || !found {
		t.Fatal("root missing", err)
	}
	lease, err := state.AcquireRunExecutionLease(ctx, domain.AcquireRunExecutionLeaseRequest{RunID: run.ID, OwnerID: "mcp-runtime-worker", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	return state, run, root, lease.Lease, capabilities
}

func bindMCPScopeRuntime(t *testing.T, scope *toolgateway.MCPExecutionScope, permission domain.RunExecutionPermissionSnapshot, capabilities domain.ExecutionPermissionRuntimeCapabilities) {
	t.Helper()
	generation, live := capabilities.FullAccessGeneration(permission)
	if !live {
		t.Fatal("fixture lacks actual current-process activation")
	}
	fence, err := capabilities.RuntimeAuthority.IssueRunAuthorizationFence(scope.RunID)
	if err != nil {
		t.Fatal(err)
	}
	scope.PermissionGeneration, scope.PermissionRuntimeEpoch, scope.RunAuthorizationFence = generation, capabilities.RuntimeAuthority.RuntimeEpoch(), fence
}
