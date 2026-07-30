package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
)

type hostExecutionFixture struct {
	intent      HostExecutionIntent
	environment []string
	interaction domain.RunExecutionInteractionSnapshot
	profile     domain.RunExecutionProfileSnapshot
	permission  domain.RunExecutionPermissionSnapshot
	surface     domain.ExecutionSurface
}

type hostStarterStub struct {
	available bool
	started   HostStartSpec
	result    HostStartResult
	err       error
}

func (s *hostStarterStub) Name() string {
	return "host-stub"
}

func (s *hostStarterStub) Available() bool {
	return s.available
}

func (s *hostStarterStub) Start(
	_ context.Context,
	spec HostStartSpec,
) (HostStartResult, error) {
	s.started = spec
	return s.result, s.err
}

func TestHostExecutorRequiresFullAccessRuntimeAndReturnsSealedReceipt(
	t *testing.T,
) {
	fixture := newHostExecutionFixture(t)
	starter := &hostStarterStub{
		available: true,
		result:    validHostStartResult(),
	}
	executor, err := NewHostExecutor(starter)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(),
		hostExecutionRequestFromFixture(fixture))
	if err != nil {
		t.Fatal(err)
	}
	if starter.started.RequestID != fixture.intent.RequestID ||
		starter.started.Command.Fingerprint != fixture.intent.Spec.Fingerprint ||
		!result.NonSandboxed || result.RestrictedToken ||
		result.EnvironmentInherited || !result.NetworkRequested {
		t.Fatalf("unexpected host execution result: %+v", result)
	}
	receipt, err := ProjectHostExecutionReceipt(result)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.RequestID != result.RequestID || !receipt.NonSandboxed ||
		receipt.ProtocolVersion != HostCommandReceiptProtocolVersion {
		t.Fatalf("unexpected host execution receipt: %+v", receipt)
	}
}

func TestHostExecutorRejectsMissingGateEnvironmentAndStaleBinding(t *testing.T) {
	fixture := newHostExecutionFixture(t)
	executor, err := NewHostExecutor(&hostStarterStub{
		available: true,
		result:    validHostStartResult(),
	})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*HostExecutionRequest)
	}{
		{
			name: "danger gate missing",
			mutate: func(request *HostExecutionRequest) {
				request.Runtime.DangerFullAccessEnabled = false
			},
		},
		{
			name: "environment changed",
			mutate: func(request *HostExecutionRequest) {
				request.Environment[0] += "-tampered"
			},
		},
		{
			name: "permission stale",
			mutate: func(request *HostExecutionRequest) {
				request.Permission.Revision++
			},
		},
		{
			name: "confirmation missing",
			mutate: func(request *HostExecutionRequest) {
				request.ExplicitlyConfirmed = false
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := hostExecutionRequestFromFixture(fixture)
			test.mutate(&request)
			if _, err := executor.Execute(
				context.Background(), request); err == nil {
				t.Fatal("unauthorized host command unexpectedly executed")
			}
		})
	}
}

