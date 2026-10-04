package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"
)

func TestControlledStartSpecRejectsCallerSelectedCommandShapes(t *testing.T) {
	request := controlledCommandTestRequest(t, ControlledCommandGoVersion)
	plan, err := PlanControlledCommand(request)
	if err != nil {
		t.Fatal(err)
	}
	spec := ControlledStartSpec{
		RequestID: ControlledExecutionRequestID(plan),
		PlanID:    plan.ID, PlanFingerprint: plan.Fingerprint,
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

func TestControlledExecutionReceiptRejectsUnsupportedOutputLimitClaim(t *testing.T) {
	now := time.Date(2026, 7, 26, 13, 0, 0, 0, time.UTC)
	emptyDigest := sha256.Sum256(nil)
	receipt := ControlledExecutionReceipt{
		RequestID: "controlled-exec-receipt", ProtocolVersion: ControlledExecutionProtocolVersion,
		PolicyVersion: ControlledExecutionPolicyVersion, Backend: "windows-fixed-restricted",
		StdoutPrefixSHA256: hex.EncodeToString(emptyDigest[:]), StderrPrefixSHA256: hex.EncodeToString(emptyDigest[:]),
		StartedAt: now, CompletedAt: now.Add(time.Second), OutputLimitExceeded: true,
		TreeReaped: true, RestrictedToken: true, LowIntegrityToken: true,
		JobAssignedAtCreation: true, KillOnJobClose: true, ActiveProcessLimit: 1,
		ProcessMemoryLimit: MaxControlledProcessMemoryBytes, StdinClosed: true, ProductExecutionEnabled: true,
	}
	if err := receipt.Validate(); !errors.Is(err, ErrControlledExecutionBoundary) {
		t.Fatalf("unsupported output-limit claim error=%v", err)
	}
	digest := sha256.Sum256(make([]byte, MaxControlledOutputCaptureBytes))
	receipt.StdoutObservedBytes = MaxControlledOutputObservedBytes
	receipt.StdoutCapturedBytes = MaxControlledOutputCaptureBytes
	receipt.StdoutPrefixSHA256 = hex.EncodeToString(digest[:])
	receipt.StdoutTruncated = true
	if err := receipt.Validate(); err != nil {
		t.Fatalf("supported output-limit claim error=%v", err)
	}
}
