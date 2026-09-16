package application

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/gitadvanced"
	"cyberagent-workbench/internal/runmutation"
)

func threadWorktreeSpec(spec ThreadGitSpec, head string) gitadvanced.Spec {
	return gitadvanced.Spec{ProtocolVersion: gitadvanced.ProtocolVersion, Operation: gitadvanced.WorktreeCreate, WorktreeName: spec.WorktreeName, Branch: spec.Branch, Commit: head}
}

func (s *ThreadGitService) threadWorktreePreview(ctx context.Context, bound threadGitBinding, spec ThreadGitSpec) (gitadvanced.Preview, error) {
	if s.advanced == nil || s.advanced.executor == nil {
		return gitadvanced.Preview{}, apperror.New(apperror.CodeFailedPrecondition, "managed Git worktrees are unavailable")
	}
	// The existing advanced operation/approval ledger is source-Workspace scoped.
	// Never silently create a worktree from the source when this task executes
	// in a distinct Drydock. That requires an explicit directory decision.
	if bound.public.WorkspaceID != bound.public.SourceWorkspaceID {
		return gitadvanced.Preview{}, apperror.New(apperror.CodeFailedPrecondition, "managed worktree creation is available for a task working directly in its source repository")
	}
	return s.advanced.executor.ReviewAdvanced(ctx, bound.public.RootPath, threadWorktreeSpec(spec, bound.repository.Head))
}

func (s *ThreadGitService) executeWorktree(ctx context.Context, bound threadGitBinding, request ThreadGitExecuteRequest, lease domain.RunExecutionLease) (ThreadGitResult, error) {
	if _, err := s.threadWorktreePreview(ctx, bound, request.Spec); err != nil {
		return ThreadGitResult{}, err
	}
	service := *s.advanced
	service.operatorThreadID = bound.thread.ID
	checkpoints := *service.checkpoints
	checkpoints.operatorGitThreadID = bound.thread.ID
	service.checkpoints = &checkpoints
	scope := GitAdvancedScope{CapabilityGeneration: service.executor.Capability().Generation, LeaseID: lease.LeaseID, LeaseGeneration: lease.Generation}
	key := threadGitKey(bound.thread.ID, request.OperationKey)
	review, err := service.Review(ctx, GitAdvancedReviewRequest{ProtocolVersion: GitAdvancedAPIProtocolVersion, RunID: bound.run.ID, OperationKey: key, RequestedBy: request.RequestedBy, Scope: scope, Spec: threadWorktreeSpec(request.Spec, bound.repository.Head)})
	if err != nil {
		return ThreadGitResult{}, err
	}
	if review.Operation == nil || review.Approval == nil || !review.Preview.Executable() {
		return ThreadGitResult{}, apperror.New(apperror.CodeFailedPrecondition, "managed worktree preview is not executable")
	}
	result := ThreadGitResult{Version: ThreadGitProtocolVersion, ThreadID: bound.thread.ID, RunID: bound.run.ID, WorkspaceID: bound.public.WorkspaceID, OperationID: review.Operation.ID, Spec: &request.Spec, State: "unknown", Branch: request.Spec.Branch}
	decision, err := s.store.DecideApproval(ctx, approval.DecisionRequest{ProposalID: review.Operation.ID, IdempotencyKey: approval.ReviewIdempotencyKey(gitadvanced.ApprovalToolName, review.Operation.ID, approval.ActionApprove), Action: approval.ActionApprove, ReviewedBy: request.RequestedBy})
	if err != nil {
		return result, err
	}
	operationCtx, cancel := s.monitorAuthority(ctx, bound, lease, false)
	defer cancel()
	executed, err := service.Execute(operationCtx, GitAdvancedExecuteRequest{ProtocolVersion: GitAdvancedAPIProtocolVersion, RunID: bound.run.ID, OperationID: review.Operation.ID, ApprovalID: decision.Approval.ID, RequestedBy: request.RequestedBy, Scope: scope})
	if err != nil {
		result.Reason = "managed worktree result is not confirmed; inspect this original request"
		return result, nil
	}
	if executed.Operation.Status != gitadvanced.OperationSucceeded || executed.Worktree == nil {
		result.Reason = "managed worktree creation did not complete"
		return result, nil
	}
	worktree, found, readErr := service.store.GetManagedGitWorktreeByName(ctx, executed.Operation.CommonDirSHA256, request.Spec.WorktreeName)
	if readErr != nil || !found || !threadGitWorktreeMatches(worktree, executed.Operation) {
		result.Reason = "managed worktree receipt requires read-only inspection"
		return result, nil
	}
	result.State = "completed"
	result.ReceiptSaved = true
	result.CommitOID = worktree.Head
	result.WorktreePath = worktree.Path
	result.CompletedAt = executed.Operation.CompletedAt
	return result, nil
}

