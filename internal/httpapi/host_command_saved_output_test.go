package httpapi

import (
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/runner"
)

func TestHostCommandSavedOutputProjectionRequiresExactSavedIdentity(t *testing.T) {
	now := time.Now().UTC()
	view := testHostCommandProposalView(t, "run-saved-output", "mission-saved-output", "session-saved-output", "workspace-saved-output")
	review := runner.HostCommandReview{
		ID: "host-review-saved-output", ProtocolVersion: runner.HostCommandReviewProtocolVersion,
		PolicyVersion: runner.HostCommandPolicyVersion, ProposalID: view.Proposal.ID,
		ProposalFingerprint: view.Proposal.Fingerprint, RunID: view.Proposal.RunID,
		Decision: runner.HostCommandReviewApprove, ReviewedBy: "test_operator", Reason: "reviewed exact command",
		OperationKeyDigest: strings.Repeat("a", 64), SingleUseExecutionAuthorized: true, CreatedAt: now,
	}
	review.RequestFingerprint = runner.HostCommandReviewRequestFingerprint(review)
	review.Fingerprint = runner.HostCommandReviewFingerprint(review)
	view.Review = &review
	receipt := testHostCommandReceipt(now)
	receipt.ProtocolVersion, receipt.PolicyVersion = runner.HostCommandReceiptProtocolVersion, runner.HostExecutionPolicyVersion
	receipt.StdoutCapturedBytes, receipt.StdoutObservedBytes = 6, 6
	view.Receipt = receipt
	output := runner.NewHostCommandSavedOutput(runner.HostExecutionResult{Stdout: runner.ControlledOutput{Data: []byte("中文")}})
	result := runner.HostCommandProposalResult{
		ID: "host-result-saved-output", ProtocolVersion: runner.HostCommandResultProtocolVersion,
		PolicyVersion: runner.HostCommandPolicyVersion, ProposalID: view.Proposal.ID,
		ProposalFingerprint: view.Proposal.Fingerprint, ReviewID: review.ID, ReviewFingerprint: review.Fingerprint,
		RequestID: receipt.RequestID, RunID: view.Proposal.RunID, SessionID: view.Proposal.SessionID,
		Status: "completed", SourceKind: "go_command_result", SourceRef: "host-command-proposal:" + view.Proposal.ID,
		ContentSHA256: strings.Repeat("b", 64), CreatedAt: now, SavedOutput: &output,
	}
	result.Fingerprint = runner.HostCommandProposalResultFingerprint(result)
	view.Result = &result
	projected := hostCommandSavedOutputView(view)
	if projected == nil || projected.Stdout.Text != "中文" || projected.Stdout.UTF8Bytes != 6 ||
		projected.ResultID != result.ID || projected.RequestID != receipt.RequestID {
		t.Fatalf("valid projection missing: %#v; result=%v review=%v receipt=%v", projected,
			result.Validate(), review.Validate(), receipt.Validate())
	}
	for _, test := range []struct {
		name   string
		change func(*application.HostCommandProposalView)
	}{
		{"legacy", func(v *application.HostCommandProposalView) { v.Result.SavedOutput = nil }},
		{"wrong run", func(v *application.HostCommandProposalView) { v.Result.RunID = "other-run" }},
		{"wrong session", func(v *application.HostCommandProposalView) { v.Result.SessionID = "other-session" }},
		{"wrong proposal", func(v *application.HostCommandProposalView) { v.Result.ProposalID = "other-proposal" }},
		{"wrong source", func(v *application.HostCommandProposalView) { v.Result.SourceRef = "other-source" }},
		{"wrong request", func(v *application.HostCommandProposalView) { v.Result.RequestID = "other-request" }},
		{"wrong review", func(v *application.HostCommandProposalView) { v.Result.ReviewID = "other-review" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := view
			copy := result
			changed.Result = &copy
			test.change(&changed)
			changed.Result.Fingerprint = runner.HostCommandProposalResultFingerprint(*changed.Result)
			if hostCommandSavedOutputView(changed) != nil {
				t.Fatal("unbound or historical result acquired structured output")
			}
		})
	}
}
