package runner

import (
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
)

func TestHistoricalControlledCommandProposalRejectsAlteredIntent(t *testing.T) {
	proposal := controlledCommandProposalFixture()
	if err := proposal.Validate(); err != nil {
		t.Fatal(err)
	}
	proposal.Kind = ControlledCommandGitStatus
	if err := proposal.Validate(); err == nil {
		t.Fatal("tampered historical proposal unexpectedly validated")
	}
}

func TestHistoricalControlledCommandReviewIsSingleUseAndImmutable(t *testing.T) {
	proposal := controlledCommandProposalFixture()
	review := ControlledCommandProposalReview{
		ID: "command-review", ProtocolVersion: ControlledCommandReviewProtocolVersion,
		PolicyVersion: ControlledCommandProposalPolicyVersion, ProposalID: proposal.ID, ProposalFingerprint: proposal.Fingerprint,
		RunID: proposal.RunID, MissionID: proposal.MissionID, SessionID: proposal.SessionID, WorkspaceID: proposal.WorkspaceID,
		Decision: ControlledCommandReviewApprove, ReviewedBy: "cli_operator", Reason: "approved for exact execution",
		OperationKeyDigest: hostCommandTestDigest, SingleUseExecutionAuthorized: true, CreatedAt: proposal.CreatedAt,
	}
	review.RequestFingerprint = ControlledCommandReviewRequestFingerprint(review)
	if err := review.Validate(); err != nil {
		t.Fatal(err)
	}
	tampered := review
	tampered.Decision = ControlledCommandReviewDeny
	if err := tampered.Validate(); err == nil {
		t.Fatal("tampered historical review unexpectedly validated")
	}
	for _, reviewer := range []string{"agent", "model", "repository", "skill", "supervisor", "run_supervisor"} {
		tampered := review
		tampered.ReviewedBy = reviewer
		tampered.RequestFingerprint = ControlledCommandReviewRequestFingerprint(tampered)
		if err := tampered.Validate(); err == nil {
			t.Fatalf("reserved reviewer %q unexpectedly validated", reviewer)
		}
	}
}

func controlledCommandProposalFixture() ControlledCommandProposal {
	proposal := ControlledCommandProposal{
		ID: "proposal-fixture", ProtocolVersion: ControlledCommandProposalProtocolVersion, PolicyVersion: ControlledCommandProposalPolicyVersion,
		RunID: "run-fixture", MissionID: "mission-fixture", SessionID: "session-fixture", WorkspaceID: "workspace-fixture", RootAgentID: "agent-root-fixture",
		InteractionSnapshotID: "interaction-fixture", InteractionRevision: 1, ExecutionProfileRevision: 1,
		PermissionSnapshotID: "permission-fixture", PermissionRevision: 1, PermissionMode: domain.RunExecutionPermissionConservative,
		PlanID: "plan-fixture", PlanFingerprint: hostCommandTestDigest, Kind: ControlledCommandGoVersion,
		TimeoutMilliseconds: 1000, Purpose: "inspect Go toolchain version", RequestedBy: "run_supervisor",
		CreatedAt: time.Date(2026, 7, 26, 13, 0, 0, 0, time.UTC),
	}
	proposal.Fingerprint = ControlledCommandProposalFingerprint(proposal)
	return proposal
}
