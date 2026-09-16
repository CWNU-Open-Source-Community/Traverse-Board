package store

import (
	"context"
	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/session"
)

// GetHostCommandProposalEvidence reads only the exact saved, non-authorizing
// Session message linked by the immutable result. It never reconstructs output.
func (s *SQLiteStore) GetHostCommandProposalEvidence(ctx context.Context, proposalID string) (session.Message, bool, error) {
	result, _, found, err := getHostCommandProposalResult(ctx, s.db, proposalID)
	if err != nil || !found {
		return session.Message{}, false, err
	}
	proposal, err := getHostCommandProposal(ctx, s.db, proposalID)
	if err != nil {
		return session.Message{}, false, err
	}
	var messageID int64
	if err := s.db.QueryRowContext(ctx, `SELECT session_message_id FROM host_command_proposal_results
		WHERE id = ? AND proposal_id = ? AND run_id = ? AND session_id = ?`,
		result.ID, proposal.ID, proposal.RunID, proposal.SessionID).Scan(&messageID); err != nil {
		return session.Message{}, false, err
	}
	message, err := getSessionMessageByID(ctx, s.db, messageID)
	if err != nil {
		return session.Message{}, false, err
	}
	if result.ProposalID != proposal.ID || result.ProposalFingerprint != proposal.Fingerprint ||
		result.RunID != proposal.RunID || result.SessionID != proposal.SessionID ||
		message.SessionID != proposal.SessionID || message.Role != "tool" ||
		message.Provenance.Version != session.ContextProvenanceVersion ||
		message.Provenance.SourceKind != session.SourceGoCommandResult || result.SourceKind != session.SourceGoCommandResult ||
		message.Provenance.SourceRef != "host-command-proposal:"+proposal.ID || message.Provenance.SourceRef != result.SourceRef ||
		message.Provenance.InstructionAuthorized || message.Provenance.ContentSHA256 != result.ContentSHA256 ||
		session.ValidateStoredMessage(message) != nil {
		return session.Message{}, false, apperror.New(apperror.CodeConflict, "Host command saved output does not match its result provenance")
	}
	return message, true, nil
}
