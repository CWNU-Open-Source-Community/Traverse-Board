package fileedit

// SameProposalContent compares immutable proposal identity and content. Review
// and application may legitimately change status, reason, and timestamps.
func SameProposalContent(left, right Edit) bool {
	return left.ID == right.ID && left.SessionID == right.SessionID &&
		left.WorkspaceID == right.WorkspaceID && left.Path == right.Path &&
		left.Operation == right.Operation && left.DestinationPath == right.DestinationPath &&
		left.OriginalText == right.OriginalText && left.ProposedText == right.ProposedText &&
		left.Diff == right.Diff && left.OriginalHash == right.OriginalHash &&
		left.ProposedHash == right.ProposedHash &&
		left.DestinationOriginalHash == right.DestinationOriginalHash &&
		left.DestinationProposedHash == right.DestinationProposedHash &&
		left.SecretsRedacted == right.SecretsRedacted
}
