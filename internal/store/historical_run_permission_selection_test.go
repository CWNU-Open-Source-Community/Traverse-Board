package store

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/runmutation"
)

// These two migration fixtures need a complete historical selection, including
// its operation/event and atomic lease/CDP effects. Keep the retired public
// five-mode selector closed: use the unchanged Store transaction only under the
// exact old prefixes covered by v126 and v143.
type historicalRunPermissionSelection struct {
	Permission domain.RunExecutionPermissionSnapshot
	operation  domain.RunExecutionPermissionOperation
	event      events.Event
}

func selectHistoricalRunPermission(ctx context.Context, state *SQLiteStore,
	runID string, mode domain.RunExecutionPermissionMode, operationKey, reason string,
) (historicalRunPermissionSelection, error) {
	var selection historicalRunPermissionSelection
	version, err := state.SchemaVersion(ctx)
	if err != nil {
		return selection, err
	}
	if version != 125 && version != 126 && version != 142 && version != 143 {
		return selection, fmt.Errorf("historical permission selection rejects schema %d", version)
	}
	switch mode {
	case domain.RunExecutionPermissionConservative, domain.RunExecutionPermissionApproval,
		domain.RunExecutionPermissionWorkspaceAccess, domain.RunExecutionPermissionFullAccess,
		domain.RunExecutionPermissionDebug:
	default:
		return selection, fmt.Errorf("historical permission selection rejects mode %s", mode)
	}
	current, err := state.GetRunExecutionPermission(ctx, runID)
	if err != nil {
		return selection, err
	}
	now := time.Now().UTC()
	if now.Before(current.CreatedAt) {
		now = current.CreatedAt
	}
	next, err := current.Next(idgen.New("run-exec-permission"), mode,
		mode != domain.RunExecutionPermissionConservative, "test_operator", reason, now)
	if err != nil {
		return selection, err
	}
	matrix, err := next.CapabilityMatrix()
	if err != nil {
		return selection, err
	}
	operation := domain.RunExecutionPermissionOperation{
		KeyDigest:          runmutation.Fingerprint("run_execution_permission_operation.v1", runID, operationKey),
		RequestFingerprint: runExecutionPermissionRequestFingerprint(next),
		SnapshotID:         next.ID, RunID: runID, RequestedBy: next.RequestedBy, CreatedAt: now,
	}
	event, err := events.New(runID, next.MissionID, events.RunExecutionPermissionSelectedEvent,
		"run_execution_permission", next.ID, map[string]any{
			"protocol": next.ProtocolVersion, "revision": next.Revision,
			"from": current.Mode, "to": next.Mode,
			"approval_policy": next.ApprovalPolicy, "command_scope": next.CommandScope,
			"filesystem_scope": next.FilesystemScope, "network_scope": next.NetworkScope,
			"persistent_terminal": next.PersistentTerminal, "background_process": next.BackgroundProcess,
			"agent_terminal_input": next.AgentTerminalInput, "risk_tier": next.RiskTier,
			"required_gate": next.RequiredGate, "policy_version": next.PolicyVersion,
			"requested_by": next.RequestedBy, "reason": next.Reason,
			"process_enabled": false, "execution_authorized": false, "capability_grant": false,
			"capability_matrix": map[string]any{
				"workspace_read": matrix.WorkspaceRead, "workspace_write": matrix.WorkspaceWrite,
				"sandboxed_command_runtime": matrix.SandboxedCommandRuntime,
				"unsandboxed_host_process":  matrix.UnsandboxedHostProcess,
				"network_access":            matrix.NetworkAccess, "credential_access": matrix.CredentialAccess,
				"user_home_access":          matrix.UserHomeAccess,
				"persistent_user_terminal":  matrix.PersistentUserTerminal,
				"persistent_agent_terminal": matrix.PersistentAgentTerminal,
				"full_cdp":                  matrix.FullCDP, "out_of_scope_policy": matrix.OutOfScopePolicy,
			},
		})
	if err != nil {
		return selection, err
	}
	event.CreatedAt = now
	stored, replayed, err := state.TransitionRunExecutionPermission(ctx, next, operation, event)
	if err != nil {
		return selection, err
	}
	if replayed {
		return selection, fmt.Errorf("historical fixture unexpectedly replayed %s", operationKey)
	}
	return historicalRunPermissionSelection{Permission: stored, operation: operation, event: event}, nil
}

func TestHistoricalRunPermissionSelectionRejectsCurrentSchema(t *testing.T) {
	state := openRunSeedBoundaryStore(t, 0)
	before := runSeedBoundaryRows(t, state)
	if _, err := selectHistoricalRunPermission(t.Context(), state, "unseeded-run",
		domain.RunExecutionPermissionFullAccess, "historical-current-guard-0001", "reject current schema"); err == nil || !strings.Contains(err.Error(), "rejects schema") {
		t.Fatalf("historical selector accepted current schema: %v", err)
	}
	if after := runSeedBoundaryRows(t, state); !reflect.DeepEqual(before, after) {
		t.Fatalf("rejected historical selection changed rows: before=%v after=%v", before, after)
	}
}
