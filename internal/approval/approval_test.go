package approval

import (
	"testing"
	"time"
)

func TestFingerprintIsDeterministicAndLengthDelimited(t *testing.T) {
	first := Fingerprint("ab", "c")
	if first != Fingerprint("ab", "c") {
		t.Fatal("fingerprint is not deterministic")
	}
	if first == Fingerprint("a", "bc") {
		t.Fatal("length-delimited fingerprint collided")
	}
	if len(first) != 64 {
		t.Fatalf("unexpected fingerprint length: %d", len(first))
	}
	operationKey := OperationKeyDigest("client-review-key")
	if operationKey == "client-review-key" || len(operationKey) != 64 || operationKey != OperationKeyDigest("client-review-key") {
		t.Fatalf("operation key was not safely digested: %q", operationKey)
	}
}

func TestRecordRequiresConsistentDecisionMetadata(t *testing.T) {
	now := time.Now().UTC()
	record := Record{
		ID: "approval-test", IdempotencyKey: "proposal:shell:tool-test", ProposalID: "tool-test",
		ToolName: "shell", ActionClass: "shell", Mode: "per_call", Status: StatusPending,
		RequestFingerprint: Fingerprint("request"), RequestedBy: "test", Version: 1,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := record.Validate(); err != nil {
		t.Fatal(err)
	}
	record.Status = StatusApproved
	if err := record.Validate(); err == nil {
		t.Fatal("expected decided approval without metadata to fail")
	}
	record.ReviewedBy = "operator"
	record.DecidedAt = &now
	if err := record.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestDecisionRequestRejectsIdempotencyKeyReuseShapeChanges(t *testing.T) {
	request := DecisionRequest{
		ProposalID: "tool-test", IdempotencyKey: "review:shell:tool-test:approve",
		Action: ActionApprove, ReviewedBy: "operator",
	}
	normalized, err := request.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if DecisionFingerprint(normalized) == DecisionFingerprint(DecisionRequest{
		ProposalID: "tool-other", IdempotencyKey: normalized.IdempotencyKey,
		Action: ActionApprove, ReviewedBy: "operator",
	}) {
		t.Fatal("decision fingerprint did not bind the proposal")
	}
}

func TestGrantRequestFingerprintPreservesLegacyReplay(t *testing.T) {
	legacy := CreateGrantRequest{SessionID: "session-test", WorkspaceID: "workspace-test",
		ToolName: "shell", ActionClass: "shell", Reason: "trusted build",
		GrantedBy: "operator"}
	want := Fingerprint("session_grant.v1", legacy.SessionID, legacy.WorkspaceID,
		legacy.ToolName, legacy.ActionClass, legacy.Reason, legacy.GrantedBy)
	if got := GrantRequestFingerprint(legacy); got != want {
		t.Fatalf("legacy grant fingerprint changed across schema v136: got=%s want=%s",
			got, want)
	}
	bounded := legacy
	bounded.ToolName = "host_command_propose"
	bounded.ActionClass = "risk_escalation"
	bounded.ScopeFingerprint = Fingerprint("risk-scope")
	bounded.Generation = 1
	bounded.MaxUses = 2
	bounded.TTL = time.Minute
	bounded.ModeSnapshotID = "mode-test"
	bounded.ModeRevision = 1
	bounded.InteractionSnapshotID = "interaction-test"
	bounded.InteractionRevision = 1
	bounded.ExecutionProfileSnapshotID = "profile-test"
	bounded.ExecutionProfileRevision = 1
	bounded.PermissionSnapshotID = "permission-test"
	bounded.PermissionRevision = 1
	bounded.PermissionMode = "workspace_access"
	bounded.WorkspaceRootFingerprint = Fingerprint("root")
	bounded.CapabilityGeneration = Fingerprint("capability")
	if GrantRequestFingerprint(bounded) == want {
		t.Fatal("bounded grant reused the legacy request fingerprint")
	}
}
