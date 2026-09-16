package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/executionauth"
	"cyberagent-workbench/internal/gitadvanced"
	"cyberagent-workbench/internal/gitmutation"
	"cyberagent-workbench/internal/repository"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/session"
	"cyberagent-workbench/internal/workspacecheckpoint"
)

type ThreadGitStore interface {
	GitMutationStore
	GitRemoteStore
	RunFileWorkspaceStore
	RunExecutionLeaseStore
	GetThread(context.Context, string) (domain.Thread, error)
	GetThreadByRun(context.Context, string) (domain.Thread, error)
	GetRunMode(context.Context, string) (domain.RunModeSnapshot, error)
	GetRunExecutionProfile(context.Context, string) (domain.RunExecutionProfileSnapshot, error)
	GetRunExecutionPermission(context.Context, string) (domain.RunExecutionPermissionSnapshot, error)
	GetGitMutationByKey(context.Context, string) (gitmutation.Record, bool, error)
	GetGitRemoteByKey(context.Context, string) (gitmutation.RemoteRecord, bool, error)
	StartGitMutationOperation(context.Context, string, string, time.Time) (gitmutation.Record, bool, error)
	StartRemoteOperation(context.Context, string, string, time.Time) (gitmutation.RemoteRecord, bool, error)
	EnsureApproval(context.Context, approval.Proposal) (approval.Record, error)
	GetApprovalByProposal(context.Context, string) (approval.Record, error)
	DecideApproval(context.Context, approval.DecisionRequest) (approval.DecisionResult, error)
	CheckThreadGitIdle(context.Context, string, string, *domain.RunExecutionLease) error
}

type ThreadGitService struct {
	store        ThreadGitStore
	local        *repository.MutationExecutor
	remote       *repository.RemoteExecutor
	checkpoints  *WorkspaceCheckpointService
	drydocks     *DrydockService
	capabilities domain.ExecutionPermissionRuntimeCapabilities
	advanced     *GitAdvancedService
}

func (s *ThreadGitService) WithAdvanced(advanced *GitAdvancedService) *ThreadGitService {
	s.advanced = advanced
	return s
}

func NewThreadGitService(store ThreadGitStore, local *repository.MutationExecutor, remote *repository.RemoteExecutor, checkpoints *WorkspaceCheckpointService, drydocks *DrydockService, capabilities domain.ExecutionPermissionRuntimeCapabilities) *ThreadGitService {
	return &ThreadGitService{store: store, local: local, remote: remote, checkpoints: checkpoints, drydocks: drydocks, capabilities: capabilities}
}

type threadGitBinding struct {
	public     ThreadGitContext
	thread     domain.Thread
	run        domain.Run
	permission domain.RunExecutionPermissionSnapshot
	repository gitadvanced.RepositoryBinding
}

func (s *ThreadGitService) bind(ctx context.Context, threadID string) (threadGitBinding, error) {
	var value threadGitBinding
	if s == nil || s.store == nil || s.local == nil || !s.local.Available() {
		return value, apperror.New(apperror.CodeFailedPrecondition, "task Git is unavailable")
	}
	var err error
	value.thread, err = s.store.GetThread(ctx, threadID)
	if err != nil {
		return value, apperror.Normalize(err)
	}
	runID := value.thread.ActiveRunID
	if runID == "" {
		runID = value.thread.LastRunID
	}
	value.run, err = s.store.GetRun(ctx, runID)
	if err != nil {
		return value, apperror.Normalize(err)
	}
	mission, err := s.store.GetMission(ctx, value.run.MissionID)
	if err != nil {
		return value, apperror.Normalize(err)
	}
	files, err := ResolveRunFileWorkspace(ctx, s.store, value.run, mission, s.drydocks)
	if err != nil {
		return value, err
	}
	if files.Drydock != nil {
		if err = requireCurrentRunFileDrydock(ctx, s.store, runID, *files.Drydock); err != nil {
			return value, err
		}
	}
	value.repository, err = s.local.AdvancedBinding(ctx, files.Workspace.RootPath)
	if err != nil {
		return value, apperror.Normalize(err)
	}
	value.permission, err = s.store.GetRunExecutionPermission(ctx, runID)
	if err != nil {
		return value, apperror.Normalize(err)
	}
	_, remotes, err := s.local.ReadThreadGitMetadata(ctx, files.Workspace.RootPath)
	if err != nil {
		return value, err
	}
	value.public = ThreadGitContext{ThreadID: threadID, RunID: runID, SessionID: value.run.SessionID, WorkspaceID: files.Workspace.ID, SourceWorkspaceID: files.Source.ID, RootPath: files.Workspace.RootPath, HeadSHA: value.repository.Head, Branch: value.repository.Branch, StatusFingerprint: value.repository.Fingerprint(), RemoteURLs: []ThreadGitRemote{}}
	names := []string{}
	for name := range remotes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		parsed, err := url.Parse(remotes[name])
		if err == nil && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == "" && (parsed.Scheme == "https" || parsed.Scheme == "file") {
			value.public.RemoteURLs = append(value.public.RemoteURLs, ThreadGitRemote{Name: name, URL: remotes[name]})
		} else {
			value.public.RemoteURLs = append(value.public.RemoteURLs, ThreadGitRemote{Name: name, BlockedReason: "此远端地址无法用于当前推送接口；SSH 或包含凭据的地址不受支持"})
		}
	}
	return value, nil
}