func TestHostExecutionIntentIsDeterministicAndDisablesAutomaticRetry(
	t *testing.T,
) {
	fixture := newHostExecutionFixture(t)
	if fixture.intent.AutomaticRetryAllowed || !fixture.intent.NonSandboxed {
		t.Fatalf("unexpected host intent: %+v", fixture.intent)
	}
	replayed, err := NewHostExecutionIntent(HostExecutionIntentRequest{
		OperationKeyDigest: fixture.intent.OperationKeyDigest,
		RunID:              fixture.intent.RunID, MissionID: fixture.intent.MissionID,
		SessionID:   fixture.intent.SessionID,
		WorkspaceID: fixture.intent.WorkspaceID,
		Interaction: fixture.interaction, Profile: fixture.profile,
		Permission: fixture.permission, Spec: fixture.intent.Spec,
		RequestedBy: fixture.intent.RequestedBy,
		CreatedAt:   fixture.intent.CreatedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if replayed.RequestID != fixture.intent.RequestID {
		t.Fatal("same operation did not reproduce the request identity")
	}
	tampered := fixture.intent
	tampered.Spec.Purpose = "tampered"
	if err := tampered.Validate(); err == nil {
		t.Fatal("tampered host execution intent unexpectedly validated")
	}
}

func TestHostExecutorPreservesValidTerminalErrorResult(t *testing.T) {
	fixture := newHostExecutionFixture(t)
	started := validHostStartResult()
	started.TimedOut = true
	executor, err := NewHostExecutor(&hostStarterStub{
		available: true, result: started, err: context.DeadlineExceeded,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(
		context.Background(), hostExecutionRequestFromFixture(fixture))
	if !errors.Is(err, context.DeadlineExceeded) || !result.TimedOut {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func newHostExecutionFixture(t *testing.T) hostExecutionFixture {
	t.Helper()
	at := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	mission := domain.Mission{
		ID: "mission-host-execution", Goal: "execute exact host command",
		Profile: domain.ProfileCode, WorkspaceID: "workspace-host-execution",
		Scope:     domain.DefaultScope("workspace-host-execution"),
		CreatedAt: at, UpdatedAt: at,
	}
	run := domain.Run{
		ID: "run-host-execution", MissionID: mission.ID,
		SessionID: "session-host-execution", Status: domain.RunCreated,
		Config: domain.RunConfig{ModelRoute: "mock/default"},
		Budget: domain.DefaultBudget(), CreatedAt: at, UpdatedAt: at,
	}
	mode, err := domain.NewInitialRunModeSnapshot(
		"mode-host-execution", run, mission, domain.ExecutionSurfaceCode,
		domain.ExecutionPhaseDeliver, "test_operator", "code", at)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := domain.NewInitialRunExecutionProfileSnapshot(
		"profile-host-preview", run, mission, "test_operator", "preview", at)
	if err != nil {
		t.Fatal(err)
	}
	initialInteraction, err := domain.NewInitialRunExecutionInteractionSnapshot(
		"interaction-host-preview", run, mission, mode, preview,
		"test_operator", at)
	if err != nil {
		t.Fatal(err)
	}
	local, err := preview.Next(
		"profile-host-local", domain.RunExecutionProfileLocal,
		"test_operator", "local", at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	interaction, err := initialInteraction.Next(
		"interaction-host-controlled",
		domain.RunExecutionInteractionControlled, mode, local,
		domain.WorkspaceTrustTrusted, true, "test_operator",
		"controlled", at.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	initialPermission, err := domain.NewInitialRunExecutionPermissionSnapshot(
		"permission-host-conservative", run, mission, "test_operator", at)
	if err != nil {
		t.Fatal(err)
	}
	permission, err := initialPermission.Next(
		"permission-host-full", domain.RunExecutionPermissionFullAccess,
		true, "test_operator", "danger full access", at.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	specRequest := hostCommandSpecTestRequest(t)
	spec, err := NewHostCommandSpec(specRequest)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := NewHostExecutionIntent(HostExecutionIntentRequest{
		OperationKeyDigest: hostCommandTestDigest,
		RunID:              run.ID, MissionID: mission.ID, SessionID: run.SessionID,
		WorkspaceID: mission.WorkspaceID, Interaction: interaction,
		Profile: local, Permission: permission, Spec: spec,
		RequestedBy: "cli_operator", CreatedAt: at.Add(4 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	return hostExecutionFixture{
		intent: intent, environment: specRequest.Environment,
		interaction: interaction, profile: local,
		permission: permission, surface: mode.Surface,
	}
}

func hostExecutionRequestFromFixture(
	fixture hostExecutionFixture,
) HostExecutionRequest {
	return HostExecutionRequest{
		Intent:      fixture.intent,
		Environment: append([]string(nil), fixture.environment...),
		Interaction: fixture.interaction, CurrentProfile: fixture.profile,
		Permission: fixture.permission,
		Runtime: domain.ExecutionPermissionRuntimeCapabilities{
			OperatorApprovalEnabled: true, DangerFullAccessEnabled: true,
		},
		CurrentSurface:      fixture.surface,
		RequestedBy:         fixture.intent.RequestedBy,
		ExplicitlyConfirmed: true,
	}
}

func validHostStartResult() HostStartResult {
	emptyDigest := sha256.Sum256(nil)
	output := ControlledOutput{
		Data: nil, CapturedPrefixSHA256: hex.EncodeToString(emptyDigest[:]),
	}
	started := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	return HostStartResult{
		Stdout: output, Stderr: output, StartedAt: started,
		CompletedAt: started.Add(time.Second),
		TreeReaped:  true, NonSandboxed: true,
		JobAssignedAtCreation: true, KillOnJobClose: true,
		ActiveProcessLimit: MaxHostActiveProcesses,
		JobMemoryLimit:     MaxHostProcessMemoryBytes,
		StdinClosed:        true, NetworkRequested: true,
		ProductExecutionEnabled: true,
	}
}
