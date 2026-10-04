package application

import (
	"context"
	"errors"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/domain"
)

type commandBoundedApprovalStore interface {
	commandApprovalStore
	GetCommandApprovalGrantScope(context.Context, string) (approval.GrantQuery, error)
	FindActiveSessionGrant(context.Context, approval.GrantQuery) (approval.SessionGrant, bool, error)
	CreateSessionGrant(context.Context, approval.CreateGrantRequest) (approval.GrantResult, error)
	ListSessionGrants(context.Context, approval.GrantListFilter) ([]approval.SessionGrant, error)
	GetSessionGrant(context.Context, string) (approval.SessionGrant, error)
	AuthorizeCommandApprovalWithSessionGrant(context.Context, approval.DecisionRequest, string) (approval.DecisionResult, error)
	CheckCommandApprovalGrant(context.Context, string) error
}

func (s *ApprovalControlService) decideBoundedCommand(ctx context.Context, run domain.Run, record approval.Record, request DecideApprovalControlRequest) (DecideApprovalControlResult, error) {
	var result DecideApprovalControlResult
	st, ok := s.store.(commandBoundedApprovalStore)
	if !ok || request.Action != ApprovalControlApproveForRun || record.Status == approval.StatusDenied || (record.Status == approval.StatusApproved && record.GrantID == "") {
		return result, apperror.New(apperror.CodeFailedPrecondition, "this exact command does not have the requested bounded approval")
	}
	var grant approval.SessionGrant
	grantCreated := false
	var err error
	if record.Status == approval.StatusApproved {
		// Replaying a completed decision does not refresh the grant or its TTL.
		// The native continuation separately checks live authority before effects.
		grant, err = st.GetSessionGrant(ctx, record.GrantID)
		if err != nil {
			return result, apperror.Normalize(err)
		}
		if !commandGrantLimitsMatch(grant, request) || grant.RunID != run.ID || grant.SessionID != record.SessionID || grant.ToolName != record.ToolName || grant.ActionClass != record.ActionClass {
			return result, apperror.New(apperror.CodeConflict, "bounded command replay changed the original grant or limits")
		}
	} else {
		if run.Terminal() {
			return result, apperror.New(apperror.CodeFailedPrecondition, "terminal Run approvals cannot be changed")
		}
		if err := s.recheckApprovalSource(ctx, record); err != nil {
			return result, err
		}
		query, err := st.GetCommandApprovalGrantScope(ctx, record.ProposalID)
		if err != nil {
			return result, apperror.Normalize(err)
		}
		// A recent-history page is not an authority lookup: an older active
		// scope can remain valid after more than 500 other grants are written.
		existing, found, err := st.FindActiveSessionGrant(ctx, query)
		if err != nil {
			return result, apperror.Normalize(err)
		}
		if found {
			if !commandGrantLimitsMatch(existing, request) {
				return result, apperror.New(apperror.CodeConflict, "active bounded command scope has different limits")
			}
			grant = existing
		}
		if grant.ID == "" {
			grants, err := st.ListSessionGrants(ctx, approval.GrantListFilter{RunID: run.ID, ToolName: record.ToolName, Limit: 500})
			if err != nil {
				return result, apperror.Normalize(err)
			}
			generation := int64(1)
			for _, previous := range grants {
				if previous.Generation >= generation {
					generation = previous.Generation + 1
				}
			}
			created, err := st.CreateSessionGrant(ctx, approval.CreateGrantRequest{
				SessionID: query.SessionID, WorkspaceID: query.WorkspaceID, ToolName: query.ToolName, ActionClass: query.ActionClass,
				Reason: request.Reason, GrantedBy: request.ReviewedBy, IdempotencyKey: "command-grant:" + request.OperationKey,
				ScopeFingerprint: query.ScopeFingerprint, Generation: generation, MaxUses: request.GrantMaxUses, TTL: time.Duration(request.GrantTTLSeconds) * time.Second,
				ModeSnapshotID: query.ModeSnapshotID, ModeRevision: query.ModeRevision, InteractionSnapshotID: query.InteractionSnapshotID, InteractionRevision: query.InteractionRevision,
				ExecutionProfileSnapshotID: query.ExecutionProfileSnapshotID, ExecutionProfileRevision: query.ExecutionProfileRevision,
				PermissionSnapshotID: query.PermissionSnapshotID, PermissionRevision: query.PermissionRevision, PermissionMode: query.PermissionMode,
				WorkspaceRootFingerprint: query.WorkspaceRootFingerprint, CapabilityGeneration: query.CapabilityGeneration})
			if err != nil {
				return result, apperror.Normalize(err)
			}
			grant = created.Grant
			grantCreated = !created.Replayed
		}
	}
	decision, err := st.AuthorizeCommandApprovalWithSessionGrant(ctx, approval.DecisionRequest{ProposalID: record.ProposalID, IdempotencyKey: request.OperationKey, Action: approval.ActionApprove, ReviewedBy: request.ReviewedBy}, grant.ID)
	if err != nil {
		return result, apperror.Normalize(err)
	}
	grant, err = st.GetSessionGrant(ctx, grant.ID)
	if err != nil {
		return result, apperror.Normalize(err)
	}
	if decision.Approval.ID != record.ID || decision.Approval.Status != approval.StatusApproved || decision.Approval.GrantID != grant.ID || decision.Consumption == nil || decision.Consumption.ApprovalID != record.ID || decision.Consumption.GrantID != grant.ID {
		return result, apperror.New(apperror.CodeInternal, "bounded approval lost its exact consumption")
	}
	return DecideApprovalControlResult{Approval: decision.Approval, Action: request.Action, Replayed: decision.Replayed, Grant: &grant, Consumption: decision.Consumption, GrantCreated: grantCreated}, nil
}

func commandGrantLimitsMatch(grant approval.SessionGrant, request DecideApprovalControlRequest) bool {
	return grant.Validate() == nil && grant.Bounded() && grant.ExpiresAt != nil && grant.MaxUses == request.GrantMaxUses &&
		grant.ExpiresAt.Sub(grant.CreatedAt) == time.Duration(request.GrantTTLSeconds)*time.Second
}

func checkCommandGrant(ctx context.Context, base any, record approval.Record) error {
	if record.GrantID == "" {
		return nil
	}
	st, ok := base.(interface {
		CheckCommandApprovalGrant(context.Context, string) error
	})
	if !ok {
		return errors.New("bounded command grant authority reader is unavailable")
	}
	if err := st.CheckCommandApprovalGrant(ctx, record.ProposalID); err != nil {
		return apperror.Wrap(apperror.CodePolicyDenied, "bounded command grant no longer authorizes execution", err)
	}
	return nil
}
