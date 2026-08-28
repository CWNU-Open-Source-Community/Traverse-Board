package application

import (
	"context"
	"fmt"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/runmutation"
)

type ThreadExecutionPermissionStore interface {
	GetThread(context.Context, string) (domain.Thread, error)
	GetThreadExecutionPermission(context.Context,
		string) (domain.ThreadExecutionPermissionSnapshot, error)
	GetRunExecutionPermission(context.Context,
		string) (domain.RunExecutionPermissionSnapshot, error)
	GetThreadExecutionPermissionSnapshot(context.Context,
		string) (domain.ThreadExecutionPermissionSnapshot, error)
	GetThreadExecutionPermissionOperation(context.Context,
		string) (domain.ThreadExecutionPermissionOperation, bool, error)
	TransitionThreadExecutionPermission(context.Context,
		domain.ThreadExecutionPermissionSnapshot,
		domain.ThreadExecutionPermissionOperation) (
		domain.ThreadExecutionPermissionSnapshot,
		domain.ThreadExecutionPermissionOperation, bool, error)
}

type ThreadExecutionPermissionService struct {
	store        ThreadExecutionPermissionStore
	capabilities domain.ExecutionPermissionRuntimeCapabilities
}

type ChangeThreadExecutionPermissionRequest struct {
	ThreadID                string
	Mode                    string
	OperationKey            string
	RequestedBy             string
	Reason                  string
	ConfirmWorkspaceAccess  bool
	ConfirmUserApproval     bool
	ConfirmDangerFullAccess bool
	ConfirmDebugAccess      bool
}

type ChangeThreadExecutionPermissionResult struct {
	Permission       domain.ThreadExecutionPermissionSnapshot
	CurrentRunID     string
	CurrentRunEffect domain.ThreadExecutionPermissionCurrentRunEffect
	Replayed         bool
}

type CurrentThreadExecutionPermissionResult struct {
	Permission             domain.ThreadExecutionPermissionSnapshot
	CurrentRunID           string
	CurrentRunMode         domain.RunExecutionPermissionMode
	CurrentRunSynchronized bool
}

func NewThreadExecutionPermissionService(store ThreadExecutionPermissionStore,
	capabilities domain.ExecutionPermissionRuntimeCapabilities,
) *ThreadExecutionPermissionService {
	return &ThreadExecutionPermissionService{store: store, capabilities: capabilities}
}

func (s *ThreadExecutionPermissionService) Current(ctx context.Context,
	threadID string,
) (domain.ThreadExecutionPermissionSnapshot, error) {
	if s == nil || s.store == nil {
		return domain.ThreadExecutionPermissionSnapshot{}, apperror.New(
			apperror.CodeFailedPrecondition, "Thread execution permission store is required")
	}
	threadID = strings.TrimSpace(threadID)
	if !domain.ValidAgentID(threadID) || strings.ContainsRune(threadID, 0) {
		return domain.ThreadExecutionPermissionSnapshot{}, apperror.New(
			apperror.CodeInvalidArgument, "Thread execution permission Thread id is invalid")
	}
	permission, err := s.store.GetThreadExecutionPermission(ctx, threadID)
	return permission, apperror.Normalize(err)
}