func (s *ThreadGitService) CaptureThreadGitContext(ctx context.Context, threadID string) (ThreadGitContext, error) {
	value, err := s.bind(ctx, threadID)
	return value.public, err
}

func (s *ThreadGitService) authority(ctx context.Context, value threadGitBinding, network bool, lease *domain.RunExecutionLease) error {
	if value.thread.Status != domain.ThreadActive || value.thread.ActiveRunID != value.run.ID || value.run.Terminal() {
		return apperror.New(apperror.CodeFailedPrecondition, "continue this task before a new Git operation")
	}
	mode, err := s.store.GetRunMode(ctx, value.run.ID)
	if err != nil {
		return err
	}
	profile, err := s.store.GetRunExecutionProfile(ctx, value.run.ID)
	if err != nil {
		return err
	}
	if mode.Surface != domain.ExecutionSurfaceCode || mode.Phase != domain.ExecutionPhaseDeliver || profile.Profile != domain.RunExecutionProfileLocal {
		return apperror.New(apperror.CodeFailedPrecondition, "Git writes require this task's Code/Deliver and Local execution settings")
	}
	decision, err := executionauth.EvaluateExecutionPermission(value.permission, s.capabilities, executionauth.PermissionRequest{Kind: executionauth.PermissionOperationStatelessCommand, HostFilesystem: true, Network: network, OperatorApproved: true})
	if err != nil {
		return err
	}
	if !decision.Allowed || !decision.HostFilesystem || (network && !decision.Network) {
		return apperror.New(apperror.CodePolicyDenied, "current task permission does not authorize this explicitly confirmed Git operation")
	}
	return s.store.CheckThreadGitIdle(ctx, value.thread.ID, value.run.ID, lease)
}

func (s *ThreadGitService) State(ctx context.Context, threadID string) (ThreadGitState, error) {
	bound, err := s.bind(ctx, threadID)
	if err != nil {
		return ThreadGitState{}, err
	}
	branches, _, err := s.local.ReadThreadGitMetadata(ctx, bound.public.RootPath)
	if err != nil {
		return ThreadGitState{}, err
	}
	state, err := s.local.InspectThreadGitState(ctx, bound.public.RootPath, bound.public.WorkspaceID)
	if err != nil {
		return ThreadGitState{}, err
	}
	value := ThreadGitState{Version: ThreadGitProtocolVersion, ThreadGitContext: bound.public, Branches: branches, Changes: state.Changes, Truncated: state.Truncated, CanExecute: true}
	if err = s.authority(ctx, bound, false, nil); err != nil {
		value.CanExecute = false
		value.BlockedReason = err.Error()
	}
	return value, nil
}

func normalizeThreadGitSpec(spec ThreadGitSpec) (ThreadGitSpec, error) {
	if spec.Operation != "worktree_create" && spec.WorktreeName != "" {
		return spec, apperror.New(apperror.CodeInvalidArgument, "only managed worktree creation accepts a name")
	}
	switch spec.Operation {
	case "stage", "unstage", "commit":
		if len(spec.Paths) == 0 || len(spec.Paths) > repository.MaxMutationPaths || spec.Branch != "" || spec.RemoteURL != "" || spec.CredentialName != "" {
			return spec, apperror.New(apperror.CodeInvalidArgument, "selected file operation has invalid fields")
		}
		if spec.Operation == "commit" {
			if strings.TrimSpace(spec.Message) == "" || len([]rune(spec.Message)) > repository.MaxMutationMessageRunes {
				return spec, apperror.New(apperror.CodeInvalidArgument, "commit requires a bounded message")
			}
		} else if spec.Message != "" {
			return spec, apperror.New(apperror.CodeInvalidArgument, "only commit accepts a message")
		}
		spec.Paths = append([]string{}, spec.Paths...)
		sort.Strings(spec.Paths)
		for i, path := range spec.Paths {
			if path == "" || (i > 0 && spec.Paths[i-1] == path) {
				return spec, apperror.New(apperror.CodeInvalidArgument, "select distinct exact file paths")
			}
		}
	case "create_branch", "switch_branch", "worktree_create":
		if spec.Branch == "" || len(spec.Paths) != 0 || spec.Message != "" || spec.RemoteURL != "" || spec.CredentialName != "" {
			return spec, apperror.New(apperror.CodeInvalidArgument, "branch operation has invalid fields")
		}
		if spec.Operation == "worktree_create" && spec.WorktreeName == "" {
			return spec, apperror.New(apperror.CodeInvalidArgument, "managed worktree requires a name")
		}
	case "push_branch":
		if spec.Branch == "" || spec.RemoteURL == "" || len(spec.Paths) != 0 || spec.Message != "" {
			return spec, apperror.New(apperror.CodeInvalidArgument, "push requires an exact remote and branch")
		}
	default:
		return spec, apperror.New(apperror.CodeInvalidArgument, "unsupported task Git operation")
	}
	return spec, nil
}

