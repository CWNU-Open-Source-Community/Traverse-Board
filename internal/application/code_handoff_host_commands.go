package application

import (
	"context"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/session"
)

const MaxCodeHandoffHostCommands = 20

type codeHandoffHostCommandStore interface {
	ListHostCommandProposals(context.Context, string, int) ([]runner.HostCommandProposal, error)
	GetHostCommandProposalReview(context.Context, string) (runner.HostCommandReview, bool, error)
	GetHostCommandProposalResult(context.Context, string) (runner.HostCommandProposalResult, bool, error)
	GetHostCommandProposalReceipt(context.Context, string) (runner.HostExecutionReceipt, bool, error)
}

// These are execution facts, not assertions about the current workspace revision
// or an inferred test outcome. No output body is loaded by this projection.
type CodeHandoffHostCommands struct {
	Items     []CodeHandoffHostCommand `json:"items"`
	Truncated bool                     `json:"truncated"`
}

type CodeHandoffHostCommand struct {
	ProposalID       string                         `json:"proposal_id"`
	RunID            string                         `json:"run_id"`
	SessionID        string                         `json:"session_id"`
	WorkspaceID      string                         `json:"workspace_id"`
	Purpose          string                         `json:"purpose"`
	WorkingDirectory string                         `json:"working_directory"`
	SpecFingerprint  string                         `json:"spec_fingerprint"`
	CreatedAt        time.Time                      `json:"created_at"`
	ReviewID         string                         `json:"review_id,omitempty"`
	ReviewDecision   string                         `json:"review_decision,omitempty"`
	ResultID         string                         `json:"result_id,omitempty"`
	ResultStatus     string                         `json:"result_status,omitempty"`
	SourceRef        string                         `json:"source_ref,omitempty"`
	ContentSHA256    string                         `json:"content_sha256,omitempty"`
	Receipt          *CodeHandoffHostCommandReceipt `json:"receipt,omitempty"`
}

type CodeHandoffHostCommandReceipt struct {
	RequestID           string    `json:"request_id"`
	ExitCode            int       `json:"exit_code"`
	TimedOut            bool      `json:"timed_out"`
	Cancelled           bool      `json:"cancelled"`
	StdoutTruncated     bool      `json:"stdout_truncated"`
	StderrTruncated     bool      `json:"stderr_truncated"`
	OutputLimitExceeded bool      `json:"output_limit_exceeded"`
	TreeReaped          bool      `json:"tree_reaped"`
	NonSandboxed        bool      `json:"non_sandboxed"`
	StartedAt           time.Time `json:"started_at"`
	CompletedAt         time.Time `json:"completed_at"`
}

func (s *CodeHandoffService) addHostCommands(ctx context.Context, run domain.Run,
	mission domain.Mission, handoff *CodeHandoff,
) error {
	reader, ok := s.store.(codeHandoffHostCommandStore)
	if !ok {
		return nil
	}
	proposals, err := reader.ListHostCommandProposals(ctx, run.ID, MaxCodeHandoffHostCommands+1)
	if err != nil {
		return apperror.Normalize(err)
	}
	projection := &CodeHandoffHostCommands{Items: make([]CodeHandoffHostCommand, 0, min(len(proposals), MaxCodeHandoffHostCommands)), Truncated: len(proposals) > MaxCodeHandoffHostCommands}
	for index, proposal := range proposals {
		if index == MaxCodeHandoffHostCommands {
			break
		}
		if proposal.Validate() != nil || proposal.RunID != run.ID || proposal.SessionID != run.SessionID ||
			proposal.MissionID != mission.ID || proposal.WorkspaceID != mission.WorkspaceID {
			return apperror.New(apperror.CodeConflict, "Code handoff host proposal escaped its Run binding")
		}
		item := CodeHandoffHostCommand{ProposalID: proposal.ID, RunID: run.ID, SessionID: run.SessionID,
			WorkspaceID: mission.WorkspaceID, Purpose: proposal.Spec.Purpose, WorkingDirectory: proposal.Spec.WorkingDirectory,
			SpecFingerprint: proposal.Spec.Fingerprint, CreatedAt: proposal.CreatedAt}
		review, reviewed, err := reader.GetHostCommandProposalReview(ctx, proposal.ID)
		if err != nil {
			return apperror.Normalize(err)
		}
		if reviewed {
			if review.Validate() != nil || review.ProposalID != proposal.ID || review.RunID != run.ID || review.ProposalFingerprint != proposal.Fingerprint {
				return apperror.New(apperror.CodeConflict, "Code handoff host review escaped its proposal binding")
			}
			item.ReviewID, item.ReviewDecision = review.ID, string(review.Decision)
		}
		result, executed, err := reader.GetHostCommandProposalResult(ctx, proposal.ID)
		if err != nil {
			return apperror.Normalize(err)
		}
		if executed {
			receipt, found, err := reader.GetHostCommandProposalReceipt(ctx, result.RequestID)
			if err != nil {
				return apperror.Normalize(err)
			}
			if !reviewed || review.Decision != runner.HostCommandReviewApprove || result.Validate() != nil ||
				result.ProposalID != proposal.ID || result.ProposalFingerprint != proposal.Fingerprint ||
				result.RunID != run.ID || result.SessionID != run.SessionID || result.ReviewID != review.ID ||
				result.ReviewFingerprint != review.Fingerprint || result.SourceKind != session.SourceGoCommandResult ||
				result.SourceRef != "host-command-proposal:"+proposal.ID || !found || receipt.Validate() != nil || receipt.RequestID != result.RequestID {
				return apperror.New(apperror.CodeConflict, "Code handoff host result escaped its reviewed execution binding")
			}
			status := "completed"
			if receipt.ExitCode != 0 || receipt.TimedOut || receipt.Cancelled || receipt.OutputLimitExceeded {
				status = "failed"
			}
			if result.Status != status {
				return apperror.New(apperror.CodeConflict, "Code handoff host result contradicts its execution receipt")
			}
			item.ResultID, item.ResultStatus, item.SourceRef, item.ContentSHA256 = result.ID, result.Status, result.SourceRef, result.ContentSHA256
			item.Receipt = &CodeHandoffHostCommandReceipt{RequestID: receipt.RequestID, ExitCode: receipt.ExitCode,
				TimedOut: receipt.TimedOut, Cancelled: receipt.Cancelled, StdoutTruncated: receipt.StdoutTruncated,
				StderrTruncated: receipt.StderrTruncated, OutputLimitExceeded: receipt.OutputLimitExceeded,
				TreeReaped: receipt.TreeReaped, NonSandboxed: receipt.NonSandboxed, StartedAt: receipt.StartedAt, CompletedAt: receipt.CompletedAt}
		}
		projection.Items = append(projection.Items, item)
	}
	handoff.HostCommands = projection
	return nil
}
