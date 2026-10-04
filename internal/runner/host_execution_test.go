package runner

import (
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
)

func TestHistoricalHostExecutionIntentRetainsIdentityAndDisablesAutomaticRetry(t *testing.T) {
	spec, err := NewHostCommandSpec(hostCommandSpecTestRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	intent := HostExecutionIntent{
		ProtocolVersion: HostCommandIntentProtocolVersion, PolicyVersion: HostExecutionPolicyVersion,
		OperationKeyDigest: hostCommandTestDigest, RunID: "run-host-execution", MissionID: "mission-host-execution",
		SessionID: "session-host-execution", WorkspaceID: "workspace-host-execution",
		InteractionSnapshotID: "interaction-host-controlled", InteractionRevision: 2, ExecutionProfileRevision: 2,
		PermissionSnapshotID: "permission-host-full", PermissionRevision: 2,
		PermissionMode: domain.RunExecutionPermissionFullAccess, Spec: spec,
		RequestedBy: "cli_operator", NonSandboxed: true,
		CreatedAt: time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
	}
	intent.RequestID = HostExecutionRequestID(intent.RunID, intent.OperationKeyDigest, intent.Spec.Fingerprint)
	if err := intent.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*HostExecutionIntent){
		func(value *HostExecutionIntent) { value.AutomaticRetryAllowed = true },
		func(value *HostExecutionIntent) { value.NonSandboxed = false },
		func(value *HostExecutionIntent) { value.Spec.Purpose = "tampered" },
	} {
		tampered := intent
		mutate(&tampered)
		if err := tampered.Validate(); err == nil {
			t.Fatal("tampered historical execution intent unexpectedly validated")
		}
	}
}
