package domain

import (
	"testing"
	"time"
)

func TestRunExecutionPermissionModesHaveClosedDefinitions(t *testing.T) {
	now := time.Now().UTC()
	mission := Mission{ID: "mission-permission", CreatedAt: now}
	run := Run{ID: "run-permission", MissionID: mission.ID, Status: RunCreated, CreatedAt: now}
	initial, err := NewInitialRunExecutionPermissionSnapshot(
		"permission-initial", run, mission, "test_operator", now)
	if err != nil {
		t.Fatal(err)
	}
	if initial.Mode != RunExecutionPermissionConservative ||
		initial.CommandScope != ExecutionPermissionCommandFixedTemplates ||
		initial.OperatorConfirmed || initial.ProcessEnabled ||
		initial.ExecutionAuthorized || initial.CapabilityGrant {
		t.Fatalf("unexpected initial permission: %+v", initial)
	}
	workspaceAccess, err := initial.Next("permission-workspace-access",
		RunExecutionPermissionWorkspaceAccess, true, "test_operator",
		"operator selected sandboxed Workspace access", now)
	if err != nil {
		t.Fatal(err)
	}
	matrix, err := workspaceAccess.CapabilityMatrix()
	if err != nil || workspaceAccess.ApprovalPolicy !=
		ExecutionPermissionApprovalOutOfScopeExactOnce ||
		workspaceAccess.CommandScope != ExecutionPermissionCommandSandboxedWorkspace ||
		workspaceAccess.FilesystemScope != ExecutionPermissionFilesystemWorkspaceGuarded ||
		workspaceAccess.NetworkScope != ExecutionPermissionNetworkDisabled ||
		!matrix.WorkspaceRead || !matrix.WorkspaceWrite ||
		!matrix.SandboxedCommandRuntime || matrix.UnsandboxedHostProcess ||
		matrix.NetworkAccess || matrix.CredentialAccess || matrix.UserHomeAccess ||
		matrix.PersistentUserTerminal || matrix.PersistentAgentTerminal || matrix.FullCDP ||
		matrix.OutOfScopePolicy != ExecutionPermissionOutOfScopeExactOnce {
		t.Fatalf("unexpected Workspace Access permission: %+v matrix=%+v err=%v",
			workspaceAccess, matrix, err)
	}
	approval, err := workspaceAccess.Next("permission-approval",
		RunExecutionPermissionApproval, true, "test_operator",
		"operator selected per-command approval", now)
	if err != nil {
		t.Fatal(err)
	}
	if approval.ApprovalPolicy != ExecutionPermissionApprovalPerCommand ||
		approval.CommandScope != ExecutionPermissionCommandArbitraryStateless ||
		approval.FilesystemScope != ExecutionPermissionFilesystemHostFull ||
		approval.NetworkScope != ExecutionPermissionNetworkHost {
		t.Fatalf("unexpected approval permission: %+v", approval)
	}
	full, err := approval.Next("permission-full", RunExecutionPermissionFullAccess,
		true, "test_operator", "operator selected full access", now)
	if err != nil {
		t.Fatal(err)
	}
	if full.PersistentTerminal || full.BackgroundProcess || full.AgentTerminalInput {
		t.Fatalf("full access unexpectedly grants debug capabilities: %+v", full)
	}
	debug, err := full.Next("permission-debug", RunExecutionPermissionDebug,
		true, "test_operator", "operator selected debug access", now)
	if err != nil {
		t.Fatal(err)
	}
	if !debug.PersistentTerminal || !debug.BackgroundProcess ||
		!debug.AgentTerminalInput {
		t.Fatalf("debug capabilities are incomplete: %+v", debug)
	}
}

func TestExecutionPermissionRuntimeCapabilitiesRequireMonotonicGates(t *testing.T) {
	if err := (ExecutionPermissionRuntimeCapabilities{
		DebugMaximumAccessEnabled: true,
	}).Validate(); err == nil {
		t.Fatal("debug gate without danger-full-access was accepted")
	}
	if err := (ExecutionPermissionRuntimeCapabilities{
		DangerFullAccessEnabled: true,
	}).Validate(); err == nil {
		t.Fatal("danger-full-access without permission control was accepted")
	}
	capabilities := ExecutionPermissionRuntimeCapabilities{
		WorkspaceSandboxEnabled: true,
		OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
		DebugMaximumAccessEnabled: true,
	}
	if err := capabilities.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []RunExecutionPermissionMode{
		RunExecutionPermissionConservative, RunExecutionPermissionWorkspaceAccess,
		RunExecutionPermissionApproval,
		RunExecutionPermissionFullAccess, RunExecutionPermissionDebug,
	} {
		if !capabilities.Allows(mode) {
			t.Fatalf("expected runtime to allow %s", mode)
		}
	}
}

func TestWorkspaceAccessRuntimeGateIsIndependentAndFailsClosed(t *testing.T) {
	closed := ExecutionPermissionRuntimeCapabilities{}
	if closed.Allows(RunExecutionPermissionWorkspaceAccess) {
		t.Fatal("Workspace Access became available without a verified sandbox adapter")
	}
	workspaceOnly := ExecutionPermissionRuntimeCapabilities{WorkspaceSandboxEnabled: true}
	if err := workspaceOnly.Validate(); err != nil ||
		!workspaceOnly.Allows(RunExecutionPermissionWorkspaceAccess) ||
		workspaceOnly.Allows(RunExecutionPermissionApproval) ||
		workspaceOnly.Allows(RunExecutionPermissionFullAccess) ||
		workspaceOnly.Allows(RunExecutionPermissionDebug) {
		t.Fatalf("Workspace Sandbox gate widened host authority: %+v err=%v",
			workspaceOnly, err)
	}
}

func TestWorkspaceAccessTransitionsFromAndToEveryExistingMode(t *testing.T) {
	now := time.Now().UTC()
	mission := Mission{ID: "mission-workspace-transitions", CreatedAt: now}
	run := Run{ID: "run-workspace-transitions", MissionID: mission.ID,
		Status: RunCreated, CreatedAt: now}
	initial, err := NewInitialRunExecutionPermissionSnapshot(
		"permission-transition-initial", run, mission, "test_operator", now)
	if err != nil {
		t.Fatal(err)
	}
	for _, existing := range []RunExecutionPermissionMode{
		RunExecutionPermissionConservative, RunExecutionPermissionApproval,
		RunExecutionPermissionFullAccess, RunExecutionPermissionDebug,
	} {
		base := initial
		if existing != RunExecutionPermissionConservative {
			base, err = initial.Next("permission-transition-from-"+string(existing),
				existing, true, "test_operator", "select existing permission", now)
			if err != nil {
				t.Fatalf("prepare %s: %v", existing, err)
			}
		}
		workspace, err := base.Next("permission-transition-to-workspace-"+string(existing),
			RunExecutionPermissionWorkspaceAccess, true, "test_operator",
			"select Workspace Access", now)
		if err != nil || workspace.Mode != RunExecutionPermissionWorkspaceAccess {
			t.Fatalf("%s -> Workspace Access: %+v err=%v", existing, workspace, err)
		}
		confirmed := existing != RunExecutionPermissionConservative
		back, err := workspace.Next("permission-transition-back-"+string(existing),
			existing, confirmed, "test_operator", "restore existing permission", now)
		if err != nil || back.Mode != existing {
			t.Fatalf("Workspace Access -> %s: %+v err=%v", existing, back, err)
		}
	}
}
