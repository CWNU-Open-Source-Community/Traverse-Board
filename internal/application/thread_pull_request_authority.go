package application

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/executionauth"
	"cyberagent-workbench/internal/githubreview"
	"cyberagent-workbench/internal/gitmutation"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/toolcontract"
	"cyberagent-workbench/internal/workspace"
)

// Only the host's digest is added to the existing immutable intent JSON. The
// public preview and common Operation need no lease or internal authority fields.
// Old records omit this field and remain readable with their original digest.
type threadPRStoredIntent struct {
	ThreadPullRequestPreview
	AuthorityFingerprint string `json:"native_authority_fingerprint,omitempty"`
}

func decodeThreadPRIntent(raw string) (ThreadPullRequestPreview, string, error) {
	var stored threadPRStoredIntent
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return stored.ThreadPullRequestPreview, "", err
	}
	if stored.AuthorityFingerprint != "" {
		decoded, err := hex.DecodeString(stored.AuthorityFingerprint)
		if err != nil || len(stored.AuthorityFingerprint) != 64 || len(decoded) != 32 {
			return stored.ThreadPullRequestPreview, "", errors.New("stored draft authority digest is invalid")
		}
	}
	return stored.ThreadPullRequestPreview, stored.AuthorityFingerprint, nil
}

func threadPRMatchesContext(p ThreadPullRequestPreview, bound ThreadGitContext) bool {
	return bound.ThreadID == p.ThreadID && bound.RunID == p.RunID && bound.SessionID == p.SessionID &&
		bound.WorkspaceID == p.WorkspaceID && bound.SourceWorkspaceID == p.SourceWorkspaceID &&
		bound.HeadSHA == p.Draft.HeadSHA && bound.Branch == p.Draft.HeadBranch && bound.StatusFingerprint == p.BindingFingerprint
}

func threadPRApprovalMatches(p ThreadPullRequestPreview, proof approval.Record) bool {
	return proof.ProposalID == p.OperationID && proof.RunID == p.RunID && proof.SessionID == p.SessionID &&
		proof.WorkspaceID == p.SourceWorkspaceID && proof.ToolName == ThreadPullRequestApprovalTool &&
		proof.ActionClass == "github_pull_request_create" && proof.Mode == "per_call" && proof.GrantID == "" &&
		proof.RequestFingerprint == p.ApprovalFingerprint
}

func (s *ThreadPullRequestService) reviewAuthorityFingerprint(ctx context.Context,
	bound ThreadGitContext, reviewing bool,
) (string, domain.RunExecutionPermissionSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return "", domain.RunExecutionPermissionSnapshot{}, err
	}
	native, err := s.review.loadRunBinding(ctx, bound.RunID, false)
	if err != nil {
		return "", native.permission, err
	}
	permission, err := s.review.store.GetRunExecutionPermission(ctx, bound.RunID)
	if err != nil {
		return "", permission, err
	}
	if !permission.Mode.IsApprovalMode() {
		return "", permission, nil
	}
	caps := s.review.permissionCapabilities
	if permission.Validate() != nil || permission.RunID != native.run.ID || permission.MissionID != native.mission.ID ||
		!caps.OperatorApprovalEnabled || caps.RuntimeAuthority == nil {
		return "", permission, apperror.New(apperror.CodePolicyDenied, "draft runtime authorization is unavailable")
	}
	generation, live := caps.FullAccessGeneration(permission)
	if !live {
		return "", permission, apperror.New(apperror.CodePolicyDenied, "draft runtime permission is inactive")
	}
	root, err := workspace.AgentCodeRootFingerprint(bound.RootPath)
	if err != nil {
		return "", permission, err
	}
	var fence uint64
	if reviewing {
		fence, err = caps.RuntimeAuthority.IssueRunAuthorizationFence(bound.RunID)
		if err != nil {
			return "", permission, err
		}
	} else {
		var found bool
		fence, found = caps.RuntimeAuthority.RunAuthorizationFence(bound.RunID)
		if !found {
			return "", permission, apperror.New(apperror.CodePolicyDenied, "draft review authority was revoked")
		}
	}
	raw, err := json.Marshal(struct {
		ThreadID, RunID, MissionID, SessionID, SourceWorkspaceID, WorkspaceID, Root string
		Mode                                                                        domain.RunModeSnapshot
		Permission                                                                  domain.RunExecutionPermissionSnapshot
		RuntimeEpoch                                                                string
		RuntimeFence, FullGeneration                                                uint64
	}{bound.ThreadID, native.run.ID, native.mission.ID, native.session.ID, native.workspace.ID, bound.WorkspaceID, root,
		native.mode, permission, caps.RuntimeAuthority.RuntimeEpoch(), fence, generation})
	if err != nil {
		return "", permission, err
	}
	return runmutation.Fingerprint("native-thread-pr-authority", string(raw)), permission, nil
}