func (s *ThreadGitService) replayWorktree(ctx context.Context, threadID, key string, request *ThreadGitExecuteRequest) (ThreadGitResult, bool, error) {
	var result ThreadGitResult
	if s.advanced == nil {
		return result, false, nil
	}
	id := "thread-git-worktree-" + threadGitKey(threadID, key)
	record, found, err := s.advanced.store.GetGitAdvancedOperation(ctx, id)
	if err != nil || !found {
		return result, found, err
	}
	thread, err := s.store.GetThreadByRun(ctx, record.RunID)
	if err != nil || thread.ID != threadID {
		return result, true, apperror.New(apperror.CodeConflict, "managed worktree request belongs to another task")
	}
	var preview gitadvanced.Preview
	if json.Unmarshal([]byte(record.PreviewJSON), &preview) != nil || record.Operation != gitadvanced.WorktreeCreate {
		return result, true, apperror.New(apperror.CodeConflict, "stored managed worktree intent is invalid")
	}
	spec := ThreadGitSpec{Operation: "worktree_create", Branch: preview.Spec.Branch, WorktreeName: preview.Spec.WorktreeName}
	if request != nil {
		requested, _ := json.Marshal(request.Spec)
		stored, _ := json.Marshal(spec)
		specJSON, _ := json.Marshal(spec)
		fingerprint := runmutation.Fingerprint(ThreadGitProtocolVersion, threadID, record.RunID, record.SessionID, record.WorkspaceID, preview.Binding.Fingerprint(), record.PermissionSnapshotID, fmt.Sprint(record.PermissionRevision), string(specJSON), "null", preview.Target, "")
		approve, approvalErr := s.store.GetApprovalByProposal(ctx, id)
		if request.RunID != record.RunID || string(requested) != string(stored) || request.ExpectedPreviewFingerprint != fingerprint || approvalErr != nil || request.RequestedBy != approve.RequestedBy {
			return result, true, apperror.New(apperror.CodeConflict, "managed worktree key was used for a different request")
		}
	}
	result = ThreadGitResult{Version: ThreadGitProtocolVersion, ThreadID: threadID, RunID: record.RunID, WorkspaceID: record.WorkspaceID, OperationID: id, State: "unknown", Spec: &spec, Replayed: true, Branch: spec.Branch, CompletedAt: record.CompletedAt}
	if record.Status == gitadvanced.OperationSucceeded {
		worktree, present, err := s.advanced.store.GetManagedGitWorktreeByName(ctx, record.CommonDirSHA256, spec.WorktreeName)
		if err == nil && present && threadGitWorktreeMatches(worktree, record) {
			result.State = "completed"
			result.ReceiptSaved = true
			result.WorktreePath = worktree.Path
			result.CommitOID = worktree.Head
			return result, true, nil
		}
	}
	result.Reason = "managed worktree result is unavailable; no creation was repeated"
	if record.Status.Terminal() {
		result.Reason = "managed worktree operation ended: " + strings.TrimSpace(string(record.ErrorCode))
	}
	return result, true, nil
}

func threadGitWorktreeMatches(worktree gitadvanced.ManagedWorktree, record gitadvanced.OperationRecord) bool {
	return worktree.CreatedOperationID == record.ID && worktree.RunID == record.RunID &&
		worktree.WorkspaceID == record.WorkspaceID && worktree.CommonDirSHA256 == record.CommonDirSHA256 &&
		worktree.RepositorySHA256 == record.RepositorySHA256 && worktree.Path != "" &&
		worktree.PathSHA256 == gitadvanced.Fingerprint("managed-worktree-path", worktree.Path)
}
