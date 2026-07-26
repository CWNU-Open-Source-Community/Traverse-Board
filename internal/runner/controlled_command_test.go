package runner

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
)

func TestControlledCommandPlanUsesClosedStructuredArgv(t *testing.T) {
	request := controlledCommandTestRequest(t, ControlledCommandGitStatus)
	plan, err := PlanControlledCommand(request)
	if err != nil {
		t.Fatal(err)
	}
	if plan.ExecutableID != "git" || len(plan.Argv) != 4 ||
		!plan.WorkingDirectoryBound || !plan.StdinClosed ||
		plan.EnvironmentInherited || plan.ProfileLoadingEnabled ||
		plan.PersistentProcess || plan.CallerShellTextAccepted ||
		plan.GoOwnedPowerShellScript || plan.NetworkRequested ||
		!plan.OSSandboxRequired || !plan.StartBlocked ||
		plan.ProductExecutionEnabled {
		t.Fatalf("controlled plan widened its boundary: %+v", plan)
	}
	if err := plan.Validate(); err != nil {
		t.Fatal(err)
	}
	tampered := plan
	tampered.Argv = []string{"status", "--porcelain", "&&", "whoami"}
	tampered.Fingerprint = controlledCommandPlanFingerprint(tampered)
	if err := tampered.Validate(); !errors.Is(err, ErrControlledCommandBoundary) {
		t.Fatalf("tampered argv error=%v", err)
	}
}

func TestControlledPowerShellPlanAcceptsOnlyGoOwnedOneShotTemplate(t *testing.T) {
	request := controlledCommandTestRequest(t,
		ControlledCommandPowerShellWorkspaceList)
	request.RelativePath = filepath.Join("src", "internal")
	plan, err := PlanControlledCommand(request)
	if err != nil {
		t.Fatal(err)
	}
	if plan.ExecutableID != "windows-powershell" ||
		!plan.GoOwnedPowerShellScript || len(plan.Argv) != 8 ||
		plan.Argv[1] != "-NoProfile" || plan.Argv[2] != "-NonInteractive" ||
		plan.Argv[4] != "Restricted" || plan.Argv[7] != request.RelativePath ||
		plan.CallerShellTextAccepted || !plan.StartBlocked {
		t.Fatalf("unexpected PowerShell plan: %+v", plan)
	}
	tampered := plan
	tampered.Argv[6] = `Invoke-Expression $args[0]`
	tampered.Fingerprint = controlledCommandPlanFingerprint(tampered)
	if err := tampered.Validate(); !errors.Is(err, ErrControlledCommandBoundary) {
		t.Fatalf("tampered PowerShell template error=%v", err)
	}
	request.RelativePath = ".."
	if _, err := PlanControlledCommand(request); !errors.Is(err, ErrControlledCommandBoundary) {
		t.Fatalf("escaping path error=%v", err)
	}
}

func TestControlledCommandPlanRejectsDebugAndStaleProfileBindings(t *testing.T) {
	request := controlledCommandTestRequest(t, ControlledCommandGoVersion)
	request.Interaction.Mode = domain.RunExecutionInteractionDebug
	request.Interaction.CommandForm = domain.ExecutionCommandUserConPTY
	request.Interaction.PersistentTerminal = true
	request.Interaction.UserInputAvailable = true
	request.Interaction.RequiredGate = domain.ExecutionInteractionGateDebugAgentLease
	if _, err := PlanControlledCommand(request); !errors.Is(err, ErrControlledCommandBoundary) {
		t.Fatalf("debug interaction error=%v", err)
	}
	request = controlledCommandTestRequest(t, ControlledCommandGoVersion)
	request.CurrentProfile.Revision++
	if _, err := PlanControlledCommand(request); !errors.Is(err, ErrControlledCommandBoundary) {
		t.Fatalf("stale profile error=%v", err)
	}
}

func controlledCommandTestRequest(t *testing.T,
	kind ControlledCommandKind,
) ControlledCommandPlanRequest {
	t.Helper()
	at := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	mission := domain.Mission{
		ID: "mission-command", Goal: "plan a command", Profile: domain.ProfileCode,
		WorkspaceID: "workspace-command", Scope: domain.DefaultScope("workspace-command"),
		CreatedAt: at, UpdatedAt: at,
	}
	run := domain.Run{
		ID: "run-command", MissionID: mission.ID, SessionID: "session-command",
		Status: domain.RunCreated,
		Config: domain.RunConfig{ModelRoute: "mock/default"},
		Budget: domain.DefaultBudget(), CreatedAt: at, UpdatedAt: at,
	}
	mode, err := domain.NewInitialRunModeSnapshot("mode-command", run, mission,
		domain.ExecutionSurfaceCode, domain.ExecutionPhaseDeliver,
		"test_operator", "code", at)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := domain.NewInitialRunExecutionProfileSnapshot("profile-preview",
		run, mission, "test_operator", "preview", at)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := domain.NewInitialRunExecutionInteractionSnapshot(
		"interaction-preview", run, mission, mode, preview, "test_operator", at)
	if err != nil {
		t.Fatal(err)
	}
	local, err := preview.Next("profile-local", domain.RunExecutionProfileLocal,
		"test_operator", "local", at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	controlled, err := initial.Next("interaction-controlled",
		domain.RunExecutionInteractionControlled, mode, local,
		domain.WorkspaceTrustTrusted, true, "test_operator", "controlled",
		at.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return ControlledCommandPlanRequest{
		ID: "command-plan-test", WorkspaceID: mission.WorkspaceID,
		WorkspaceRoot: filepath.Clean(t.TempDir()), Interaction: controlled,
		CurrentProfile: local, CurrentSurface: mode.Surface, Kind: kind,
	}
}