func (s *ThreadPullRequestService) dispatchGuard(ctx context.Context, p ThreadPullRequestPreview,
	row gitmutation.RemoteRecord, approvalID string, lease domain.RunExecutionLease,
) (toolcontract.DispatchGuard, error) {
	_, expectedAuthority, err := decodeThreadPRIntent(row.SpecJSON)
	if err != nil || expectedAuthority == "" {
		return nil, apperror.New(apperror.CodePolicyDenied, "a fresh native draft review is required")
	}
	op, err := githubreview.DraftOperation(p.Draft)
	if err != nil {
		return nil, err
	}
	fingerprint, err := toolcontract.FingerprintOperation(op)
	if err != nil {
		return nil, err
	}
	subject := executionauth.SubjectRef{RunID: p.RunID, ActorID: "native-thread-pr-operator"}
	authorizer := executionauth.NewPolicyAuthorizer(func(checkCtx context.Context, actual executionauth.SubjectRef,
		_ toolcontract.Operation, approvalRef string,
	) (executionauth.OperationAuthority, error) {
		if err := ctx.Err(); err != nil {
			return executionauth.OperationAuthority{}, err
		}
		if actual != subject || approvalRef != approvalID {
			return executionauth.OperationAuthority{}, errors.New("draft subject or approval changed")
		}
		bound, err := s.git.CaptureThreadGitContext(checkCtx, p.ThreadID)
		if err != nil {
			return executionauth.OperationAuthority{}, err
		}
		if !threadPRMatchesContext(p, bound) {
			return executionauth.OperationAuthority{}, apperror.New(apperror.CodeConflict, "reviewed draft Git binding changed")
		}
		if err := s.authority(checkCtx, bound, &lease); err != nil {
			return executionauth.OperationAuthority{}, err
		}
		current, permission, err := s.reviewAuthorityFingerprint(checkCtx, bound, false)
		if err != nil {
			return executionauth.OperationAuthority{}, err
		}
		if current != expectedAuthority {
			return executionauth.OperationAuthority{}, apperror.New(apperror.CodeConflict, "draft native authority changed after review")
		}
		connection, found, err := s.review.store.GetGitHubReviewConnection(checkCtx, p.ConnectionID)
		if err != nil {
			return executionauth.OperationAuthority{}, err
		}
		if !found || !connection.Enabled || !connection.Network.WriteEnabled || connection.Generation != p.ConnectionGeneration ||
			connection.Repository != p.Draft.Repository || connection.Credential != p.Draft.Credential {
			return executionauth.OperationAuthority{}, apperror.New(apperror.CodeConflict, "draft connection authority changed")
		}
		if err := requireThreadPRRepository(bound, connection.Repository); err != nil {
			return executionauth.OperationAuthority{}, err
		}
		stored, found, err := s.store.GetRemoteOperation(checkCtx, row.ID)
		if err != nil {
			return executionauth.OperationAuthority{}, err
		}
		if !found || stored.SpecJSON != row.SpecJSON || stored.RequestFingerprint != row.RequestFingerprint || stored.CompletedAt != nil {
			return executionauth.OperationAuthority{}, errors.New("draft immutable intent changed or completed")
		}
		proof, err := s.store.GetApproval(checkCtx, approvalRef)
		if err != nil {
			return executionauth.OperationAuthority{}, err
		}
		if !threadPRApprovalMatches(p, proof) {
			return executionauth.OperationAuthority{}, errors.New("draft exact one-time approval changed")
		}
		projection, err := domain.ExecutionPermissionApproval(permission)
		if err != nil {
			return executionauth.OperationAuthority{}, err
		}
		binding := runmutation.Fingerprint("native-thread-pr-dispatch", current, lease.LeaseID, lease.OwnerID,
			strconv.FormatInt(lease.Generation, 10), row.RequestFingerprint)
		return executionauth.OperationAuthority{Mode: projection.Mode, BindingFingerprint: binding, RuntimeAvailable: true,
			FullActivated: projection.Mode == domain.ExecutionApprovalFull, EffectsVerified: false,
			Approval: &executionauth.BoundApproval{Ref: proof.ID, Subject: subject, OperationFingerprint: fingerprint, Status: string(proof.Status)}}, nil
	})
	decision, err := authorizer.Authorize(ctx, subject, op, approvalID)
	if err != nil {
		return nil, err
	}
	if decision.Outcome != "allow" || decision.Validate() != nil {
		return nil, apperror.New(apperror.CodePolicyDenied, "draft creation requires exact operator approval")
	}
	return executionauth.NewRecheckingDispatchGuard(fingerprint, decision.BeforeDispatch, func(checkCtx context.Context) error {
		return authorizer.Recheck(checkCtx, subject, op, approvalID, decision.AuthorizationRef)
	}, "draft dispatch was denied or its inputs changed"), nil
}
