package application

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/credential"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/gitmutation"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/repository"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/session"
)

// GitRemoteStore is the bounded store surface for the remote workflow.
type GitRemoteStore interface {
	GetRun(context.Context, string) (domain.Run, error)
	GetMission(context.Context, string) (domain.Mission, error)
	GetWorkspaceByID(context.Context, string) (session.WorkspaceRecord, error)
	CreateRemoteOperation(context.Context, gitmutation.RemoteRecord) (gitmutation.RemoteRecord, bool, error)
	CompleteRemoteOperation(context.Context, string, gitmutation.RemoteRecord, time.Time) (gitmutation.RemoteRecord, bool, error)
}

// GitRemoteService owns the review-then-execute flow for network-scoped
// remote Git and PR operations.
type GitRemoteService struct {
	store       GitRemoteStore
	executor    *repository.RemoteExecutor
	prClient    *repository.PRClient
	credentials credential.Store
}

func NewGitRemoteService(store GitRemoteStore, executor *repository.RemoteExecutor,
	prClient *repository.PRClient, credentials credential.Store,
) *GitRemoteService {
	return &GitRemoteService{store: store, executor: executor, prClient: prClient, credentials: credentials}
}

type GitRemoteRequest struct {
	RunID        string
	OperationKey string
	Spec         repository.RemoteSpec
	RequestedBy  string
}

type GitRemoteExecuteResult struct {
	Record   gitmutation.RemoteRecord
	Receipt  repository.RemoteReceipt
	PR       repository.PRReceipt
	Replayed bool
}

// Execute validates the network scope, resolves the workspace, runs the
// typed operation, and records the redacted receipt plus the run event.
func (s *GitRemoteService) Execute(ctx context.Context, request GitRemoteRequest) (GitRemoteExecuteResult, error) {
	if s == nil || s.store == nil || s.executor == nil || !s.executor.Available() {
		return GitRemoteExecuteResult{}, apperror.New(apperror.CodeFailedPrecondition,
			"remote git service requires a store and an available executor")
	}
	if err := s.executor.ValidateSpec(request.Spec); err != nil {
		return GitRemoteExecuteResult{}, apperror.Wrap(apperror.CodeInvalidArgument,
			"remote spec is invalid", err)
	}
	run, err := s.store.GetRun(ctx, request.RunID)
	if err != nil {
		return GitRemoteExecuteResult{}, apperror.Normalize(err)
	}
	mission, err := s.store.GetMission(ctx, run.MissionID)
	if err != nil {
		return GitRemoteExecuteResult{}, apperror.Normalize(err)
	}
	workspace, err := s.store.GetWorkspaceByID(ctx, mission.WorkspaceID)
	if err != nil {
		return GitRemoteExecuteResult{}, apperror.Normalize(err)
	}
	parsed, err := url.Parse(request.Spec.RemoteURL)
	if err != nil {
		return GitRemoteExecuteResult{}, apperror.New(apperror.CodeInvalidArgument, "remote URL is invalid")
	}
	port := parsed.Port()
	if port == "" {
		port = "443"
	}
	specJSON, err := json.Marshal(request.Spec)
	if err != nil {
		return GitRemoteExecuteResult{}, err
	}
	keyDigest := runmutation.OperationKeyDigest("git_remote_operation.v1", run.ID, request.OperationKey)
	requestFingerprint := runmutation.Fingerprint("git_remote_request.v1", run.ID,
		workspace.ID, parsed.Hostname(), port, "https", request.Spec.Branch, string(specJSON))
	record := gitmutation.RemoteRecord{
		ID: idgen.New("git-remote"), ProtocolVersion: repository.RemoteProtocolVersion,
		OperationKeyDigest: keyDigest, RequestFingerprint: requestFingerprint,
		RunID: run.ID, WorkspaceID: workspace.ID, Operation: gitmutation.RemoteOperation(request.Spec.Operation),
		SpecJSON: string(specJSON), RemoteHost: parsed.Hostname(), RemotePort: port,
		Protocol: "https", Branch: request.Spec.Branch, CreatedAt: time.Now().UTC(),
	}
	created, replayed, err := s.store.CreateRemoteOperation(ctx, record)
	if err != nil {
		return GitRemoteExecuteResult{}, apperror.Normalize(err)
	}
	if replayed {
		return GitRemoteExecuteResult{Record: created, Replayed: true}, nil
	}
	binding := repository.RemoteBinding{
		ProtocolVersion: repository.RemoteProtocolVersion, RunID: run.ID, WorkspaceID: workspace.ID,
		RemoteHost: parsed.Hostname(), RemotePort: port, Protocol: "https",
		Branch: request.Spec.Branch, NetworkTTLMillis: request.Spec.NetworkTTLMillis,
		CredentialName: request.Spec.CredentialName, CapturedAt: time.Now().UTC(),
	}
	var receipt repository.RemoteReceipt
	var pr repository.PRReceipt
	if request.Spec.Operation == repository.RemoteCreatePR || request.Spec.Operation == repository.RemoteUpdatePR {
		if s.prClient == nil {
			return GitRemoteExecuteResult{}, apperror.New(apperror.CodeFailedPrecondition, "PR client is unavailable")
		}
		token := ""
		if request.Spec.CredentialName != "" {
			if s.credentials == nil || !s.credentials.Available() {
				return GitRemoteExecuteResult{}, apperror.New(apperror.CodeUnavailable, "credential store is unavailable")
			}
			resolved, found, err := s.credentials.Get(ctx, request.Spec.CredentialName)
			if err != nil || !found {
				return GitRemoteExecuteResult{}, apperror.New(apperror.CodeUnavailable, "referenced credential is unavailable")
			}
			token = resolved
		}
		if request.Spec.Operation == repository.RemoteCreatePR {
			pr, err = s.prClient.CreatePR(ctx, request.Spec.RemoteURL, request.Spec.Branch,
				request.Spec.BaseBranch, request.Spec.PRTitle, request.Spec.PRBody, token)
		} else {
			pr, err = s.prClient.UpdatePR(ctx, request.Spec.RemoteURL, request.Spec.PRNumber, request.Spec.PRTitle, request.Spec.PRBody, token)
		}
		if err != nil {
			return GitRemoteExecuteResult{}, err
		}
		receipt = repository.RemoteReceipt{ProtocolVersion: repository.RemoteProtocolVersion,
			Operation: request.Spec.Operation, Branch: request.Spec.Branch, RemoteHost: parsed.Hostname(),
			PullRequestURL: pr.URL}
	} else {
		receipt, err = s.executor.ExecuteGit(ctx, workspace.RootPath, request.Spec, binding, request.OperationKey)
		if err != nil {
			return GitRemoteExecuteResult{}, err
		}
	}
	completed, _, err := s.store.CompleteRemoteOperation(ctx, created.ID, gitmutation.RemoteRecord{
		PostHead: receipt.PostHead, CommitID: receipt.CommitID, PullRequestURL: receipt.PullRequestURL,
		PullRequestNumber: pr.Number, StderrPrefix: receipt.StderrPrefix,
	}, time.Now().UTC())
	if err != nil {
		return GitRemoteExecuteResult{}, apperror.Normalize(err)
	}
	return GitRemoteExecuteResult{Record: completed, Receipt: receipt, PR: pr}, nil
}

var _ = strings.TrimSpace