func threadGitRemoteSpec(spec ThreadGitSpec) repository.RemoteSpec {
	return repository.RemoteSpec{ProtocolVersion: repository.RemoteProtocolVersion, Operation: repository.RemotePushBranch, RemoteURL: spec.RemoteURL, Branch: spec.Branch, CredentialName: spec.CredentialName, NetworkTTLMillis: (2 * time.Minute).Milliseconds()}
}

func (s *ThreadGitService) preview(ctx context.Context, bound threadGitBinding, spec ThreadGitSpec, lease *domain.RunExecutionLease) (ThreadGitPreview, repository.SelectedGitReview, error) {
	value := ThreadGitPreview{Version: ThreadGitProtocolVersion, ThreadGitContext: bound.public, Spec: spec, CanExecute: true}
	var selected repository.SelectedGitReview
	if spec.Operation == "switch_branch" && bound.public.WorkspaceID != bound.public.SourceWorkspaceID {
		value.CanExecute = false
		value.BlockedReason = "任务隔离目录绑定自己的分支，不能切换该目录的分支"
		return value, selected, nil
	}
	if err := s.authority(ctx, bound, spec.Operation == "push_branch", lease); err != nil {
		value.CanExecute = false
		value.BlockedReason = err.Error()
		return value, selected, nil
	}
	var err error
	if len(spec.Paths) > 0 {
		if spec.Operation == "stage" || spec.Operation == "unstage" {
			selected, err = s.local.ReviewIndexChange(ctx, bound.public.RootPath, spec.Paths, spec.Operation == "unstage")
		} else {
			selected, err = s.local.ReviewSelected(ctx, bound.public.RootPath, spec.Paths)
		}
		if err != nil {
			return value, selected, err
		}
		if !selected.Binding.SameState(bound.repository) {
			return value, selected, apperror.New(apperror.CodeConflict, "repository changed during review")
		}
		value.Diff = selected.Diff
		if spec.Operation == "commit" {
			author, authorErr := s.local.ReadCommitAuthor(ctx, bound.public.RootPath)
			if authorErr != nil {
				value.CanExecute = false
				value.BlockedReason = authorErr.Error()
				return value, selected, nil
			}
			value.CommitAuthor = &author
		}
	} else if spec.Operation == "push_branch" {
		if s.remote == nil || !s.remote.Available() || bound.repository.Head == "unborn" || bound.repository.Branch == "" {
			return value, selected, apperror.New(apperror.CodeFailedPrecondition, "push requires a current branch and commit")
		}
		remote := threadGitRemoteSpec(spec)
		if err = s.remote.ValidateSpec(remote); err != nil {
			return value, selected, apperror.Wrap(apperror.CodeInvalidArgument, "remote target is invalid", err)
		}
		oid, err := s.remote.ReadRemoteOID(ctx, bound.public.RootPath, remote)
		if err != nil {
			return value, selected, err
		}
		if oid == "" {
			oid = "missing"
		}
		value.ExpectedRemoteOID = oid
	} else if spec.Operation == "worktree_create" {
		advanced, err := s.threadWorktreePreview(ctx, bound, spec)
		if err != nil {
			return value, selected, err
		}
		value.Diff = advanced.Summary + "\nCreates a separate managed directory; the current task working directory is unchanged."
		if !advanced.Executable() {
			value.CanExecute = false
			value.BlockedReason = strings.Join(advanced.BlockedReasons, "; ")
		}
		value.ExpectedRemoteOID = advanced.Target // internal stable target included below, not a remote ref.
	} else {
		value.TargetCommitOID = bound.repository.Head
		if spec.Operation == "create_branch" {
			if existing, readErr := s.local.ReadBranchTarget(ctx, bound.public.RootPath, spec.Branch); readErr == nil && existing != "" {
				value.CanExecute = false
				value.BlockedReason = "target branch already exists"
			} else if apperror.CodeOf(readErr) == apperror.CodeInvalidArgument {
				return value, selected, readErr
			}
		}
		if spec.Operation == "switch_branch" {
			value.TargetCommitOID, err = s.local.ReadBranchTarget(ctx, bound.public.RootPath, spec.Branch)
			if err != nil {
				return value, selected, err
			}
			state, err := s.local.InspectThreadGitState(ctx, bound.public.RootPath, bound.public.WorkspaceID)
			if err != nil {
				return value, selected, err
			}
			if !state.Clean {
				value.CanExecute = false
				value.BlockedReason = "switching branches requires a clean working tree and index"
			}
		}
	}
	specJSON, _ := json.Marshal(spec)
	filesJSON, _ := json.Marshal(selected.Files)
	value.PreviewFingerprint = runmutation.Fingerprint(ThreadGitProtocolVersion, bound.thread.ID, bound.run.ID, bound.run.SessionID, bound.public.WorkspaceID, bound.repository.Fingerprint(), bound.permission.ID, fmt.Sprint(bound.permission.Revision), string(specJSON), string(filesJSON), value.ExpectedRemoteOID, value.TargetCommitOID)
	if value.CommitAuthor != nil {
		value.PreviewFingerprint = runmutation.Fingerprint("thread_git_commit_author", value.PreviewFingerprint, value.CommitAuthor.Name, value.CommitAuthor.Email)
	}
	if spec.Operation == "worktree_create" {
		value.ExpectedRemoteOID = ""
	}
	return value, selected, nil
}