func (s *ThreadExecutionPermissionService) Inspect(ctx context.Context,
	threadID string,
) (CurrentThreadExecutionPermissionResult, error) {
	permission, err := s.Current(ctx, threadID)
	if err != nil {
		return CurrentThreadExecutionPermissionResult{}, err
	}
	threadRecord, err := s.store.GetThread(ctx, permission.ThreadID)
	if err != nil {
		return CurrentThreadExecutionPermissionResult{}, apperror.Normalize(err)
	}
	result := CurrentThreadExecutionPermissionResult{Permission: permission,
		CurrentRunID: threadRecord.ActiveRunID}
	if threadRecord.ActiveRunID == "" {
		return result, nil
	}
	runPermission, err := s.store.GetRunExecutionPermission(ctx, threadRecord.ActiveRunID)
	if err != nil {
		return CurrentThreadExecutionPermissionResult{}, apperror.Normalize(err)
	}
	result.CurrentRunMode = runPermission.Mode
	result.CurrentRunSynchronized = runPermission.Mode == permission.Mode &&
		runPermission.PolicyVersion == permission.PolicyVersion &&
		runPermission.ApprovalPolicy == permission.ApprovalPolicy &&
		runPermission.CommandScope == permission.CommandScope &&
		runPermission.FilesystemScope == permission.FilesystemScope &&
		runPermission.NetworkScope == permission.NetworkScope &&
		runPermission.PersistentTerminal == permission.PersistentTerminal &&
		runPermission.BackgroundProcess == permission.BackgroundProcess &&
		runPermission.AgentTerminalInput == permission.AgentTerminalInput &&
		runPermission.RiskTier == permission.RiskTier &&
		runPermission.RequiredGate == permission.RequiredGate &&
		!runPermission.ProcessEnabled && !runPermission.ExecutionAuthorized &&
		!runPermission.CapabilityGrant
	return result, nil
}

func (s *ThreadExecutionPermissionService) Change(ctx context.Context,
	request ChangeThreadExecutionPermissionRequest,
) (ChangeThreadExecutionPermissionResult, error) {
	if s == nil || s.store == nil {
		return ChangeThreadExecutionPermissionResult{}, apperror.New(
			apperror.CodeFailedPrecondition, "Thread execution permission store is required")
	}
	if err := s.capabilities.Validate(); err != nil {
		return ChangeThreadExecutionPermissionResult{}, apperror.Wrap(
			apperror.CodeFailedPrecondition,
			"Thread execution permission runtime capabilities are invalid", err)
	}
	normalized, target, confirmed, err :=
		normalizeChangeThreadExecutionPermissionRequest(request)
	if err != nil {
		return ChangeThreadExecutionPermissionResult{}, apperror.Wrap(
			apperror.CodeInvalidArgument, err.Error(), err)
	}
	if !s.capabilities.Allows(target) {
		return ChangeThreadExecutionPermissionResult{}, apperror.New(
			apperror.CodePolicyDenied,
			fmt.Sprintf(
				"Thread execution permission %s is unavailable because this process lacks gate %s",
				target, requiredExecutionPermissionGate(target)))
	}
	keyDigest := runmutation.Fingerprint(
		"thread_execution_permission_operation.v1", normalized.ThreadID,
		normalized.OperationKey)
	requestFingerprint := runmutation.Fingerprint(
		"thread_execution_permission_change_request.v1", normalized.ThreadID,
		string(target), fmt.Sprintf("%t", confirmed), normalized.RequestedBy,
		normalized.Reason)
	if replay, found, err := s.loadReplay(ctx, keyDigest, requestFingerprint,
		normalized.ThreadID, normalized.RequestedBy, target); err != nil {
		return ChangeThreadExecutionPermissionResult{}, err
	} else if found {
		return replay, nil
	}
	threadRecord, err := s.store.GetThread(ctx, normalized.ThreadID)
	if err != nil {
		return ChangeThreadExecutionPermissionResult{}, apperror.Normalize(err)
	}
	if threadRecord.Status != domain.ThreadActive {
		return ChangeThreadExecutionPermissionResult{}, apperror.New(
			apperror.CodeFailedPrecondition,
			"Thread execution permission can only change while the Thread is active")
	}
	current, err := s.store.GetThreadExecutionPermission(ctx, threadRecord.ID)
	if err != nil {
		return ChangeThreadExecutionPermissionResult{}, apperror.Normalize(err)
	}
	now := time.Now().UTC()
	if now.Before(current.CreatedAt) {
		now = current.CreatedAt
	}
	next, err := current.Next(idgen.New("thread-exec-permission"), target, confirmed,
		normalized.RequestedBy, normalized.Reason, now)
	if err != nil {
		return ChangeThreadExecutionPermissionResult{}, apperror.Wrap(
			apperror.CodeInvalidArgument,
			"Thread execution permission transition is invalid", err)
	}
	operation := domain.ThreadExecutionPermissionOperation{
		KeyDigest: keyDigest, RequestFingerprint: requestFingerprint,
		SnapshotID: next.ID, ThreadID: next.ThreadID, RequestedBy: next.RequestedBy,
		CreatedAt: next.CreatedAt,
	}
	stored, storedOperation, replayed, err := s.store.TransitionThreadExecutionPermission(
		ctx, next, operation)
	return ChangeThreadExecutionPermissionResult{
		Permission: stored, CurrentRunID: storedOperation.CurrentRunID,
		CurrentRunEffect: storedOperation.CurrentRunEffect, Replayed: replayed,
	}, apperror.Normalize(err)
}

