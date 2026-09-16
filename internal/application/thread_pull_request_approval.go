package application

import (
	"context"
	"encoding/json"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/gitmutation"
)

type threadPullRequestApprovalStore interface {
	GetRemoteOperation(context.Context, string) (gitmutation.RemoteRecord, bool, error)
	GetThreadByRun(context.Context, string) (domain.Thread, error)
	CheckThreadGitIdle(context.Context, string, string, *domain.RunExecutionLease) error
	DecideApproval(context.Context, approval.DecisionRequest) (approval.DecisionResult, error)
}

func recheckThreadPullRequestApproval(ctx context.Context, base ApprovalControlStore, a approval.Record) error {
	st, ok := base.(threadPullRequestApprovalStore)
	if !ok {
		return apperror.New(apperror.CodeFailedPrecondition, "task pull request approval source is unavailable")
	}
	row, found, err := st.GetRemoteOperation(ctx, a.ProposalID)
	if err != nil {
		return err
	}
	var p ThreadPullRequestPreview
	if !found || json.Unmarshal([]byte(row.SpecJSON), &p) != nil || p.Version != ThreadPullRequestVersion || !p.DraftOnly || p.OperationID != row.ID || p.RunID != row.RunID || p.WorkspaceID != row.WorkspaceID || p.Draft.Validate() != nil || row.Operation != gitmutation.RemoteCreatePR || row.StartedAt != nil || row.CompletedAt != nil || p.ApprovalFingerprint != row.RequestFingerprint || threadPRFingerprint(p) != row.RequestFingerprint || a.RunID != p.RunID || a.SessionID != p.SessionID || a.WorkspaceID != p.SourceWorkspaceID || a.ActionClass != "github_pull_request_create" || a.Mode != "per_call" || a.RequestFingerprint != p.ApprovalFingerprint {
		return apperror.New(apperror.CodeFailedPrecondition, "draft approval source changed or is no longer pending")
	}
	t, err := st.GetThreadByRun(ctx, p.RunID)
	if err != nil {
		return err
	}
	if t.ID != p.ThreadID || t.WorkspaceID != p.SourceWorkspaceID {
		return apperror.New(apperror.CodeConflict, "draft approval belongs to another task")
	}
	// No provider or model work occurs here. Creation subsequently rechecks the
	// live connection, permission, lease, exact local commit and remote branches.
	return st.CheckThreadGitIdle(ctx, p.ThreadID, p.RunID, nil)
}
