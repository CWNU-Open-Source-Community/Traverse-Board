package application

import (
	"context"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/session"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type hostHistoryFixture struct {
	run      domain.Run
	mission  domain.Mission
	proposal runner.HostCommandProposal
	review   *runner.HostCommandReview
	result   *runner.HostCommandProposalResult
	receipt  *runner.HostExecutionReceipt
}

func (s *hostHistoryFixture) ListHostCommandProposals(context.Context, string, int) ([]runner.HostCommandProposal, error) {
	return []runner.HostCommandProposal{s.proposal}, nil
}
func (s *hostHistoryFixture) GetHostCommandProposal(context.Context, string) (runner.HostCommandProposal, error) {
	return s.proposal, nil
}
func (s *hostHistoryFixture) GetHostCommandProposalReview(context.Context, string) (runner.HostCommandReview, bool, error) {
	if s.review == nil {
		return runner.HostCommandReview{}, false, nil
	}
	return *s.review, true, nil
}
func (s *hostHistoryFixture) GetHostCommandProposalResult(context.Context, string) (runner.HostCommandProposalResult, bool, error) {
	if s.result == nil {
		return runner.HostCommandProposalResult{}, false, nil
	}
	return *s.result, true, nil
}
func (s *hostHistoryFixture) GetHostCommandProposalReceipt(context.Context, string) (runner.HostExecutionReceipt, bool, error) {
	if s.receipt == nil {
		return runner.HostExecutionReceipt{}, false, nil
	}
	return *s.receipt, true, nil
}
func hostHandoffRecordedFixture(t *testing.T) *hostHistoryFixture {
	t.Helper()
	root, now := t.TempDir(), time.Now().UTC()
	spec, err := runner.NewHostCommandSpec(runner.HostCommandSpecRequest{
		ExecutablePath: filepath.Join(root, "historical.exe"), ExecutableSHA256: strings.Repeat("a", 64),
		Argv: []string{"--version"}, WorkingDirectory: root, Environment: []string{"PATH=" + root},
		NetworkIntent: runner.HostNetworkIntentHost, TimeoutMilliseconds: 1000, Purpose: "historical inspection",
	})
	if err != nil {
		t.Fatal(err)
	}
	proposal := runner.HostCommandProposal{ID: "host-history", ProtocolVersion: runner.HostCommandProposalProtocolVersion,
		PolicyVersion: runner.HostCommandPolicyVersion, RunID: "run-history", MissionID: "mission-history", SessionID: "session-history",
		WorkspaceID: "workspace-history", RootAgentID: "root-history", InteractionSnapshotID: "interaction-history", InteractionRevision: 1,
		ExecutionProfileRevision: 1, PermissionSnapshotID: "permission-history", PermissionRevision: 1,
		PermissionMode: domain.RunExecutionPermissionApproval, RequestedBy: "run_supervisor", Spec: spec, CreatedAt: now}
	proposal.Fingerprint = runner.HostCommandProposalFingerprint(proposal)
	if err := proposal.Validate(); err != nil {
		t.Fatal(err)
	}
	review := runner.HostCommandReview{ID: "review-history", ProtocolVersion: runner.HostCommandReviewProtocolVersion,
		PolicyVersion: runner.HostCommandPolicyVersion, ProposalID: proposal.ID, ProposalFingerprint: proposal.Fingerprint,
		RunID: proposal.RunID, Decision: runner.HostCommandReviewApprove, ReviewedBy: "test_operator", Reason: "saved decision",
		OperationKeyDigest: strings.Repeat("b", 64), SingleUseExecutionAuthorized: true, CreatedAt: now}
	review.RequestFingerprint = runner.HostCommandReviewRequestFingerprint(review)
	review.Fingerprint = runner.HostCommandReviewFingerprint(review)
	if err := review.Validate(); err != nil {
		t.Fatal(err)
	}
	receipt := runner.HostExecutionReceipt{RequestID: "request-history", ProtocolVersion: runner.HostCommandReceiptProtocolVersion,
		PolicyVersion: runner.HostExecutionPolicyVersion, Backend: "historical-job", StdoutPrefixSHA256: session.ContentSHA256(""),
		StderrPrefixSHA256: session.ContentSHA256(""), StartedAt: now, CompletedAt: now, TreeReaped: true, NonSandboxed: true,
		JobAssignedAtCreation: true, KillOnJobClose: true, ActiveProcessLimit: runner.MaxHostActiveProcesses,
		JobMemoryLimit: runner.MaxHostProcessMemoryBytes, StdinClosed: true, NetworkRequested: true, ProductExecutionEnabled: true}
	if err := receipt.Validate(); err != nil {
		t.Fatal(err)
	}
	result := runner.HostCommandProposalResult{ID: "result-history", ProtocolVersion: runner.HostCommandResultProtocolVersion,
		PolicyVersion: runner.HostCommandPolicyVersion, ProposalID: proposal.ID, ProposalFingerprint: proposal.Fingerprint,
		ReviewID: review.ID, ReviewFingerprint: review.Fingerprint, RequestID: receipt.RequestID,
		RunID: proposal.RunID, SessionID: proposal.SessionID, Status: "completed", SourceKind: session.SourceGoCommandResult,
		SourceRef: "host-command-proposal:" + proposal.ID, ContentSHA256: strings.Repeat("c", 64), CreatedAt: now}
	result.Fingerprint = runner.HostCommandProposalResultFingerprint(result)
	if err := result.Validate(); err != nil {
		t.Fatal(err)
	}
	return &hostHistoryFixture{proposal: proposal, review: &review, result: &result, receipt: &receipt,
		run:     domain.Run{ID: proposal.RunID, MissionID: proposal.MissionID, SessionID: proposal.SessionID},
		mission: domain.Mission{ID: proposal.MissionID, WorkspaceID: proposal.WorkspaceID}}
}

func TestHostCommandHistoryReadsSavedResultsWithoutExecutionAuthority(t *testing.T) {
	fixture := hostHandoffRecordedFixture(t)
	history := NewHostCommandHistory(fixture)
	view, err := history.Get(t.Context(), fixture.proposal.ID)
	if err != nil || view.ID() != fixture.proposal.ID || view.Result == nil || view.Receipt == nil ||
		view.Receipt.RequestID != fixture.receipt.RequestID || view.Review.Fingerprint != fixture.review.Fingerprint {
		t.Fatalf("saved history changed: %+v %v", view, err)
	}
	fixture.result = nil
	fixture.receipt = nil
	view, err = history.Get(t.Context(), fixture.proposal.ID)
	if err != nil || view.Result != nil || view.Receipt != nil {
		t.Fatalf("history invented an execution: %+v %v", view, err)
	}
}

func (f *hostHistoryFixture) GetHostCommandProposalExecutionIntentByProposal(context.Context, string) (runner.HostExecutionIntent, bool, error) {
	return runner.HostExecutionIntent{}, false, nil
}