func (s *ThreadExecutionPermissionService) loadReplay(ctx context.Context,
	keyDigest, requestFingerprint, threadID, requestedBy string,
	target domain.RunExecutionPermissionMode,
) (ChangeThreadExecutionPermissionResult, bool, error) {
	existing, found, err := s.store.GetThreadExecutionPermissionOperation(ctx, keyDigest)
	if err != nil {
		return ChangeThreadExecutionPermissionResult{}, false, apperror.Normalize(err)
	}
	if !found {
		return ChangeThreadExecutionPermissionResult{}, false, nil
	}
	if existing.RequestFingerprint != requestFingerprint ||
		existing.ThreadID != threadID || existing.RequestedBy != requestedBy {
		return ChangeThreadExecutionPermissionResult{}, true, apperror.New(
			apperror.CodeConflict,
			"Thread execution permission operation key was already used for different intent")
	}
	stored, err := s.store.GetThreadExecutionPermissionSnapshot(ctx, existing.SnapshotID)
	if err != nil {
		return ChangeThreadExecutionPermissionResult{}, true, apperror.Normalize(err)
	}
	if stored.ID != existing.SnapshotID || stored.ThreadID != existing.ThreadID ||
		stored.RequestedBy != existing.RequestedBy ||
		!stored.CreatedAt.Equal(existing.CreatedAt) || stored.Mode != target ||
		stored.ProcessEnabled || stored.ExecutionAuthorized || stored.CapabilityGrant {
		return ChangeThreadExecutionPermissionResult{}, true, apperror.New(
			apperror.CodeInternal,
			"stored Thread execution permission operation binding is invalid")
	}
	return ChangeThreadExecutionPermissionResult{
		Permission: stored, CurrentRunID: existing.CurrentRunID,
		CurrentRunEffect: existing.CurrentRunEffect, Replayed: true,
	}, true, nil
}

func normalizeChangeThreadExecutionPermissionRequest(
	request ChangeThreadExecutionPermissionRequest,
) (ChangeThreadExecutionPermissionRequest, domain.RunExecutionPermissionMode, bool, error) {
	// Reuse the Run selector's exact actor, reason, operation-key, confirmation,
	// and mode validation. Thread preferences intentionally have the same risk
	// acknowledgement and process-local runtime gates as Run selections.
	normalized, mode, confirmed, err := normalizeChangeRunExecutionPermissionRequest(
		ChangeRunExecutionPermissionRequest{
			RunID: request.ThreadID, Mode: request.Mode,
			OperationKey: request.OperationKey, RequestedBy: request.RequestedBy,
			Reason:                  request.Reason,
			ConfirmWorkspaceAccess:  request.ConfirmWorkspaceAccess,
			ConfirmUserApproval:     request.ConfirmUserApproval,
			ConfirmDangerFullAccess: request.ConfirmDangerFullAccess,
			ConfirmDebugAccess:      request.ConfirmDebugAccess,
		})
	if err != nil {
		return ChangeThreadExecutionPermissionRequest{}, "", false, err
	}
	request.ThreadID = normalized.RunID
	request.Mode = normalized.Mode
	request.OperationKey = normalized.OperationKey
	request.RequestedBy = normalized.RequestedBy
	request.Reason = normalized.Reason
	return request, mode, confirmed, nil
}