func (s *ThreadGitService) Preview(ctx context.Context, threadID string, request ThreadGitPreviewRequest) (ThreadGitPreview, error) {
	if request.Version != ThreadGitProtocolVersion {
		return ThreadGitPreview{}, apperror.New(apperror.CodeInvalidArgument, "task Git version is invalid")
	}
	spec, err := normalizeThreadGitSpec(request.Spec)
	if err != nil {
		return ThreadGitPreview{}, err
	}
	bound, err := s.bind(ctx, threadID)
	if err != nil {
		return ThreadGitPreview{}, err
	}
	if request.RunID != bound.run.ID {
		return ThreadGitPreview{}, apperror.New(apperror.CodeConflict, "task execution changed")
	}
	value, _, err := s.preview(ctx, bound, spec, nil)
	return value, err
}

type threadGitIntent struct {
	Version            string                             `json:"version"`
	ThreadID           string                             `json:"thread_id"`
	SessionID          string                             `json:"session_id"`
	WorkspaceID        string                             `json:"workspace_id"`
	RootPath           string                             `json:"root_path"`
	Spec               ThreadGitSpec                      `json:"spec"`
	PreviewFingerprint string                             `json:"preview_fingerprint"`
	Binding            gitadvanced.RepositoryBinding      `json:"binding"`
	Files              []repository.SelectedGitFile       `json:"files,omitempty"`
	Commit             *repository.PreparedSelectedCommit `json:"commit,omitempty"`
	Remote             *repository.RemoteSpec             `json:"remote,omitempty"`
	RequestedBy        string                             `json:"requested_by"`
	TargetCommitOID    string                             `json:"target_commit_oid,omitempty"`
}

func threadGitKey(threadID, key string) string {
	return runmutation.OperationKeyDigest("thread_git_operation.v1", threadID, key)
}

func (s *ThreadGitService) Execute(ctx context.Context, threadID string, request ThreadGitExecuteRequest) (ThreadGitResult, error) {
	if request.Version != ThreadGitProtocolVersion || request.RunID == "" || strings.TrimSpace(request.OperationKey) == "" || len(request.OperationKey) > 256 || !gitadvanced.ValidDigest(request.ExpectedPreviewFingerprint) || request.RequestedBy == "" || len(request.RequestedBy) > 128 {
		return ThreadGitResult{}, apperror.New(apperror.CodeInvalidArgument, "task Git execution identity is invalid")
	}
	spec, err := normalizeThreadGitSpec(request.Spec)
	if err != nil {
		return ThreadGitResult{}, err
	}
	request.Spec = spec
	if result, found, err := s.replay(ctx, threadID, request.OperationKey, &request); err != nil || found {
		return result, err
	}
	if result, found, err := s.replayWorktree(ctx, threadID, request.OperationKey, &request); err != nil || found {
		return result, err
	}
	bound, err := s.bind(ctx, threadID)
	if err != nil {
		return ThreadGitResult{}, err
	}
	if bound.run.ID != request.RunID {
		return ThreadGitResult{}, apperror.New(apperror.CodeConflict, "task execution changed")
	}
	if err = s.authority(ctx, bound, spec.Operation == "push_branch", nil); err != nil {
		return ThreadGitResult{}, err
	}
	var result ThreadGitResult
	err = withRunExecutionLease(ctx, s.store, bound.run.ID, "thread-git:"+threadGitKey(threadID, request.OperationKey), RunExecutionLeasePolicy{TTL: time.Minute, RenewInterval: 10 * time.Second}, func(operationCtx context.Context, lease domain.RunExecutionLease) error {
		fresh, err := s.bind(operationCtx, threadID)
		if err != nil {
			return err
		}
		preview, selected, err := s.preview(operationCtx, fresh, spec, &lease)
		if err != nil {
			return err
		}
		if !preview.CanExecute || preview.PreviewFingerprint != request.ExpectedPreviewFingerprint {
			return apperror.New(apperror.CodeConflict, "Git preview changed; review the selected operation again")
		}
		if spec.Operation == "worktree_create" {
			result, err = s.executeWorktree(operationCtx, fresh, request, lease)
		} else {
			result, err = s.executeOnce(operationCtx, fresh, preview, selected, request, lease)
		}
		return err
	})
	return result, err
}

