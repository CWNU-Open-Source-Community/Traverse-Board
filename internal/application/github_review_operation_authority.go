package application

import (
	"context"
	"encoding/json"
	"errors"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/executionauth"
	"cyberagent-workbench/internal/githubreview"
	"cyberagent-workbench/internal/toolcontract"
	"cyberagent-workbench/internal/workspace"
)

// The existing approval digest binds host authority as well as the exact native
// write. The preview retains its independent content digest, and old receipts
// remain readable. Only an explicit review can issue a new runtime fence.
func (s *GitHubReviewService) writeApprovalFingerprint(ctx context.Context,
	value githubReviewRunBinding, connection githubreview.Connection,
	spec githubreview.WriteSpec, preview githubreview.WritePreview, reviewing bool,
) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	runtime := s.permissionCapabilities.RuntimeAuthority
	if runtime == nil || !connection.Enabled || !connection.Network.WriteEnabled || connection.Validate() != nil {
		return "", apperror.New(apperror.CodePolicyDenied, "GitHub review runtime or network authority is unavailable")
	}
	generation, live := s.permissionCapabilities.FullAccessGeneration(value.permission)
	if !live {
		return "", apperror.New(apperror.CodePolicyDenied, "GitHub review runtime permission is inactive")
	}
	root, err := workspace.AgentCodeRootFingerprint(value.workspace.RootPath)
	if err != nil {
		return "", err
	}
	op, err := githubreview.ReviewWriteOperation(spec, preview)
	if err != nil {
		return "", err
	}
	fingerprint, err := toolcontract.FingerprintOperation(op)
	if err != nil {
		return "", err
	}
	var fence uint64
	if reviewing {
		fence, err = runtime.IssueRunAuthorizationFence(value.run.ID)
		if err != nil {
			return "", err
		}
	} else {
		var found bool
		fence, found = runtime.RunAuthorizationFence(value.run.ID)
		if !found {
			return "", apperror.New(apperror.CodePolicyDenied, "GitHub review authority was revoked")
		}
	}
	raw, err := json.Marshal(struct {
		RunID, MissionID, SessionID, WorkspaceID, Root, Operation string
		Mode                                                      domain.RunModeSnapshot
		Permission                                                domain.RunExecutionPermissionSnapshot
		Connection                                                githubreview.Connection
		RuntimeEpoch                                              string
		RuntimeFence, FullGeneration                              uint64
	}{value.run.ID, value.mission.ID, value.session.ID, value.workspace.ID, root, fingerprint,
		value.mode, value.permission, connection, runtime.RuntimeEpoch(), fence, generation})
	if err != nil {
		return "", err
	}
	return githubreview.Fingerprint("native-github-review-approval.v2", string(raw)), nil
}

func githubReviewApprovalMatches(record githubreview.WriteRecord, proof approval.Record) bool {
	return proof.ProposalID == record.ID && proof.RunID == record.RunID &&
		proof.SessionID == record.SessionID && proof.WorkspaceID == record.WorkspaceID &&
		proof.ToolName == githubreview.ApprovalToolName && proof.ActionClass == githubreview.ApprovalActionClass &&
		proof.Mode == "per_call" && proof.GrantID == "" && proof.RequestFingerprint == record.ApprovalFingerprint
}

