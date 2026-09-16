package httpapi

import (
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/runner"
)

// This read projection is independent of the legacy evidence envelope. Old
// results omit it; callers must not infer streams from arbitrary output markers.
type HostCommandSavedOutputView struct {
	ResultID  string                     `json:"result_id"`
	RequestID string                     `json:"request_id"`
	Stdout    HostCommandSavedStreamView `json:"stdout"`
	Stderr    HostCommandSavedStreamView `json:"stderr"`
}

type HostCommandSavedStreamView struct {
	Text      string `json:"text"`
	UTF8Bytes int    `json:"utf8_bytes"`
	Truncated bool   `json:"truncated"`
	// Redacted means the saved text passed through the redaction policy. It does
	// not assert that a secret was found or that every possible secret is known.
	Redacted bool `json:"redacted"`
}

func hostCommandSavedOutputView(view application.HostCommandProposalView) *HostCommandSavedOutputView {
	result, receipt, review := view.Result, view.Receipt, view.Review
	if result == nil || result.SavedOutput == nil || receipt == nil || review == nil ||
		result.Validate() != nil || receipt.Validate() != nil || review.Validate() != nil ||
		result.ProposalID != view.Proposal.ID || result.ProposalFingerprint != view.Proposal.Fingerprint ||
		result.RunID != view.Proposal.RunID || result.SessionID != view.Proposal.SessionID ||
		result.SourceKind != "go_command_result" || result.SourceRef != "host-command-proposal:"+view.Proposal.ID ||
		review.ProposalID != view.Proposal.ID || review.ProposalFingerprint != view.Proposal.Fingerprint ||
		review.RunID != view.Proposal.RunID || review.Decision != runner.HostCommandReviewApprove ||
		result.ReviewID != review.ID || result.ReviewFingerprint != review.Fingerprint ||
		result.RequestID != receipt.RequestID || result.SavedOutput.ValidateReceipt(*receipt) != nil {
		return nil
	}
	stream := func(value runner.HostCommandSavedStream) HostCommandSavedStreamView {
		return HostCommandSavedStreamView{Text: value.Text, UTF8Bytes: len(value.Text),
			Truncated: value.Truncated, Redacted: value.Redacted}
	}
	return &HostCommandSavedOutputView{ResultID: result.ID, RequestID: result.RequestID,
		Stdout: stream(result.SavedOutput.Stdout), Stderr: stream(result.SavedOutput.Stderr)}
}
