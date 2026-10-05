package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/executionauth"
	"cyberagent-workbench/internal/repository"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/toolcontract"
	"cyberagent-workbench/internal/workspace"
)

func (s *ThreadGitService) nativeAuthorityFingerprint(ctx context.Context, bound threadGitBinding, reviewing bool) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	runtime := s.capabilities.RuntimeAuthority
	if runtime == nil {
		return "", apperror.New(apperror.CodePolicyDenied, "task Git runtime authorization is unavailable")
	}
	run, err := s.store.GetRun(ctx, bound.run.ID)
	if err != nil {
		return "", err
	}
	mission, err := s.store.GetMission(ctx, run.MissionID)
	if err != nil {
		return "", err
	}
	files, err := resolveRunFileWorkspaceControl(ctx, s.store, run, mission, s.drydocks)
	if err != nil {
		return "", err
	}
	if files.Drydock != nil {
		if err := requireCurrentRunFileDrydock(ctx, s.store, run.ID, *files.Drydock); err != nil {
			return "", err
		}
	}
	if run.SessionID != bound.run.SessionID || mission.ID != bound.run.MissionID ||
		files.Workspace.ID != bound.public.WorkspaceID || files.Source.ID != bound.public.SourceWorkspaceID || files.Workspace.RootPath != bound.public.RootPath {
		return "", apperror.New(apperror.CodeConflict, "task Git directory or Run binding changed")
	}
	permission, err := s.store.GetRunExecutionPermission(ctx, run.ID)
	if err != nil {
		return "", err
	}
	mode, err := s.store.GetRunMode(ctx, run.ID)
	if err != nil {
		return "", err
	}
	profile, err := s.store.GetRunExecutionProfile(ctx, run.ID)
	if err != nil {
		return "", err
	}
	generation, live := s.capabilities.FullAccessGeneration(permission)
	if permission.Validate() != nil || permission.RunID != run.ID || permission.MissionID != mission.ID || !permission.Mode.IsApprovalMode() || !live {
		return "", apperror.New(apperror.CodePolicyDenied, "task Git permission is unavailable")
	}
	root, err := workspace.AgentCodeRootFingerprint(files.Workspace.RootPath)
	if err != nil {
		return "", err
	}
	var fence uint64
	if reviewing {
		fence, err = runtime.IssueRunAuthorizationFence(run.ID)
		if err != nil {
			return "", err
		}
	} else {
		var found bool
		fence, found = runtime.RunAuthorizationFence(run.ID)
		if !found {
			return "", apperror.New(apperror.CodePolicyDenied, "task Git review authority was revoked")
		}
	}
	drydockIdentity := []string(nil)
	if files.Drydock != nil {
		drydockIdentity = []string{files.Drydock.ID, files.Drydock.RunID, files.Drydock.SessionID, files.Drydock.RootFingerprint}
	}
	raw, err := json.Marshal(struct {
		ThreadID, RunID, MissionID, SessionID, WorkspaceID, SourceWorkspaceID, Root string
		Mode                                                                        domain.RunModeSnapshot
		Profile                                                                     domain.RunExecutionProfileSnapshot
		Permission                                                                  domain.RunExecutionPermissionSnapshot
		RuntimeEpoch                                                                string
		RuntimeFence, FullGeneration                                                uint64
		DrydockIdentity                                                             []string
	}{bound.thread.ID, run.ID, mission.ID, run.SessionID, files.Workspace.ID, files.Source.ID, root, mode, profile, permission, runtime.RuntimeEpoch(), fence, generation, drydockIdentity})
	if err != nil {
		return "", err
	}
	return runmutation.Fingerprint("native-thread-git-authority", string(raw)), nil
}

func threadGitApprovalMatches(proof approval.Record, bound threadGitBinding, id, fingerprint string) bool {
	return proof.ProposalID == id && proof.RunID == bound.run.ID && proof.SessionID == bound.run.SessionID &&
		proof.WorkspaceID == bound.public.WorkspaceID && proof.ToolName == "thread.git" && proof.ActionClass == "git_write" &&
		proof.Mode == "per_call" && proof.GrantID == "" && proof.RequestFingerprint == fingerprint
}

func threadGitNativeOperation(bound threadGitBinding, intent threadGitIntent, selected repository.SelectedGitReview, key string) (toolcontract.Operation, error) {
	switch intent.Spec.Operation {
	case "commit":
		if intent.CommitAuthor == nil {
			return toolcontract.Operation{}, errors.New("commit author was not reviewed")
		}
		return repository.SelectedCommitOperation(bound.public.RootPath, selected, intent.Spec.Message, key, *intent.CommitAuthor)
	case "stage", "unstage":
		return repository.SelectedIndexOperation(bound.public.RootPath, selected, intent.Spec.Operation == "unstage")
	case "create_branch", "switch_branch":
		return repository.ThreadBranchOperation(bound.public.RootPath, threadGitBranchSpec(intent), bound.repository, intent.TargetCommitOID)
	case "push_branch":
		if intent.Remote == nil {
			return toolcontract.Operation{}, errors.New("remote intent is missing")
		}
		return repository.ThreadRemoteOperation(bound.public.RootPath, *intent.Remote, threadGitRemoteBinding(bound, intent), key)
	default:
		return toolcontract.Operation{}, errors.New("unsupported native Git operation")
	}
}