func (s *GitHubReviewService) writeDispatchGuard(ctx context.Context, record githubreview.WriteRecord,
	approvalID string,
) (toolcontract.DispatchGuard, error) {
	op, err := githubreview.ReviewWriteOperation(record.Spec, record.Preview)
	if err != nil {
		return nil, err
	}
	fingerprint, err := toolcontract.FingerprintOperation(op)
	if err != nil {
		return nil, err
	}
	subject := executionauth.SubjectRef{RunID: record.RunID, ActorID: "native-github-review-operator"}
	authorizer := executionauth.NewPolicyAuthorizer(func(checkCtx context.Context, actual executionauth.SubjectRef,
		_ toolcontract.Operation, approvalRef string,
	) (executionauth.OperationAuthority, error) {
		if err := ctx.Err(); err != nil {
			return executionauth.OperationAuthority{}, err
		}
		if actual != subject || approvalRef != approvalID {
			return executionauth.OperationAuthority{}, errors.New("GitHub review subject or approval changed")
		}
		current, err := s.loadRunBinding(checkCtx, record.RunID, true)
		if err != nil {
			return executionauth.OperationAuthority{}, err
		}
		connection, found, err := s.store.GetGitHubReviewConnection(checkCtx, record.ConnectionID)
		if err != nil {
			return executionauth.OperationAuthority{}, err
		}
		if !found || current.session.ID != record.SessionID || current.workspace.ID != record.WorkspaceID {
			return executionauth.OperationAuthority{}, errors.New("GitHub review native binding changed")
		}
		binding, err := s.writeApprovalFingerprint(checkCtx, current, connection, record.Spec, record.Preview, false)
		if err != nil {
			return executionauth.OperationAuthority{}, err
		}
		if binding != record.ApprovalFingerprint {
			return executionauth.OperationAuthority{}, apperror.New(apperror.CodeConflict, "GitHub review authority changed after review")
		}
		stored, found, err := s.store.GetGitHubReviewWrite(checkCtx, record.ID)
		if err != nil {
			return executionauth.OperationAuthority{}, err
		}
		if !found || stored.RequestFingerprint != record.RequestFingerprint || stored.ApprovalFingerprint != record.ApprovalFingerprint ||
			stored.RunID != record.RunID || stored.SessionID != record.SessionID || stored.WorkspaceID != record.WorkspaceID ||
			stored.ConnectionID != record.ConnectionID || (stored.ApprovalID != "" && stored.ApprovalID != approvalID) ||
			(stored.Status != githubreview.OperationProposed && stored.Status != githubreview.OperationRunning) {
			return executionauth.OperationAuthority{}, errors.New("GitHub review immutable intent changed or already completed")
		}
		storedOp, err := githubreview.ReviewWriteOperation(stored.Spec, stored.Preview)
		if err != nil {
			return executionauth.OperationAuthority{}, err
		}
		storedFingerprint, err := toolcontract.FingerprintOperation(storedOp)
		if err != nil || storedFingerprint != fingerprint {
			return executionauth.OperationAuthority{}, errors.New("GitHub review stored write changed")
		}
		proof, err := s.store.GetApproval(checkCtx, approvalRef)
		if err != nil {
			return executionauth.OperationAuthority{}, err
		}
		if !githubReviewApprovalMatches(stored, proof) {
			return executionauth.OperationAuthority{}, errors.New("GitHub review exact one-time approval changed")
		}
		projection, err := domain.ExecutionPermissionApproval(current.permission)
		if err != nil {
			return executionauth.OperationAuthority{}, err
		}
		return executionauth.OperationAuthority{Mode: projection.Mode, BindingFingerprint: binding,
			RuntimeAvailable: true, FullActivated: projection.Mode == domain.ExecutionApprovalFull,
			EffectsVerified: false, Approval: &executionauth.BoundApproval{Ref: proof.ID, Subject: subject,
				OperationFingerprint: fingerprint, Status: string(proof.Status)}}, nil
	})
	decision, err := authorizer.Authorize(ctx, subject, op, approvalID)
	if err != nil {
		return nil, err
	}
	if decision.Outcome != "allow" || decision.Validate() != nil {
		return nil, apperror.New(apperror.CodePolicyDenied, "GitHub review requires exact operator approval")
	}
	return executionauth.NewRecheckingDispatchGuard(fingerprint, decision.BeforeDispatch, func(checkCtx context.Context) error {
		return authorizer.Recheck(checkCtx, subject, op, approvalID, decision.AuthorizationRef)
	}, "GitHub review dispatch was denied or its inputs changed"), nil
}
