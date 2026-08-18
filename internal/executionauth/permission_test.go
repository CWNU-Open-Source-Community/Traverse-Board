package executionauth

import (
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
)

func permissionSnapshot(t *testing.T,
	mode domain.RunExecutionPermissionMode,
) domain.RunExecutionPermissionSnapshot {
	t.Helper()
	now := time.Now().UTC()
	mission := domain.Mission{ID: "mission-authz", CreatedAt: now}
	run := domain.Run{
		ID: "run-authz", MissionID: mission.ID, Status: domain.RunCreated, CreatedAt: now,
	}
	initial, err := domain.NewInitialRunExecutionPermissionSnapshot(
		"permission-initial", run, mission, "test_operator", now)
	if err != nil {
		t.Fatal(err)
	}
	if mode == domain.RunExecutionPermissionConservative {
		return initial
	}
	next, err := initial.Next("permission-next", mode, true, "test_operator",
		"test permission selection", now)
	if err != nil {
		t.Fatal(err)
	}
	return next
}

func TestEvaluateExecutionPermissionDistinguishesAllFourModes(t *testing.T) {
	conservative, err := EvaluateExecutionPermission(
		permissionSnapshot(t, domain.RunExecutionPermissionConservative),
		domain.ExecutionPermissionRuntimeCapabilities{},
		PermissionRequest{Kind: PermissionOperationFixedTemplate})
	if err != nil || !conservative.Allowed || conservative.RequiresApproval {
		t.Fatalf("conservative=%+v err=%v", conservative, err)
	}
	denied, err := EvaluateExecutionPermission(
		permissionSnapshot(t, domain.RunExecutionPermissionConservative),
		domain.ExecutionPermissionRuntimeCapabilities{},
		PermissionRequest{Kind: PermissionOperationStatelessCommand})
	if err != nil || denied.Allowed {
		t.Fatalf("conservative arbitrary command=%+v err=%v", denied, err)
	}

	approvalRuntime := domain.ExecutionPermissionRuntimeCapabilities{
		OperatorApprovalEnabled: true,
	}
	pending, err := EvaluateExecutionPermission(
		permissionSnapshot(t, domain.RunExecutionPermissionApproval), approvalRuntime,
		PermissionRequest{Kind: PermissionOperationStatelessCommand, Network: true})
	if err != nil || pending.Allowed || !pending.RequiresApproval {
		t.Fatalf("approval pending=%+v err=%v", pending, err)
	}
	approved, err := EvaluateExecutionPermission(
		permissionSnapshot(t, domain.RunExecutionPermissionApproval), approvalRuntime,
		PermissionRequest{
			Kind: PermissionOperationStatelessCommand, Network: true,
			HostFilesystem: true, OperatorApproved: true,
		})
	if err != nil || !approved.Allowed || !approved.RequiresApproval ||
		!approved.Network || !approved.HostFilesystem {
		t.Fatalf("approval approved=%+v err=%v", approved, err)
	}

	fullRuntime := domain.ExecutionPermissionRuntimeCapabilities{
		OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
	}
	full, err := EvaluateExecutionPermission(
		permissionSnapshot(t, domain.RunExecutionPermissionFullAccess), fullRuntime,
		PermissionRequest{
			Kind: PermissionOperationStatelessCommand, Network: true, HostFilesystem: true,
		})
	if err != nil || !full.Allowed || full.RequiresApproval {
		t.Fatalf("full=%+v err=%v", full, err)
	}
	managed, err := EvaluateExecutionPermission(
		permissionSnapshot(t, domain.RunExecutionPermissionFullAccess), fullRuntime,
		PermissionRequest{Kind: PermissionOperationManagedCommand,
			HostFilesystem: true, BackgroundProcess: true})
	if err != nil || !managed.Allowed || managed.RequiresApproval ||
		!managed.HostFilesystem || !managed.BackgroundProcess || managed.Network ||
		managed.PersistentTerminal || managed.AgentTerminalInput {
		t.Fatalf("full managed command=%+v err=%v", managed, err)
	}
	if _, err := EvaluateExecutionPermission(
		permissionSnapshot(t, domain.RunExecutionPermissionFullAccess), fullRuntime,
		PermissionRequest{Kind: PermissionOperationManagedCommand,
			HostFilesystem: true, Network: true, BackgroundProcess: true}); err == nil {
		t.Fatal("managed command accepted a network capability")
	}
	fullPersistent, err := EvaluateExecutionPermission(
		permissionSnapshot(t, domain.RunExecutionPermissionFullAccess), fullRuntime,
		PermissionRequest{Kind: PermissionOperationPersistentTerminal})
	if err != nil || fullPersistent.Allowed {
		t.Fatalf("full persistent=%+v err=%v", fullPersistent, err)
	}

	debugRuntime := domain.ExecutionPermissionRuntimeCapabilities{
		OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
		DebugMaximumAccessEnabled: true,
	}
	debug, err := EvaluateExecutionPermission(
		permissionSnapshot(t, domain.RunExecutionPermissionDebug), debugRuntime,
		PermissionRequest{
			Kind: PermissionOperationPersistentTerminal, Network: true,
			HostFilesystem: true, BackgroundProcess: true, AgentTerminalInput: true,
		})
	if err != nil || !debug.Allowed || !debug.PersistentTerminal ||
		!debug.BackgroundProcess || !debug.AgentTerminalInput {
		t.Fatalf("debug=%+v err=%v", debug, err)
	}
}

func TestEvaluateExecutionPermissionRechecksRuntimeGate(t *testing.T) {
	decision, err := EvaluateExecutionPermission(
		permissionSnapshot(t, domain.RunExecutionPermissionFullAccess),
		domain.ExecutionPermissionRuntimeCapabilities{},
		PermissionRequest{Kind: PermissionOperationStatelessCommand})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Allowed {
		t.Fatal("persisted full-access selection bypassed the process-local gate")
	}
}