func (s *ThreadGitService) executeOnce(ctx context.Context, bound threadGitBinding, preview ThreadGitPreview, selected repository.SelectedGitReview, request ThreadGitExecuteRequest, lease domain.RunExecutionLease) (ThreadGitResult, error) {
	key := threadGitKey(bound.thread.ID, request.OperationKey)
	intent := threadGitIntent{Version: ThreadGitProtocolVersion, ThreadID: bound.thread.ID, SessionID: bound.run.SessionID, WorkspaceID: bound.public.WorkspaceID, RootPath: bound.public.RootPath, Spec: request.Spec, PreviewFingerprint: preview.PreviewFingerprint, Binding: bound.repository, Files: selected.Files, RequestedBy: request.RequestedBy, TargetCommitOID: preview.TargetCommitOID}
	var prepared *repository.PreparedSelectedCommit
	var err error
	if request.Spec.Operation == "commit" {
		if preview.CommitAuthor == nil {
			return ThreadGitResult{}, apperror.New(apperror.CodeFailedPrecondition, "Git 提交身份尚未审阅")
		}
		prepared, err = s.local.PrepareSelectedCommit(ctx, bound.public.RootPath, selected, request.Spec.Message, key, *preview.CommitAuthor)
		if err != nil {
			return ThreadGitResult{}, err
		}
		defer prepared.Close()
		intent.Commit = prepared
	}
	if request.Spec.Operation == "stage" || request.Spec.Operation == "unstage" {
		prepared, err = s.local.PrepareSelectedIndex(ctx, bound.public.RootPath, selected, request.Spec.Operation == "unstage")
		if err != nil {
			return ThreadGitResult{}, err
		}
		defer prepared.Close()
	}
	if request.Spec.Operation == "push_branch" {
		remote := threadGitRemoteSpec(request.Spec)
		remote.CommitOID = bound.repository.Head
		remote.ExpectedRemoteOID = preview.ExpectedRemoteOID
		intent.Remote = &remote
	}
	encoded, err := json.Marshal(intent)
	if err != nil {
		return ThreadGitResult{}, err
	}
	if len(encoded) > 32768 {
		return ThreadGitResult{}, apperror.New(apperror.CodeResourceExhausted, "selected Git intent exceeds its storage bound")
	}
	id := "thread-git-" + key
	fingerprint := runmutation.Fingerprint("thread_git_request.v1", bound.thread.ID, bound.run.ID, preview.PreviewFingerprint, request.RequestedBy)
	if intent.Remote != nil {
		parsed, _ := url.Parse(intent.Remote.RemoteURL)
		host := parsed.Hostname()
		port := parsed.Port()
		if port == "" {
			port = "443"
		}
		if host == "" {
			host = "local-fixture"
		}
		_, replayed, err := s.store.CreateRemoteOperation(ctx, gitmutation.RemoteRecord{ID: id, ProtocolVersion: repository.RemoteProtocolVersion, OperationKeyDigest: key, RequestFingerprint: fingerprint, RunID: bound.run.ID, WorkspaceID: bound.public.WorkspaceID, Operation: gitmutation.RemotePushBranch, SpecJSON: string(encoded), RemoteHost: host, RemotePort: port, Protocol: parsed.Scheme, Branch: intent.Spec.Branch, PreHead: bound.repository.Head, CreatedAt: time.Now().UTC()})
		if err != nil {
			return ThreadGitResult{}, err
		}
		if replayed {
			return s.Observe(ctx, bound.thread.ID, request.OperationKey)
		}
	} else {
		_, replayed, err := s.store.CreateGitMutationOperation(ctx, gitmutation.Record{ID: id, ProtocolVersion: gitmutation.ProtocolVersion, OperationKeyDigest: key, RequestFingerprint: fingerprint, RunID: bound.run.ID, WorkspaceID: bound.public.WorkspaceID, Operation: gitmutation.Operation(intent.Spec.Operation), SpecJSON: string(encoded), PreHead: bound.repository.Head, CreatedAt: time.Now().UTC()})
		if err != nil {
			return ThreadGitResult{}, err
		}
		if replayed {
			return s.Observe(ctx, bound.thread.ID, request.OperationKey)
		}
	}
	result := ThreadGitResult{Version: ThreadGitProtocolVersion, ThreadID: bound.thread.ID, RunID: bound.run.ID, WorkspaceID: bound.public.WorkspaceID, OperationID: id, Spec: &request.Spec, State: "unknown", Branch: bound.repository.Branch}
	approve, err := s.store.EnsureApproval(ctx, approval.Proposal{IdempotencyKey: approval.ProposalIdempotencyKey("thread.git", id), ProposalID: id, SessionID: bound.run.SessionID, WorkspaceID: bound.public.WorkspaceID, ToolName: "thread.git", ActionClass: "git_write", Mode: "per_call", Status: approval.StatusPending, RequestFingerprint: fingerprint, RequestedBy: request.RequestedBy, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()})
	if err != nil {
		return result, err
	}
	if approve.RunID != bound.run.ID || approve.RequestFingerprint != fingerprint {
		return result, apperror.New(apperror.CodeConflict, "Git approval source changed")
	}
	decision, err := s.store.DecideApproval(ctx, approval.DecisionRequest{ProposalID: id, IdempotencyKey: approval.ReviewIdempotencyKey("thread.git", id, approval.ActionApprove), Action: approval.ActionApprove, ReviewedBy: request.RequestedBy})
	if err != nil {
		return result, err
	}
	if decision.Approval.Status != approval.StatusApproved {
		return result, apperror.New(apperror.CodePolicyDenied, "Git operation was not approved")
	}
	if err = s.authority(ctx, bound, intent.Remote != nil, &lease); err != nil {
		return result, err
	}
	var claimed bool
	if intent.Remote != nil {
		_, claimed, err = s.store.StartRemoteOperation(ctx, id, fingerprint, time.Now().UTC())
	} else {
		_, claimed, err = s.store.StartGitMutationOperation(ctx, id, fingerprint, time.Now().UTC())
	}
	if err != nil {
		return result, err
	}
	if !claimed {
		return s.Observe(ctx, bound.thread.ID, request.OperationKey)
	}
	stopCtx, stop := s.monitorAuthority(ctx, bound, lease, intent.Remote != nil)
	defer stop()
	if intent.Remote != nil {
		if err = s.requireLiveThreadGitAuthority(stopCtx, bound, lease); err != nil {
			return result, err
		}
		receipt, err := s.remote.ExecuteGit(stopCtx, bound.public.RootPath, *intent.Remote, repository.RemoteBinding{RunID: bound.run.ID, WorkspaceID: bound.public.WorkspaceID, LocalHead: bound.repository.Head, Branch: intent.Spec.Branch}, key)
		if err != nil {
			result.Reason = "push result is not confirmed; inspect this original request"
			return result, nil
		}
		at := time.Now().UTC()
		_, _, err = s.store.CompleteRemoteOperation(context.WithoutCancel(ctx), id, gitmutation.RemoteRecord{PostHead: receipt.PostHead, CommitID: receipt.CommitID, StderrPrefix: receipt.StderrPrefix}, at)
		if err != nil {
			return result, err
		}
		result.State = "completed"
		result.ReceiptSaved = true
		result.CommitOID = receipt.CommitID
		result.RemoteOID = receipt.CommitID
		result.Branch = intent.Spec.Branch
		result.CompletedAt = &at
		return result, nil
	}
	boundary := WorkspaceMutationBoundaryRequest{RunID: bound.run.ID, Kind: workspacecheckpoint.TransactionGitMutation, OperationKey: key, TriggerReceiptID: key, LeaseID: lease.LeaseID, LeaseGeneration: lease.Generation}
	if s.checkpoints == nil {
		return result, apperror.New(apperror.CodeFailedPrecondition, "Git mutation checkpoint service is unavailable")
	}
	checkpoints := s.threadGitCheckpoints(bound)
	if _, err = checkpoints.BeginBoundary(stopCtx, boundary); err != nil {
		return result, err
	}
	var receipt repository.MutationReceipt
	if err = s.requireLiveThreadGitAuthority(stopCtx, bound, lease); err != nil {
		// Complete the already-open boundary as failed without dispatching Git.
	} else if prepared != nil {
		err = prepared.Publish(stopCtx)
		receipt = repository.MutationReceipt{PreHead: bound.repository.Head, PostHead: bound.repository.Head, Branch: bound.repository.Branch}
		if prepared.CommitOID != "" {
			receipt.PostHead = prepared.CommitOID
			receipt.CommitID = prepared.CommitOID
		}
	} else {
		receipt, err = s.local.ExecuteThreadBranch(stopCtx, bound.public.RootPath, repository.MutationSpec{ProtocolVersion: repository.MutationProtocolVersion, Operation: repository.MutationOperation(intent.Spec.Operation), Branch: intent.Spec.Branch}, bound.repository, intent.TargetCommitOID)
	}
	completionCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	_, boundaryErr := checkpoints.CompleteBoundary(completionCtx, boundary, err)
	if err != nil || boundaryErr != nil {
		result.Reason = "Git operation requires read-only result inspection"
		return result, nil
	}
	at := time.Now().UTC()
	_, _, err = s.store.CompleteGitMutationOperation(completionCtx, id, gitmutation.Record{PostHead: receipt.PostHead, Branch: receipt.Branch, CommitID: receipt.CommitID, Clean: receipt.Clean, Conflicted: receipt.Conflicted}, at)
	if err != nil {
		return result, err
	}
	result.State = "completed"
	result.ReceiptSaved = true
	result.CommitOID = receipt.CommitID
	result.Branch = receipt.Branch
	result.CompletedAt = &at
	return result, nil
}

