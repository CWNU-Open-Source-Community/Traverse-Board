package store

import (
	"context"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runmutation"
)

// GetWorkspaceImageByOperationKey observes the original upload without
// reserving an intent or taking a SQLite write transaction. Absence is only a
// current observation; a previously in-flight import may still commit later.
func (s *SQLiteStore) GetWorkspaceImageByOperationKey(ctx context.Context, workspaceID, operationKey string) (domain.WorkspaceImage, bool, error) {
	key, err := domain.NormalizeAgentOperationKey(operationKey)
	if err != nil || key != operationKey || !domain.ValidAgentID(workspaceID) {
		return domain.WorkspaceImage{}, false, apperror.New(apperror.CodeInvalidArgument, "Workspace image request identity is invalid")
	}
	op := runmutation.Fingerprint("workspace_image_operation.v1", workspaceID, key)
	image, _, err := scanWorkspaceImage(s.db.QueryRowContext(ctx, workspaceImageSelect+` WHERE workspace_id=? AND operation_digest=?`, workspaceID, op))
	if apperror.CodeOf(err) == apperror.CodeNotFound {
		return domain.WorkspaceImage{}, false, nil
	}
	return image, err == nil, err
}
