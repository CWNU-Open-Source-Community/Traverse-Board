package application

import (
	"context"
	"encoding/json"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/executionauth"
	"cyberagent-workbench/internal/fileedit"
	"cyberagent-workbench/internal/session"
	"cyberagent-workbench/internal/toolcontract"
	"cyberagent-workbench/internal/workspace"
)

// fileEditDispatchCheck binds the existing prepared apply transaction to the
// common authorizer. It consumes one dispatch grant before the first mutation,
// then rechecks that same authority before later native publication/removal.
// No new approval or Run/Session ledger is created by this bridge.
func (s *FileEditApplyService) fileEditDispatchCheck(ctx context.Context,
	prepared fileedit.ApplyOperation, binding fileEditApplyBinding, request ApplyFileEditRequest,
) (func() error, error) {
	subject := executionauth.SubjectRef{RunID: prepared.RunID, ActorID: request.AppliedBy}
	rootFingerprint, err := workspace.AgentCodeRootFingerprint(binding.workspace.RootPath)
	if err != nil {
		return nil, apperror.Normalize(err)
	}
	operation, err := fileEditOperation(prepared, binding.edit, rootFingerprint)
	if err != nil {
		return nil, err
	}
	fingerprint, err := toolcontract.FingerprintOperation(operation)
	if err != nil {
		return nil, apperror.Normalize(err)
	}
	authorizer := executionauth.NewPolicyAuthorizer(func(checkCtx context.Context,
		actualSubject executionauth.SubjectRef, _ toolcontract.Operation, approvalRef string,
	) (executionauth.OperationAuthority, error) {
		current, err := s.loadOperationBinding(checkCtx, prepared)
		if err != nil {
			return executionauth.OperationAuthority{}, err
		}
		currentRoot, err := workspace.AgentCodeRootFingerprint(current.workspace.RootPath)
		if err != nil {
			return executionauth.OperationAuthority{}, apperror.Normalize(err)
		}
		if actualSubject != subject || current.run.Status != domain.RunRunning ||
			current.session.Status != session.StatusActive ||
			current.edit.Status != fileedit.StatusApproved ||
			current.workspace.RootPath != binding.workspace.RootPath || currentRoot != rootFingerprint ||
			current.approval.ID != approvalRef || current.approval.RequestFingerprint !=
			fileedit.ApprovalFingerprint(current.edit.SessionID, current.edit.WorkspaceID, current.edit) {
			return executionauth.OperationAuthority{}, apperror.New(apperror.CodePolicyDenied,
				"FileEdit execution authority changed before filesystem mutation")
		}
		finalOperation, err := fileEditOperation(prepared, current.edit, currentRoot)
		if err != nil {
			return executionauth.OperationAuthority{}, err
		}
		finalFingerprint, err := toolcontract.FingerprintOperation(finalOperation)
		if err != nil || finalFingerprint != fingerprint {
			return executionauth.OperationAuthority{}, apperror.New(apperror.CodeConflict,
				"FileEdit final inputs changed before filesystem mutation")
		}
		if err := s.checkCurrentPolicy(checkCtx, current); err != nil {
			return executionauth.OperationAuthority{}, err
		}
		if err := s.checkAutomaticFileEditAuthorization(checkCtx, current, request); err != nil {
			return executionauth.OperationAuthority{}, err
		}
		leases, ok := s.store.(RunExecutionLeaseStore)
		if !ok {
			return executionauth.OperationAuthority{}, apperror.New(apperror.CodeFailedPrecondition,
				"FileEdit execution lease reader is unavailable")
		}
		lease, found, err := leases.GetRunExecutionLease(checkCtx, prepared.RunID)
		if err != nil {
			return executionauth.OperationAuthority{}, apperror.Normalize(err)
		}
		if !found || !lease.ActiveAt(s.now().UTC()) || lease.LeaseID != request.LeaseID ||
			lease.Generation != request.LeaseGeneration {
			return executionauth.OperationAuthority{}, apperror.New(apperror.CodePolicyDenied,
				"FileEdit execution lease changed before filesystem mutation")
		}
		// Legacy reviewed proposals stay per-operation approvals. This does not
		// map the old five modes onto new runtime capabilities or activate full.
		// Automatic legacy rows still require their exact original live grant.
		raw, err := json.Marshal(struct {
			Root     string
			Request  ApplyFileEditRequest
			Approval approval.Record
		}{currentRoot, request, current.approval})
		if err != nil {
			return executionauth.OperationAuthority{}, err
		}
		return executionauth.OperationAuthority{
			Mode: domain.ExecutionApprovalAsk, BindingFingerprint: fileedit.HashText(string(raw)),
			RuntimeAvailable: true, EffectsVerified: true,
			Approval: &executionauth.BoundApproval{Ref: current.approval.ID, Subject: subject,
				OperationFingerprint: finalFingerprint, Status: string(current.approval.Status)},
		}, nil
	})
	var decision executionauth.Decision
	started := false
	return func() error {
		// The manager rechecks the proposal's actual hashes and rooted paths on
		// each invocation. Reload the immutable native transaction too, rather
		// than trusting a stale application-level permission check.
		current, err := s.loadOperationBinding(ctx, prepared)
		if err != nil {
			return err
		}
		actual, err := fileEditOperation(prepared, current.edit, rootFingerprint)
		if err != nil {
			return err
		}
		actualFingerprint, err := toolcontract.FingerprintOperation(actual)
		if err != nil {
			return err
		}
		if !started {
			started = true
			// Defer activation until an actual mutation is needed. A manager
			// recovering already-published bytes must remain a read-only replay.
			decision, err = authorizer.Authorize(ctx, subject, operation, binding.approval.ID)
			if err != nil {
				return err
			}
			if decision.Outcome != "allow" || decision.Validate() != nil {
				return apperror.New(apperror.CodePolicyDenied, "FileEdit operation was not authorized")
			}
			return decision.BeforeDispatch(ctx, actualFingerprint)
		}
		return authorizer.Recheck(ctx, subject, actual, binding.approval.ID, decision.AuthorizationRef)
	}, nil
}

func fileEditOperation(prepared fileedit.ApplyOperation, edit fileedit.Edit,
	rootFingerprint string,
) (toolcontract.Operation, error) {
	raw, err := json.Marshal(struct {
		Prepared fileedit.ApplyOperation
		Root     string
	}{prepared, rootFingerprint})
	if err != nil {
		return toolcontract.Operation{}, err
	}
	effects := []toolcontract.Effect{toolcontract.EffectReversibleWrite}
	if edit.Operation == fileedit.OperationDelete {
		effects = []toolcontract.Effect{toolcontract.EffectDestructive}
	}
	targets := []toolcontract.Target{{Kind: "file", Locator: edit.WorkspaceID + ":" + edit.Path}}
	if edit.DestinationPath != "" {
		targets = append(targets, toolcontract.Target{Kind: "file", Locator: edit.WorkspaceID + ":" + edit.DestinationPath})
	}
	operation := toolcontract.Operation{ID: "file-edit:" + prepared.KeyDigest,
		Kind: toolcontract.OperationFileWrite, ToolID: fileedit.ApprovalToolName(edit),
		Component: toolcontract.ComponentRef{PackageID: "traverse-board", ComponentID: "workspace-files"},
		AdapterID: "host-rooted-files", AdapterRevision: "1",
		InputFingerprint: fileedit.HashText(string(raw)), Targets: targets, Effects: effects}
	return operation, operation.Validate()
}