// The checkpoint must capture the same physical task directory as the Git
// binding, including a continued Drydock. This private clone never changes the
// shared service's resolver or permits unrelated paused mutations.
func (s *ThreadGitService) threadGitCheckpoints(bound threadGitBinding) *WorkspaceCheckpointService {
	value := *s.checkpoints
	if bound.public.WorkspaceID != bound.public.SourceWorkspaceID && s.drydocks != nil {
		owner := *s.drydocks
		owner.WithCheckpointService(s.checkpoints)
		value = *owner.gitMutationCheckpointService()
	}
	value.operatorGitThreadID = bound.thread.ID
	value.runWorkspace = func(ctx context.Context, runID string) (session.WorkspaceInfo, bool, error) {
		if runID != bound.run.ID {
			return session.WorkspaceInfo{}, false, apperror.New(apperror.CodeConflict, "Git checkpoint Run changed")
		}
		run, err := s.store.GetRun(ctx, runID)
		if err != nil {
			return session.WorkspaceInfo{}, false, err
		}
		mission, err := s.store.GetMission(ctx, run.MissionID)
		if err != nil {
			return session.WorkspaceInfo{}, false, err
		}
		files, err := ResolveRunFileWorkspace(ctx, s.store, run, mission, s.drydocks)
		if err != nil {
			return session.WorkspaceInfo{}, false, err
		}
		if files.Drydock != nil {
			if err := requireCurrentRunFileDrydock(ctx, s.store, runID, *files.Drydock); err != nil {
				return session.WorkspaceInfo{}, false, err
			}
		}
		if run.SessionID != bound.run.SessionID || files.Workspace.ID != bound.public.WorkspaceID || files.Workspace.RootPath != bound.public.RootPath {
			return session.WorkspaceInfo{}, false, apperror.New(apperror.CodeConflict, "Git checkpoint directory changed")
		}
		return files.Workspace, true, nil
	}
	return &value
}