func threadGitBranchSpec(intent threadGitIntent) repository.MutationSpec {
	return repository.MutationSpec{ProtocolVersion: repository.MutationProtocolVersion, Operation: repository.MutationOperation(intent.Spec.Operation), Branch: intent.Spec.Branch}
}

func threadGitRemoteBinding(bound threadGitBinding, intent threadGitIntent) repository.RemoteBinding {
	return repository.RemoteBinding{RunID: bound.run.ID, WorkspaceID: bound.public.WorkspaceID, LocalHead: bound.repository.Head, Branch: intent.Spec.Branch}
}

func (s *ThreadGitService) nativeDispatchGuard(ctx context.Context, bound threadGitBinding, intent threadGitIntent,
	selected repository.SelectedGitReview, id, key, requestFingerprint, encoded, approvalID string, lease domain.RunExecutionLease,
) (toolcontract.DispatchGuard, error) {
	operation, err := threadGitNativeOperation(bound, intent, selected, key)
	if err != nil {
		return nil, err
	}
	fingerprint, err := toolcontract.FingerprintOperation(operation)
	if err != nil {
		return nil, err
	}
	subject := executionauth.SubjectRef{RunID: bound.run.ID, ActorID: "native-thread-git-operator"}
	authorizer := executionauth.NewPolicyAuthorizer(func(checkCtx context.Context, actual executionauth.SubjectRef, _ toolcontract.Operation, approvalRef string) (executionauth.OperationAuthority, error) {
		if err := ctx.Err(); err != nil {
			return executionauth.OperationAuthority{}, err
		}
		if actual != subject || approvalRef != approvalID {
			return executionauth.OperationAuthority{}, errors.New("task Git subject or approval changed")
		}
		if err := s.requireLiveThreadGitAuthority(checkCtx, bound, lease); err != nil {
			return executionauth.OperationAuthority{}, err
		}
		if err := s.authority(checkCtx, bound, intent.Remote != nil, &lease); err != nil {
			return executionauth.OperationAuthority{}, err
		}
		current, err := s.nativeAuthorityFingerprint(checkCtx, bound, false)
		if err != nil {
			return executionauth.OperationAuthority{}, err
		}
		if current != intent.AuthorityFingerprint {
			return executionauth.OperationAuthority{}, errors.New("task Git authority changed after review")
		}
		if intent.Remote == nil {
			row, found, err := s.store.GetGitMutationRecord(checkCtx, id)
			if err != nil || !found || row.RequestFingerprint != requestFingerprint || row.SpecJSON != encoded || row.CompletedAt != nil {
				return executionauth.OperationAuthority{}, errors.New("task Git immutable local intent changed or completed")
			}
		} else {
			row, found, err := s.store.GetGitRemoteByKey(checkCtx, key)
			if err != nil || !found || row.ID != id || row.RequestFingerprint != requestFingerprint || row.SpecJSON != encoded || row.CompletedAt != nil {
				return executionauth.OperationAuthority{}, errors.New("task Git immutable remote intent changed or completed")
			}
		}
		proof, err := s.store.GetApprovalByProposal(checkCtx, id)
		if err != nil {
			return executionauth.OperationAuthority{}, err
		}
		if proof.ID != approvalRef || !threadGitApprovalMatches(proof, bound, id, requestFingerprint) {
			return executionauth.OperationAuthority{}, errors.New("task Git exact consent changed")
		}
		projection, err := domain.ExecutionPermissionApproval(bound.permission)
		if err != nil {
			return executionauth.OperationAuthority{}, err
		}
		binding := runmutation.Fingerprint("native-thread-git-dispatch", current, id, requestFingerprint, lease.LeaseID, lease.OwnerID, fmt.Sprint(lease.Generation))
		return executionauth.OperationAuthority{Mode: projection.Mode, BindingFingerprint: binding, RuntimeAvailable: true,
			FullActivated: projection.Mode == domain.ExecutionApprovalFull, EffectsVerified: false,
			Approval: &executionauth.BoundApproval{Ref: proof.ID, Subject: subject, OperationFingerprint: fingerprint, Status: string(proof.Status)}}, nil
	})
	decision, err := authorizer.Authorize(ctx, subject, operation, approvalID)
	if err != nil {
		return nil, err
	}
	if decision.Outcome != "allow" || decision.Validate() != nil {
		return nil, apperror.New(apperror.CodePolicyDenied, "task Git requires exact native confirmation")
	}
	return executionauth.NewRecheckingDispatchGuard(fingerprint, decision.BeforeDispatch, func(checkCtx context.Context) error {
		return authorizer.Recheck(checkCtx, subject, operation, approvalID, decision.AuthorizationRef)
	}, "task Git dispatch was denied or its inputs changed"), nil
}
