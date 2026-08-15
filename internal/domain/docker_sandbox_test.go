package domain

import (
	"strings"
	"testing"
	"time"
)

func TestDockerSandboxAdmissionRequiresExactAuthorizedBoundary(t *testing.T) {
	value := dockerSandboxAdmissionFixture(t)
	if err := value.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	mutations := []struct {
		name string
		edit func(*DockerSandboxAdmission)
	}{
		{"network", func(value *DockerSandboxAdmission) { value.NetworkMode = "allowlist" }},
		{"approval", func(value *DockerSandboxAdmission) { value.ApprovalID = "" }},
		{"epoch", func(value *DockerSandboxAdmission) { value.RuntimeEpochFingerprint = "" }},
		{"authority", func(value *DockerSandboxAdmission) { value.ExecutionAuthorized = false }},
		{"artifact", func(value *DockerSandboxAdmission) { value.ArtifactCommitAuthorized = false }},
		{"manifest", func(value *DockerSandboxAdmission) { value.ManifestJSON += " " }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			changed := value
			mutation.edit(&changed)
			changed.AdmissionFingerprint = DockerSandboxAdmissionFingerprint(changed)
			if err := changed.Validate(); err == nil {
				t.Fatal("mutated admission unexpectedly validated")
			}
		})
	}
}

func TestDockerSandboxDenialIsMetadataOnlyAndExact(t *testing.T) {
	now := time.Date(2026, 8, 14, 9, 59, 0, 0, time.UTC)
	value := DockerSandboxDenial{
		ProtocolVersion:    DockerSandboxDenialProtocolVersion,
		OperationKeyDigest: strings.Repeat("d", 64), RunID: "run-1",
		MissionID: "mission-1", WorkspaceID: "workspace-1", PlanID: "plan-1",
		RequestedBy: "cli_operator", ReasonCode: DockerSandboxReasonBudgetExhausted,
		RemediationCode: DockerSandboxRemediationRestoreBudget,
		NetworkMode:     "disabled", CreatedAt: now,
	}
	value.DenialFingerprint = DockerSandboxDenialFingerprint(value)
	if err := value.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	changed := value
	changed.NetworkMode = "allowlist"
	changed.DenialFingerprint = DockerSandboxDenialFingerprint(changed)
	if err := changed.Validate(); err == nil {
		t.Fatal("network-enabled denial unexpectedly validated")
	}
}

func TestDockerSandboxRecordBindsLaunchAndReceipt(t *testing.T) {
	admission := dockerSandboxAdmissionFixture(t)
	now := admission.CreatedAt.Add(time.Second)
	start := DockerSandboxStartIntent{
		AdmissionID: admission.ID, ProtocolVersion: DockerSandboxStartProtocolVersion,
		OperationKeyDigest:      strings.Repeat("c", 64),
		RequestFingerprint:      strings.Repeat("d", 64),
		RuntimeEpochFingerprint: admission.RuntimeEpochFingerprint,
		RunID:                   admission.RunID, RequestedBy: admission.RequestedBy, CreatedAt: now,
	}
	start.StartFingerprint = DockerSandboxStartFingerprint(start)
	launch := DockerSandboxLaunch{
		AdmissionID: admission.ID, ProtocolVersion: DockerSandboxLaunchProtocolVersion,
		StartOperationKeyDigest:     start.OperationKeyDigest,
		LifecycleIntentID:           "docker-lifecycle-1",
		LifecycleRequestFingerprint: strings.Repeat("b", 64),
		AttemptID:                   "docker-attempt-1",
		RunID:                       admission.RunID, CreatedAt: now,
	}
	launch.LaunchFingerprint = DockerSandboxLaunchFingerprint(launch)
	exit := 0
	receipt := DockerSandboxReceipt{
		ID: "docker-receipt-1", AdmissionID: admission.ID,
		ProtocolVersion:   DockerSandboxReceiptProtocolVersion,
		LifecycleIntentID: launch.LifecycleIntentID, AttemptID: launch.AttemptID,
		RunID: admission.RunID, WorkspaceID: admission.WorkspaceID,
		Outcome: DockerSandboxOutcomeSucceeded, ReasonCode: DockerSandboxReasonCompleted,
		ExitCode: &exit, LogReceiptID: "docker-log-1", OutputStagingReceiptID: "",
		OutputCommitReceiptID: "", ArtifactCount: 0, CleanupComplete: true,
		ArtifactCommitAuthorized: true,
		CompletedAt:              now.Add(time.Second),
	}
	receipt.ReceiptFingerprint = DockerSandboxReceiptFingerprint(receipt)
	record := DockerSandboxRecord{Admission: admission, Start: &start,
		Launch: &launch, Receipt: &receipt}
	if err := record.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	changed := receipt
	changed.AttemptID = "foreign-attempt"
	changed.ReceiptFingerprint = DockerSandboxReceiptFingerprint(changed)
	record.Receipt = &changed
	if err := record.Validate(); err == nil {
		t.Fatal("foreign receipt unexpectedly validated")
	}
}