func (s *ThreadGitService) monitorAuthority(ctx context.Context, bound threadGitBinding, lease domain.RunExecutionLease, network bool) (context.Context, context.CancelFunc) {
	child, cancel := context.WithCancel(ctx)
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-child.Done():
				return
			case <-ticker.C:
				if err := s.requireLiveThreadGitAuthority(child, bound, lease); err != nil {
					cancel()
					return
				}
			}
		}
	}()
	return child, cancel
}

func (s *ThreadGitService) requireLiveThreadGitAuthority(ctx context.Context, bound threadGitBinding, lease domain.RunExecutionLease) error {
	thread, err := s.store.GetThread(ctx, bound.thread.ID)
	if err != nil {
		return err
	}
	run, err := s.store.GetRun(ctx, bound.run.ID)
	if err != nil {
		return err
	}
	permission, err := s.store.GetRunExecutionPermission(ctx, bound.run.ID)
	if err != nil {
		return err
	}
	current, found, err := s.store.GetRunExecutionLease(ctx, run.ID)
	if err != nil {
		return err
	}
	if thread.Status != domain.ThreadActive || thread.ActiveRunID != run.ID || run.Status != bound.run.Status ||
		permission.ID != bound.permission.ID || permission.Revision != bound.permission.Revision || !s.capabilities.AllowsSnapshot(permission) ||
		!found || current.LeaseID != lease.LeaseID || current.Generation != lease.Generation || !current.ActiveAt(time.Now().UTC()) {
		return apperror.New(apperror.CodeConflict, "task Git execution authority changed")
	}
	return nil
}

func (s *ThreadGitService) Observe(ctx context.Context, threadID, key string) (ThreadGitResult, error) {
	result, found, err := s.replay(ctx, threadID, key, nil)
	if !found && err == nil {
		if advanced, present, advancedErr := s.replayWorktree(ctx, threadID, key, nil); present || advancedErr != nil {
			return advanced, advancedErr
		}
	}
	return result, err
}

