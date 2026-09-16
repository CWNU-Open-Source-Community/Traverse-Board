package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/credential"
	"cyberagent-workbench/internal/githubreview"
	"cyberagent-workbench/internal/gitmutation"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/repository"
	"cyberagent-workbench/internal/store"
)

type threadPRRemoteFixture struct {
	fakeGitHubReviewRemote
	head, base        string
	created           *githubreview.PullRequest
	createError       error
	creates, observes int
}

func (f *threadPRRemoteFixture) DefaultBranch(context.Context, githubreview.RepositoryIdentity, githubreview.CredentialReference) (string, error) {
	return "main", nil
}
func (f *threadPRRemoteFixture) BranchSHA(_ context.Context, _ githubreview.RepositoryIdentity, branch string, _ githubreview.CredentialReference) (string, error) {
	if branch == "main" {
		return f.base, nil
	}
	return f.head, nil
}
func (f *threadPRRemoteFixture) ListPullRequests(context.Context, githubreview.RepositoryIdentity, string, string, githubreview.CredentialReference) ([]githubreview.PullRequest, error) {
	if f.created != nil {
		return []githubreview.PullRequest{*f.created}, nil
	}
	return []githubreview.PullRequest{}, nil
}
func (f *threadPRRemoteFixture) GetPullRequest(context.Context, githubreview.RepositoryIdentity, int64, githubreview.CredentialReference) (githubreview.PullRequest, error) {
	return *f.created, nil
}
func (f *threadPRRemoteFixture) ObserveDraft(context.Context, githubreview.PullRequestDraft) (githubreview.PullRequest, bool, error) {
	f.observes++
	if f.created == nil {
		return githubreview.PullRequest{}, false, nil
	}
	return *f.created, true, nil
}
func (f *threadPRRemoteFixture) CreateDraft(_ context.Context, d githubreview.PullRequestDraft) (githubreview.PullRequest, error) {
	f.creates++
	p := githubreview.PullRequest{Repository: d.Repository, Number: 17, NodeID: "PR17", URL: "https://github.com/acme/widget/pull/17", Title: githubreview.SanitizeRemoteText(d.Title, 1024), State: "open", Draft: true, HeadRepository: d.Repository.FullName, HeadBranch: d.HeadBranch, HeadSHA: d.HeadSHA, BaseBranch: d.BaseBranch, BaseSHA: d.BaseSHA, UpdatedAt: time.Now().UTC()}
	f.created = &p
	return p, f.createError
}

type threadPRFixture struct {
	service               *ThreadPullRequestService
	remote                *threadPRRemoteFixture
	state                 *store.SQLiteStore
	threadID, runID, root string
	request               ThreadPullRequestPreviewRequest
	control               *ApprovalControlService
	reviewer              *gitAdvancedApprovalReviewer
}

