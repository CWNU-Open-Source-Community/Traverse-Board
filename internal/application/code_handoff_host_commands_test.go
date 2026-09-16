package application

import (
	"context"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/session"
)

type hostHandoffProjectionStore struct {
	CodeHandoffStore
	state *hostCommandProposalReviewStoreStub
}

func (s *hostHandoffProjectionStore) ListHostCommandProposals(ctx context.Context, id string, n int) ([]runner.HostCommandProposal, error) {
	return s.state.ListHostCommandProposals(ctx, id, n)
}
func (s *hostHandoffProjectionStore) GetHostCommandProposalReview(ctx context.Context, id string) (runner.HostCommandReview, bool, error) {
	return s.state.GetHostCommandProposalReview(ctx, id)
}
func (s *hostHandoffProjectionStore) GetHostCommandProposalResult(ctx context.Context, id string) (runner.HostCommandProposalResult, bool, error) {
	return s.state.GetHostCommandProposalResult(ctx, id)
}
func (s *hostHandoffProjectionStore) GetHostCommandProposalReceipt(ctx context.Context, id string) (runner.HostExecutionReceipt, bool, error) {
	return s.state.GetHostCommandProposalReceipt(ctx, id)
}

func hostHandoffRecordedFixture(t *testing.T) *hostCommandProposalReviewStoreStub {
	t.Helper()
	state := hostCommandProposalReviewFixture(t)
	review, err := runner.NewHostCommandReview("review-handoff", state.proposal, runner.HostCommandReviewApprove,
		"test_operator", "exact review", strings.Repeat("a", 64), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	intent, err := runner.NewApprovedHostExecutionIntent(state.proposal, review, strings.Repeat("b", 64), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	execution, err := (&hostCommandProposalExecutorStub{output: "bounded output"}).Execute(t.Context(), runner.HostExecutionRequest{Intent: intent})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := runner.ProjectHostExecutionReceipt(execution)
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.NewHostCommandProposalResult("result-handoff", state.proposal, review, intent.RequestID, "completed",
		session.SourceGoCommandResult, "host-command-proposal:"+state.proposal.ID, strings.Repeat("c", 64), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	state.review, state.receipt, state.result = &review, &receipt, &result
	return state
}

func TestCodeHandoffHostCommandReceiptBoundaries(t *testing.T) {
	for _, scenario := range []string{"recorded", "timed_out", "cancelled", "truncated", "wrong_session", "wrong_source", "wrong_request", "contradictory_status"} {
		t.Run(scenario, func(t *testing.T) {
			state := hostHandoffRecordedFixture(t)
			expectConflict := false
			switch scenario {
			case "timed_out":
				state.receipt.TimedOut = true
				state.result.Status = "failed"
			case "cancelled":
				state.receipt.Cancelled = true
				state.result.Status = "failed"
			case "truncated":
				state.receipt.StdoutObservedBytes = runner.MaxControlledOutputCaptureBytes + 1
				state.receipt.StdoutCapturedBytes = runner.MaxControlledOutputCaptureBytes
				state.receipt.StdoutTruncated = true
			case "wrong_session":
				state.result.SessionID = "another-session"
				expectConflict = true
			case "wrong_source":
				state.result.SourceRef = "host-command-proposal:another"
				expectConflict = true
			case "wrong_request":
				state.receipt.RequestID = "another-request"
				expectConflict = true
			case "contradictory_status":
				state.receipt.ExitCode = 3
				expectConflict = true
			}
			state.result.Fingerprint = runner.HostCommandProposalResultFingerprint(*state.result)
			service := NewCodeHandoffService(&hostHandoffProjectionStore{state: state})
			var handoff CodeHandoff
			err := service.addHostCommands(t.Context(), state.run, state.mission, &handoff)
			if expectConflict {
				if apperror.CodeOf(err) != apperror.CodeConflict {
					t.Fatalf("expected identity conflict, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			item := handoff.HostCommands.Items[0]
			if item.Receipt.TimedOut != state.receipt.TimedOut || item.Receipt.Cancelled != state.receipt.Cancelled ||
				item.Receipt.StdoutTruncated != state.receipt.StdoutTruncated || handoff.Verification.PassCount != 0 || handoff.StandardCodeDelivery != nil {
				t.Fatalf("execution fact changed or inferred a verification: %#v", handoff)
			}
		})
	}
}
