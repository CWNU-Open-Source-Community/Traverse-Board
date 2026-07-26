package domain

import (
	"testing"
	"time"
)

func TestRunExecutionInteractionRequiresMatchingProfileAndExplicitTrust(t *testing.T) {
	at := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	mission := Mission{
		ID: "mission-interaction", Goal: "test interaction modes", Profile: ProfileCode,
		WorkspaceID: "workspace-interaction", Scope: DefaultScope("workspace-interaction"),
		CreatedAt: at, UpdatedAt: at,
	}
	run := Run{
		ID: "run-interaction", MissionID: mission.ID, SessionID: "session-interaction",
		Status: RunCreated, Config: RunConfig{ModelRoute: "mock/default"},
		Budget: DefaultBudget(), CreatedAt: at, UpdatedAt: at,
	}
	mode, err := NewInitialRunModeSnapshot("run-mode-interaction", run, mission,
		ExecutionSurfaceCode, ExecutionPhaseDeliver, "test_operator", "test mode", at)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := NewInitialRunExecutionProfileSnapshot("profile-preview", run, mission,
		"test_operator", "preview", at)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := NewInitialRunExecutionInteractionSnapshot("interaction-preview", run,
		mission, mode, preview, "test_operator", at)
	if err != nil {
		t.Fatal(err)
	}
	if initial.Mode != RunExecutionInteractionPreview ||
		initial.WorkspaceTrust != WorkspaceTrustUntrusted ||
		initial.ProcessEnabled || initial.ExecutionAuthorized || initial.CapabilityGrant {
		t.Fatalf("unexpected initial interaction: %+v", initial)
	}

	local, err := preview.Next("profile-local", RunExecutionProfileLocal,
		"test_operator", "select local", at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := initial.Next("interaction-controlled", RunExecutionInteractionControlled,
		mode, preview, WorkspaceTrustTrusted, true, "test_operator", "controlled",
		at.Add(2*time.Second)); err == nil {
		t.Fatal("controlled mode accepted a preview execution profile")
	}
	if _, err := initial.Next("interaction-controlled", RunExecutionInteractionControlled,
		mode, local, WorkspaceTrustUntrusted, true, "test_operator", "controlled",
		at.Add(2*time.Second)); err == nil {
		t.Fatal("controlled mode accepted an untrusted Workspace")
	}
	controlled, err := initial.Next("interaction-controlled",
		RunExecutionInteractionControlled, mode, local, WorkspaceTrustTrusted, true,
		"test_operator", "controlled", at.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if controlled.CommandForm != ExecutionCommandStructuredArgv ||
		controlled.PersistentTerminal || controlled.AgentInputDefault ||
		controlled.ProcessEnabled || controlled.ExecutionAuthorized ||
		controlled.CapabilityGrant {
		t.Fatalf("controlled mode widened authority: %+v", controlled)
	}
}

func TestRunExecutionInteractionSeparatesDebugAndCyberTrustModels(t *testing.T) {
	at := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	mission := Mission{
		ID: "mission-boundaries", Goal: "test boundaries", Profile: ProfileCode,
		WorkspaceID: "workspace-boundaries", Scope: DefaultScope("workspace-boundaries"),
		CreatedAt: at, UpdatedAt: at,
	}
	run := Run{
		ID: "run-boundaries", MissionID: mission.ID, SessionID: "session-boundaries",
		Status: RunCreated, Config: RunConfig{ModelRoute: "mock/default"},
		Budget: DefaultBudget(), CreatedAt: at, UpdatedAt: at,
	}
	codeMode, err := NewInitialRunModeSnapshot("mode-code", run, mission,
		ExecutionSurfaceCode, ExecutionPhaseDeliver, "test_operator", "code", at)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := NewInitialRunExecutionProfileSnapshot("profile-preview", run, mission,
		"test_operator", "preview", at)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := NewInitialRunExecutionInteractionSnapshot("interaction-preview", run,
		mission, codeMode, preview, "test_operator", at)
	if err != nil {
		t.Fatal(err)
	}
	local, err := preview.Next("profile-local", RunExecutionProfileLocal,
		"test_operator", "local", at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	debug, err := initial.Next("interaction-debug", RunExecutionInteractionDebug,
		codeMode, local, WorkspaceTrustTrusted, true, "test_operator", "debug",
		at.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !debug.PersistentTerminal || !debug.UserInputAvailable ||
		debug.AgentInputDefault || debug.RequiredGate != ExecutionInteractionGateDebugAgentLease {
		t.Fatalf("unexpected debug controls: %+v", debug)
	}

	cyberMode := codeMode
	cyberMode.Surface = ExecutionSurfaceCyber
	docker, err := preview.Next("profile-docker", RunExecutionProfileDocker,
		"test_operator", "docker", at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	cyber, err := initial.Next("interaction-cyber", RunExecutionInteractionCyber,
		cyberMode, docker, WorkspaceTrustTrusted, true, "test_operator", "cyber",
		at.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if cyber.CommandForm != ExecutionCommandContainerPTY ||
		cyber.RequiredGate != ExecutionInteractionGateCyberContainerPTY ||
		cyber.AgentInputDefault || cyber.ProcessEnabled || cyber.ExecutionAuthorized {
		t.Fatalf("unexpected cyber controls: %+v", cyber)
	}
	if _, err := initial.Next("interaction-cyber-local", RunExecutionInteractionCyber,
		cyberMode, local, WorkspaceTrustTrusted, true, "test_operator", "cyber",
		at.Add(2*time.Second)); err == nil {
		t.Fatal("cyber mode accepted a local execution profile")
	}
}
