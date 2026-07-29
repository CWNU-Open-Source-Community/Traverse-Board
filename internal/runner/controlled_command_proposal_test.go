package runner

import (
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
)

func TestControlledCommandProposalSealsFixedIntent(t *testing.T) {
	now := time.Now().UTC()
	request := controlledCommandTestRequest(t, ControlledCommandGitStatus)
	request.ID = "plan-proposal"
	plan, err := PlanControlledCommand(request)
	if err != nil {
		t.Fatal(err)
	}
	run := domain.Run{ID: plan.RunID, MissionID: request.Interaction.MissionID}
	mission := domain.Mission{ID: run.MissionID}
	permission, err := domain.NewInitialRunExecutionPermissionSnapshot(
		"permission-proposal", run, mission, "schema_v88", now)
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := NewControlledCommandProposal(
		ControlledCommandProposalRequest{
			ID: "command-proposal", Plan: plan, MissionID: mission.ID,
			SessionID: "session-proposal", RootAgentID: "agent-root-proposal",
			Permission: permission, Purpose: "inspect repository status",
			RequestedBy: "run_supervisor", CreatedAt: now,
		})
	if err != nil {
		t.Fatal(err)
	}
	if proposal.ExecutionAuthorized || proposal.InstructionAuthorized ||
		proposal.CapabilityGrant || proposal.Kind != ControlledCommandGitStatus ||
		proposal.Fingerprint == "" {
		t.Fatalf("unexpected proposal: %+v", proposal)
	}
	tampered := proposal
	tampered.Kind = ControlledCommandGoVersion
	if err := tampered.Validate(); err == nil {
		t.Fatal("tampered proposal unexpectedly validated")
	}
}

func TestControlledCommandProposalReviewIsSingleUseAndImmutable(t *testing.T) {
	proposal := controlledCommandProposalFixture(t)
	review, err := NewControlledCommandProposalReview(
		"command-review", proposal, ControlledCommandReviewApprove,
		"cli_operator", "approved for exact execution",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if !review.SingleUseExecutionAuthorized || review.CapabilityGrant {
		t.Fatalf("unexpected review authority: %+v", review)
	}
	tampered := review
	tampered.Decision = ControlledCommandReviewDeny
	if err := tampered.Validate(); err == nil {
		t.Fatal("tampered review unexpectedly validated")
	}
	for _, reviewer := range []string{
		"agent", "model", "repository", "skill", "supervisor", "run_supervisor",
	} {
		if _, err := NewControlledCommandProposalReview(
			"command-review-"+reviewer, proposal,
			ControlledCommandReviewApprove, reviewer, "must be rejected",
			"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			time.Now().UTC()); err == nil {
			t.Fatalf("reserved reviewer %q unexpectedly validated", reviewer)
		}
	}
}

func controlledCommandProposalFixture(t *testing.T) ControlledCommandProposal {
	t.Helper()
	now := time.Now().UTC()
	request := controlledCommandTestRequest(t, ControlledCommandGoVersion)
	request.ID = "plan-fixture"
	plan, err := PlanControlledCommand(request)
	if err != nil {
		t.Fatal(err)
	}
	run := domain.Run{ID: plan.RunID, MissionID: request.Interaction.MissionID}
	mission := domain.Mission{ID: run.MissionID}
	permission, err := domain.NewInitialRunExecutionPermissionSnapshot(
		"permission-fixture", run, mission, "schema_v88", now)
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := NewControlledCommandProposal(
		ControlledCommandProposalRequest{
			ID: "proposal-fixture", Plan: plan, MissionID: mission.ID,
			SessionID: "session-fixture", RootAgentID: "agent-root-fixture",
			Permission: permission, Purpose: "inspect Go toolchain version",
			RequestedBy: "run_supervisor", CreatedAt: now,
		})
	if err != nil {
		t.Fatal(err)
	}
	return proposal
}