func (s *ThreadGitService) replay(ctx context.Context, threadID, key string, request *ThreadGitExecuteRequest) (ThreadGitResult, bool, error) {
	result := ThreadGitResult{Version: ThreadGitProtocolVersion, ThreadID: threadID, State: "not_received"}
	thread, err := s.store.GetThread(ctx, threadID)
	if err != nil {
		return result, false, err
	}
	result.RunID = thread.LastRunID
	if key == "" || len(key) > 256 {
		return result, false, apperror.New(apperror.CodeInvalidArgument, "Git operation key is invalid")
	}
	digest := threadGitKey(threadID, key)
	local, found, err := s.store.GetGitMutationByKey(ctx, digest)
	if err != nil {
		return result, false, err
	}
	var remote gitmutation.RemoteRecord
	if !found {
		remote, found, err = s.store.GetGitRemoteByKey(ctx, digest)
		if err != nil {
			return result, false, err
		}
	}
	if !found {
		return result, false, nil
	}
	var intent threadGitIntent
	raw := local.SpecJSON
	fingerprint := local.RequestFingerprint
	result.RunID = local.RunID
	result.WorkspaceID = local.WorkspaceID
	result.OperationID = local.ID
	result.CompletedAt = local.CompletedAt
	result.CommitOID = local.CommitID
	result.Branch = local.Branch
	if remote.ID != "" {
		raw = remote.SpecJSON
		fingerprint = remote.RequestFingerprint
		result.RunID = remote.RunID
		result.WorkspaceID = remote.WorkspaceID
		result.OperationID = remote.ID
		result.CompletedAt = remote.CompletedAt
		result.CommitOID = remote.CommitID
		result.Branch = remote.Branch
	}
	if json.Unmarshal([]byte(raw), &intent) != nil || intent.Version != ThreadGitProtocolVersion || intent.ThreadID != threadID || intent.WorkspaceID != result.WorkspaceID {
		return result, true, apperror.New(apperror.CodeConflict, "Git request belongs to a different scope")
	}
	if request != nil {
		a, _ := json.Marshal(request.Spec)
		b, _ := json.Marshal(intent.Spec)
		if request.RunID != result.RunID || request.ExpectedPreviewFingerprint != intent.PreviewFingerprint || request.RequestedBy != intent.RequestedBy || string(a) != string(b) {
			return result, true, apperror.New(apperror.CodeConflict, "Git key was already used for a different request")
		}
	}
	if fingerprint != runmutation.Fingerprint("thread_git_request.v1", threadID, result.RunID, intent.PreviewFingerprint, intent.RequestedBy) {
		return result, true, apperror.New(apperror.CodeConflict, "stored Git request fingerprint is invalid")
	}
	result.Spec = &intent.Spec
	result.Replayed = true
	result.State = "unknown"
	if result.CompletedAt != nil {
		result.State = "completed"
		result.ReceiptSaved = true
		if remote.ID != "" {
			result.RemoteOID = remote.CommitID
		}
		return result, true, nil
	}
	// Observation uses the original Run's binding and stored immutable target.
	// It never reconstructs success from whichever HEAD happens to be current.
	bound, err := s.bind(ctx, threadID)
	if err != nil || bound.run.ID != result.RunID || bound.public.WorkspaceID != intent.WorkspaceID || bound.public.RootPath != intent.RootPath || bound.repository.RepositorySHA256 != intent.Binding.RepositorySHA256 || bound.repository.CommonDirSHA256 != intent.Binding.CommonDirSHA256 {
		result.Reason = "original repository authority is unavailable; no operation was repeated"
		return result, true, nil
	}
	approve, err := s.store.GetApprovalByProposal(ctx, result.OperationID)
	if err != nil || approve.Status != approval.StatusApproved || approve.RequestFingerprint != fingerprint {
		result.Reason = "approval or execution was not confirmed; no operation was repeated"
		return result, true, nil
	}
	if intent.Commit != nil && local.StartedAt != nil {
		ok, err := s.local.ObserveSelectedCommit(ctx, intent.RootPath, *intent.Commit)
		if err == nil && ok {
			result.State = "completed"
			result.Observed = true
			result.CommitOID = intent.Commit.CommitOID
			result.Branch = intent.Commit.Branch
			return result, true, nil
		}
	} else if intent.Remote != nil && remote.StartedAt != nil {
		decision, err := executionauth.EvaluateExecutionPermission(bound.permission, s.capabilities, executionauth.PermissionRequest{Kind: executionauth.PermissionOperationStatelessCommand, HostFilesystem: true, Network: true, OperatorApproved: true})
		if err == nil && decision.Allowed && decision.Network {
			oid, err := s.remote.ReadRemoteOID(ctx, intent.RootPath, *intent.Remote)
			if err == nil && oid == intent.Remote.CommitOID {
				result.State = "completed"
				result.Observed = true
				result.RemoteOID = oid
				result.CommitOID = oid
				result.Branch = intent.Remote.Branch
				return result, true, nil
			}
		}
	}
	result.Reason = "the original Git result is still unknown; inspect repository state without repeating the operation"
	return result, true, nil
}

var _ = errors.Join
