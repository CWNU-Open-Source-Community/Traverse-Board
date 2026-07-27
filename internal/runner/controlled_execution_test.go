package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"
)

type controlledStarterStub struct {
	available bool
	result    ControlledStartResult
	err       error
	spec      ControlledStartSpec
}

func (s *controlledStarterStub) Name() string {
	return "controlled-starter-stub"
}

func (s *controlledStarterStub) Available() bool {
	return s.available
}

func (s *controlledStarterStub) Start(_ context.Context,
	spec ControlledStartSpec,
) (ControlledStartResult, error) {
	s.spec = spec
	return s.result, s.err
}

func TestControlledExecutorRevalidatesExactDurableBindings(t *testing.T) {
	request := controlledExecutionTestRequest(t, ControlledCommandGoVersion)
	starter := &controlledStarterStub{
		available: true, result: controlledStartTestResult(),
	}
	executor, err := NewControlledExecutor(starter)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.RunID != request.Plan.RunID ||
		result.PlanFingerprint != request.Plan.Fingerprint ||
		result.Backend != starter.Name() || !result.ProductExecutionEnabled ||
		!result.RestrictedToken || !result.LowIntegrityToken ||
		!result.JobAssignedAtCreation || !result.TreeReaped {
		t.Fatalf("unexpected execution result: %+v", result)
	}
	if starter.spec.ExecutableID != "go" ||
		starter.spec.WorkspaceRoot != request.WorkspaceRoot ||
		len(starter.spec.Argv) != 1 || starter.spec.Argv[0] != "version" {
		t.Fatalf("unexpected start spec: %+v", starter.spec)
	}
	if err := result.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestControlledExecutorRejectsModelStaleAndTamperedRequests(t *testing.T) {
	request := controlledExecutionTestRequest(t, ControlledCommandGitStatus)
	starter := &controlledStarterStub{
		available: true, result: controlledStartTestResult(),
	}
	executor, err := NewControlledExecutor(starter)
	if err != nil {
		t.Fatal(err)
	}
	request.RequestedBy = "model"
	if _, err := executor.Execute(context.Background(), request); !errors.Is(err, ErrControlledExecutionDenied) {
		t.Fatalf("model requester error=%v", err)
	}
	request = controlledExecutionTestRequest(t, ControlledCommandGitStatus)
	request.CurrentProfile.Revision++
	if _, err := executor.Execute(context.Background(), request); !errors.Is(err, ErrControlledExecutionBoundary) {
		t.Fatalf("stale profile error=%v", err)
	}
	request = controlledExecutionTestRequest(t, ControlledCommandGitStatus)
	request.Plan.Argv = []string{"status", "--porcelain"}
	request.Plan.Fingerprint = controlledCommandPlanFingerprint(request.Plan)
	if _, err := executor.Execute(context.Background(), request); !errors.Is(err, ErrControlledCommandBoundary) {
		t.Fatalf("tampered argv error=%v", err)
	}
}

func TestControlledExecutorFailsClosedWhenPlatformIsUnavailable(t *testing.T) {
	executor, err := NewControlledExecutor(&controlledStarterStub{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Execute(context.Background(),
		controlledExecutionTestRequest(t, ControlledCommandGoVersion)); !errors.Is(err, ErrControlledExecutionPlatform) {
		t.Fatalf("unavailable starter error=%v", err)
	}
}

func TestControlledStartSpecRejectsCallerSelectedCommandShapes(t *testing.T) {
	request := controlledExecutionTestRequest(t, ControlledCommandGoVersion)
	spec := ControlledStartSpec{
		RequestID: ControlledExecutionRequestID(request.Plan),
		PlanID:    request.Plan.ID, PlanFingerprint: request.Plan.Fingerprint,
		ExecutableID: "go", Argv: []string{"env", "GOPATH"},
		WorkspaceRoot: request.WorkspaceRoot,
		Timeout:       DefaultControlledCommandTimeout,
	}
	if err := spec.Validate(); !errors.Is(err, ErrControlledExecutionBoundary) {
		t.Fatalf("caller-selected argv error=%v", err)
	}
	spec.ExecutableID = "cmd"
	spec.Argv = []string{"/c", "dir"}
	if err := spec.Validate(); !errors.Is(err, ErrControlledExecutionBoundary) {
		t.Fatalf("caller-selected executable error=%v", err)
	}
}

func TestControlledStartResultRejectsUnsupportedOutputLimitClaim(t *testing.T) {
	result := controlledStartTestResult()
	result.OutputLimitExceeded = true
	if err := result.Validate(); !errors.Is(err, ErrControlledExecutionBoundary) {
		t.Fatalf("unsupported output-limit claim error=%v", err)
	}

	data := make([]byte, MaxControlledOutputCaptureBytes)
	digest := sha256.Sum256(data)
	result.Stdout = ControlledOutput{
		Data: data, ObservedBytes: MaxControlledOutputObservedBytes,
		CapturedBytes:        len(data),
		CapturedPrefixSHA256: hex.EncodeToString(digest[:]),
		Truncated:            true,
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("supported output-limit claim error=%v", err)
	}
}

func controlledExecutionTestRequest(t *testing.T,
	kind ControlledCommandKind,
) ControlledExecutionRequest {
	t.Helper()
	planRequest := controlledCommandTestRequest(t, kind)
	plan, err := PlanControlledCommand(planRequest)
	if err != nil {
		t.Fatal(err)
	}
	return ControlledExecutionRequest{
		Plan: plan, WorkspaceRoot: planRequest.WorkspaceRoot,
		Interaction:    planRequest.Interaction,
		CurrentProfile: planRequest.CurrentProfile,
		CurrentSurface: planRequest.CurrentSurface,
		RequestedBy:    "test_operator", OperatorConfirmed: true,
	}
}

func controlledStartTestResult() ControlledStartResult {
	now := time.Date(2026, 7, 26, 13, 0, 0, 0, time.UTC)
	emptyDigest := sha256.Sum256(nil)
	empty := ControlledOutput{
		CapturedPrefixSHA256: hex.EncodeToString(emptyDigest[:]),
	}
	return ControlledStartResult{
		ExitCode: 0, Stdout: empty, Stderr: empty,
		StartedAt: now, CompletedAt: now.Add(time.Second),
		TreeReaped: true, RestrictedToken: true, LowIntegrityToken: true,
		JobAssignedAtCreation: true, KillOnJobClose: true,
		ActiveProcessLimit: 1,
		ProcessMemoryLimit: MaxControlledProcessMemoryBytes,
		StdinClosed:        true, ProductExecutionEnabled: true,
	}
}
