package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"
)

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