func TestDockerSandboxStartIntentIsExactAndEpochBound(t *testing.T) {
	admission := dockerSandboxAdmissionFixture(t)
	value := DockerSandboxStartIntent{
		AdmissionID: admission.ID, ProtocolVersion: DockerSandboxStartProtocolVersion,
		OperationKeyDigest:      strings.Repeat("b", 64),
		RequestFingerprint:      strings.Repeat("c", 64),
		RuntimeEpochFingerprint: admission.RuntimeEpochFingerprint,
		RunID:                   admission.RunID, RequestedBy: admission.RequestedBy,
		CreatedAt: admission.CreatedAt.Add(time.Second),
	}
	value.StartFingerprint = DockerSandboxStartFingerprint(value)
	if err := value.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	record := DockerSandboxRecord{Admission: admission, Start: &value}
	if err := record.Validate(); err != nil {
		t.Fatalf("record Validate() error = %v", err)
	}
	changed := value
	changed.RuntimeEpochFingerprint = strings.Repeat("f", 64)
	changed.StartFingerprint = DockerSandboxStartFingerprint(changed)
	record.Start = &changed
	if err := record.Validate(); err == nil {
		t.Fatal("foreign runtime epoch unexpectedly bound to admission")
	}
}

func TestDockerSandboxCancellationBindsExactOperation(t *testing.T) {
	now := time.Date(2026, 8, 14, 10, 1, 0, 0, time.UTC)
	value := DockerSandboxCancellation{
		ID: "docker-cancel-1", AdmissionID: "docker-admission-1",
		ProtocolVersion: DockerSandboxCancellationProtocolVersion,
		RunID:           "run-1", RequestedBy: "cli_operator",
		OperationKeyDigest: strings.Repeat("c", 64),
		ReasonCode:         DockerSandboxReasonCancelled, RequestedAt: now,
	}
	value.CancellationFingerprint = DockerSandboxCancellationFingerprint(value)
	if err := value.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	value.ReasonCode = DockerSandboxReasonTimedOut
	value.CancellationFingerprint = DockerSandboxCancellationFingerprint(value)
	if err := value.Validate(); err == nil {
		t.Fatal("non-cancel reason unexpectedly validated")
	}
}

func dockerSandboxAdmissionFixture(t *testing.T) DockerSandboxAdmission {
	t.Helper()
	digest := strings.Repeat("a", 64)
	manifest := `{"protocol_version":"sandbox_manifest.v1","backend":"docker"}`
	now := time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC)
	value := DockerSandboxAdmission{
		ID: "docker-admission-1", ProtocolVersion: DockerSandboxAdmissionProtocolVersion,
		OperationKeyDigest: digest, RequestFingerprint: digest,
		LifecycleOperationDigest: digest, RunID: "run-1", MissionID: "mission-1",
		WorkspaceID: "workspace-1", PlanID: "plan-1", CandidateID: "candidate-1",
		PreparationID: "preparation-1", ManifestJSON: manifest,
		ManifestFingerprint: digestParts("sandbox_manifest.v1", manifest),
		PlanFingerprint:     digest, SpecFingerprint: digest, AuthorityFingerprint: digest,
		ReadinessFingerprint: digest, ReadinessExpiresAt: now.Add(30 * time.Second),
		RuntimeEpochFingerprint: digest, ProfileSnapshotID: "profile-1", ProfileRevision: 2,
		PermissionSnapshotID: "permission-1", PermissionRevision: 2,
		PermissionMode: RunExecutionPermissionApproval,
		ApprovalID:     "approval-1", ApprovalVersion: 2, PolicyFingerprint: digest,
		NetworkMode: "disabled", CPUQuotaMillis: 1000, MemoryBytes: 256 * 1024 * 1024,
		PIDs: 64, DiskBytes: 4 * 1024 * 1024, WallClockSeconds: 30,
		LogBytes: 256 * 1024, LogLines: 4096, ToolCallsRemaining: 4,
		Decision: DockerSandboxAdmissionAuthorized, ReasonCode: DockerSandboxReasonReady,
		RemediationCode: DockerSandboxRemediationNone, ProductEntryEnabled: true,
		ExecutionAuthorized: true, ArtifactCommitAuthorized: true,
		RequestedBy: "cli_operator", CreatedAt: now,
	}
	value.AdmissionFingerprint = DockerSandboxAdmissionFingerprint(value)
	return value
}
