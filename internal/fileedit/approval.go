package fileedit

import "cyberagent-workbench/internal/approval"

// ApprovalFingerprint preserves the existing ledger format, including the
// legacy replace fingerprint. Execution also binds original/destination hashes
// in the prepared ApplyOperation; this helper does not grant authority.
func ApprovalFingerprint(sessionID, workspaceID string, edit Edit) string {
	if edit.Operation == "" || edit.Operation == OperationReplace {
		return approval.FileEditFingerprint(sessionID, workspaceID, edit.Path, edit.ProposedHash)
	}
	return approval.FileMutationFingerprint(ApprovalToolName(edit), sessionID,
		workspaceID, edit.Operation, edit.Path, edit.DestinationPath, edit.OriginalHash,
		edit.ProposedHash, edit.DestinationOriginalHash, edit.DestinationProposedHash)
}