func newThreadPRFixture(t *testing.T) threadPRFixture {
	t.Helper()
	f := newGitAdvancedApplicationFixture(t)
	if _, _, err := f.state.ReleaseRunExecutionLease(t.Context(), f.lease); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRunService(f.state).Pause(t.Context(), f.run.ID); err != nil {
		t.Fatal(err)
	}
	runFixtureGit(t, "-C", f.root, "branch", "-M", "feature/pr")
	runFixtureGit(t, "-C", f.root, "remote", "add", "origin", "https://github.com/acme/widget.git")
	thread, err := f.state.GetThreadByRun(t.Context(), f.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	local, _ := repository.NewMutationExecutor()
	git := NewThreadGitService(f.state, local, nil, nil, nil, f.capabilities)
	bound, err := git.CaptureThreadGitContext(t.Context(), thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	repo, _ := githubreview.ParseRepository("acme/widget")
	ref := githubreview.CredentialReference{Name: "thread-pr-token", Kind: githubreview.AuthFineGrainedPAT}
	remote := &threadPRRemoteFixture{head: bound.HeadSHA, base: strings.Repeat("1", 40)}
	remote.qualification = githubreview.Qualification{Eligible: true, Capability: githubreview.CapabilitySnapshot{Read: true}, Diagnostics: []githubreview.Diagnostic{}}
	credentials := credential.NewMemoryStore()
	if err = credentials.Put(t.Context(), ref.Name, "fixture-not-a-real-token"); err != nil {
		t.Fatal(err)
	}
	review, err := NewGitHubReviewService(f.state, credentials, f.executor, f.capabilities)
	if err != nil {
		t.Fatal(err)
	}
	review.clientFactory = func(*githubreview.AuthManager, githubreview.Connection) (githubReviewRemote, error) {
		return remote, nil
	}
	configured, err := review.Configure(t.Context(), GitHubReviewConfigureRequest{ProtocolVersion: GitHubReviewAPIProtocolVersion, Repository: repo, Credential: ref, AllowedLogHosts: []string{}, WriteEnabled: true, Enabled: true, RequestedBy: "test_operator"})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewThreadPullRequestService(f.state, review, git)
	reviewer := &gitAdvancedApprovalReviewer{}
	return threadPRFixture{service: svc, remote: remote, state: f.state, threadID: thread.ID, runID: f.run.ID, root: f.root, request: ThreadPullRequestPreviewRequest{Version: ThreadPullRequestVersion, ConnectionID: configured.Connection.ID, BaseBranch: "main", Title: "Reviewed draft", Body: "Description\n\nValidation: fixture", ExpectedRunID: f.run.ID, ExpectedHeadSHA: bound.HeadSHA, OperationKey: "draft-original-key"}, reviewer: reviewer, control: NewApprovalControlService(f.state, reviewer, policy.NewDefaultChecker())}
}

func (f threadPRFixture) previewAndApprove(t *testing.T) ThreadPullRequestPreviewResult {
	t.Helper()
	p, err := f.service.Preview(t.Context(), f.threadID, f.request)
	if err != nil || p.Approval == nil || p.Preview == nil {
		t.Fatalf("preview %#v %v", p, err)
	}
	if _, err = f.control.Decide(t.Context(), DecideApprovalControlRequest{Version: ApprovalControlProtocolVersion, RunID: f.runID, ApprovalID: p.Approval.ID, Action: ApprovalControlApproveOnce, OperationKey: f.request.OperationKey + "-approve", ReviewedBy: "http_operator"}); err != nil {
		t.Fatal(err)
	}
	if f.reviewer.calls != 0 {
		t.Fatal("PR approval invoked tool/model reviewer")
	}
	return p
}

func TestThreadPullRequestApprovedCreateAndPureObserver(t *testing.T) {
	f := newThreadPRFixture(t)
	p := f.previewAndApprove(t)
	request := ThreadPullRequestCreateRequest{Version: ThreadPullRequestVersion, OperationID: p.Preview.OperationID, ApprovalID: p.Approval.ID}
	created, err := f.service.Create(t.Context(), f.threadID, request)
	if err != nil || created.State != "created" || !created.ReceiptSaved || f.remote.creates != 1 {
		t.Fatalf("create %#v %v calls%d", created, err, f.remote.creates)
	}
	before, _ := f.state.ListRunEvents(t.Context(), f.runID)
	for i := 0; i < 2; i++ {
		v, err := f.service.Observe(t.Context(), f.threadID, f.request.OperationKey)
		if err != nil || v.State != "created" || v.PullRequest.Number != 17 {
			t.Fatalf("observe %#v %v", v, err)
		}
	}
	if _, err = f.service.Create(t.Context(), f.threadID, request); err != nil {
		t.Fatal(err)
	}
	after, _ := f.state.ListRunEvents(t.Context(), f.runID)
	if len(before) != len(after) || f.remote.creates != 1 || f.reviewer.calls != 0 {
		t.Fatal("replay wrote or resumed work")
	}
	run, _ := f.state.GetRun(t.Context(), f.runID)
	if string(run.Status) != "paused" {
		t.Fatal("PR operation resumed the model task")
	}
	unknown, err := f.service.Observe(t.Context(), f.threadID, "never-received")
	if err != nil || unknown.State != "not_received" {
		t.Fatalf("missing %#v %v", unknown, err)
	}
	if _, found, _ := f.state.GetRemoteOperation(t.Context(), "thread-pr-"+threadPRKey(f.threadID, "never-received")); found {
		t.Fatal("observer reserved unknown key")
	}
}

func TestThreadPullRequestUnknownIsReadOnlyAfterServiceRestart(t *testing.T) {
	f := newThreadPRFixture(t)
	p := f.previewAndApprove(t)
	f.remote.createError = &githubreview.Error{Code: githubreview.FailureOffline, Message: "fixture response lost after accepting POST"}
	r, err := f.service.Create(t.Context(), f.threadID, ThreadPullRequestCreateRequest{Version: ThreadPullRequestVersion, OperationID: p.Preview.OperationID, ApprovalID: p.Approval.ID})
	if err != nil || r.State != "unknown" || r.ReceiptSaved {
		t.Fatalf("uncertain %#v %v", r, err)
	}
	before, _, _ := f.state.GetRemoteOperation(t.Context(), p.Preview.OperationID)
	eventsBefore, _ := f.state.ListRunEvents(t.Context(), f.runID)
	f.service = NewThreadPullRequestService(f.state, f.service.review, f.service.git)
	for i := 0; i < 2; i++ {
		v, err := f.service.Observe(t.Context(), f.threadID, f.request.OperationKey)
		if err != nil || v.State != "created" || v.ReceiptSaved {
			t.Fatalf("recovered %#v %v", v, err)
		}
	}
	after, _, _ := f.state.GetRemoteOperation(t.Context(), p.Preview.OperationID)
	eventsAfter, _ := f.state.ListRunEvents(t.Context(), f.runID)
	if before.CompletedAt != nil || after.CompletedAt != nil || after.StartedAt == nil || before.SpecJSON != after.SpecJSON || len(eventsBefore) != len(eventsAfter) || f.remote.creates != 1 {
		t.Fatal("unknown recovery changed history or repeated creation")
	}
}

func TestThreadPullRequestRejectsChangedKeyHeadAndDeniedApproval(t *testing.T) {
	for _, scenario := range []string{"body-key", "head", "denied", "remote-not-published"} {
		t.Run(scenario, func(t *testing.T) {
			f := newThreadPRFixture(t)
			if scenario == "remote-not-published" {
				f.remote.head = strings.Repeat("2", 40)
				if _, err := f.service.Preview(t.Context(), f.threadID, f.request); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
					t.Fatalf("unpublished %v", err)
				}
				return
			}
			p, err := f.service.Preview(t.Context(), f.threadID, f.request)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "body-key" {
				f.request.Body = "changed"
				if _, err = f.service.Preview(t.Context(), f.threadID, f.request); apperror.CodeOf(err) != apperror.CodeConflict {
					t.Fatalf("key drift %v", err)
				}
				return
			}
			action := ApprovalControlApproveOnce
			if scenario == "denied" {
				action = ApprovalControlDeny
			}
			_, err = f.control.Decide(t.Context(), DecideApprovalControlRequest{Version: ApprovalControlProtocolVersion, RunID: f.runID, ApprovalID: p.Approval.ID, Action: action, OperationKey: "decision-original-key", ReviewedBy: "operator"})
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "head" {
				if err = os.WriteFile(filepath.Join(f.root, "new.txt"), []byte("new commit\n"), 0600); err != nil {
					t.Fatal(err)
				}
				runFixtureGit(t, "-C", f.root, "add", "new.txt")
				runFixtureGit(t, "-C", f.root, "commit", "-qm", "new head")
			}
			_, err = f.service.Create(t.Context(), f.threadID, ThreadPullRequestCreateRequest{Version: ThreadPullRequestVersion, OperationID: p.Preview.OperationID, ApprovalID: p.Approval.ID})
			if err == nil || f.remote.creates != 0 {
				t.Fatalf("unsafe create calls=%d err=%v", f.remote.creates, err)
			}
			row, _, _ := f.state.GetRemoteOperation(t.Context(), p.Preview.OperationID)
			if row.StartedAt != nil {
				t.Fatal("rejected operation consumed execution claim")
			}
		})
	}
}

type threadPRFailReceiptStore struct{ *store.SQLiteStore }

func (s threadPRFailReceiptStore) CompleteRemoteOperation(context.Context, string, gitmutation.RemoteRecord, time.Time) (gitmutation.RemoteRecord, bool, error) {
	return gitmutation.RemoteRecord{}, false, errors.New("fixture unavailable receipt storage")
}
func TestThreadPullRequestReceiptFailureDoesNotRetryCreate(t *testing.T) {
	f := newThreadPRFixture(t)
	p := f.previewAndApprove(t)
	f.service.store = threadPRFailReceiptStore{f.state}
	v, err := f.service.Create(t.Context(), f.threadID, ThreadPullRequestCreateRequest{Version: ThreadPullRequestVersion, OperationID: p.Preview.OperationID, ApprovalID: p.Approval.ID})
	if err != nil || v.State != "unknown" {
		t.Fatalf("lost receipt %#v %v", v, err)
	}
	f.service.store = f.state
	v, err = f.service.Observe(t.Context(), f.threadID, f.request.OperationKey)
	if err != nil || v.State != "created" || v.ReceiptSaved || f.remote.creates != 1 {
		t.Fatalf("observe %#v %v", v, err)
	}
}

func TestThreadPullRequestQualificationPreservesExactRemoteFailure(t *testing.T) {
	f := newThreadPRFixture(t)
	f.remote.qualification.Eligible = false
	f.remote.qualification.Diagnostics = []githubreview.Diagnostic{{Code: string(githubreview.FailureOffline), Level: githubreview.DiagnosticError, Message: "fixture GitHub connection is offline"}}
	_, err := f.service.Preview(t.Context(), f.threadID, f.request)
	if apperror.CodeOf(err) != apperror.CodeUnavailable || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("qualification failure became a policy denial: %v", err)
	}
	if _, found, _ := f.state.GetRemoteOperation(t.Context(), "thread-pr-"+threadPRKey(f.threadID, f.request.OperationKey)); found {
		t.Fatal("failed qualification reserved creation")
	}
}
