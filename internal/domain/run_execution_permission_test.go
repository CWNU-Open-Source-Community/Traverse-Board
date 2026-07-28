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
	approval, err := initial.Next("permission-approval",
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
		OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
		DebugMaximumAccessEnabled: true,
	}
	if err := capabilities.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []RunExecutionPermissionMode{
		RunExecutionPermissionConservative, RunExecutionPermissionApproval,
		RunExecutionPermissionFullAccess, RunExecutionPermissionDebug,
	} {
		if !capabilities.Allows(mode) {
			t.Fatalf("expected runtime to allow %s", mode)
		}
	}
}
