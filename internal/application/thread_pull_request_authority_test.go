package application

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/githubreview"
	"cyberagent-workbench/internal/gitmutation"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/store"
)

type threadPRWire struct {
	posts atomic.Int64
	reads atomic.Int64
	mu    sync.Mutex
	body  string
}

// Review still uses the existing native fixture. Execution uses the actual
// authenticated GitHub client, HTTP stack and the already configured ledger.
func installThreadPRWire(t *testing.T, f *threadPRFixture, lostReply bool, beforeList func()) *threadPRWire {
	t.Helper()
	wire := &threadPRWire{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer fixture-not-a-real-token" {
			t.Error("native wire lost the exact fixture credential")
		}
		if r.Method == http.MethodGet {
			wire.reads.Add(1)
		}
		response := func() map[string]any {
			wire.mu.Lock()
			defer wire.mu.Unlock()
			return map[string]any{"number": 17, "node_id": "PR17", "state": "open", "title": f.request.Title,
				"body": wire.body, "draft": true, "merged": false, "updated_at": time.Now().UTC(),
				"base": map[string]any{"ref": "main", "sha": f.remote.base, "repo": map[string]any{"full_name": "acme/widget", "node_id": "R_widget"}},
				"head": map[string]any{"ref": "feature/pr", "sha": f.remote.head, "repo": map[string]any{"full_name": "acme/widget"}}}
		}
		var value any
		switch {
		case strings.Contains(r.URL.Path, "/branches/"):
			branch := strings.TrimPrefix(r.URL.Path, "/repos/acme/widget/branches/")
			sha := f.remote.base
			if branch == "feature/pr" {
				sha = f.remote.head
			}
			value = map[string]any{"name": branch, "commit": map[string]string{"sha": sha}}
		case r.URL.Path == "/repos/acme/widget/pulls" && r.Method == http.MethodPost:
			wire.posts.Add(1)
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["draft"] != true || body["title"] != f.request.Title || body["head"] != "feature/pr" || body["base"] != "main" {
				t.Errorf("changed native draft payload: %#v", body)
			}
			wire.mu.Lock()
			wire.body, _ = body["body"].(string)
			wire.mu.Unlock()
			if lostReply {
				connection, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				_ = connection.Close()
				return
			}
			value = response()
		case r.URL.Path == "/repos/acme/widget/pulls" && r.Method == http.MethodGet:
			if beforeList != nil {
				beforeList()
			}
			value = []any{}
			if wire.posts.Load() > 0 {
				value = []any{response()}
			}
		case r.URL.Path == "/repos/acme/widget/pulls/17" && r.Method == http.MethodGet:
			value = response()
		default:
			t.Errorf("unexpected native request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if err := json.NewEncoder(w).Encode(value); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	prior := f.service.review
	review, err := NewGitHubReviewServiceForTest(f.state, prior.credentials, prior.executor,
		prior.permissionCapabilities, server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	f.service = NewThreadPullRequestService(f.state, review, f.service.git)
	return wire
}

func restartThreadPRRuntime(t *testing.T, f *threadPRFixture) *domain.ExecutionPermissionRuntimeAuthority {
	t.Helper()
	prior := f.service.review
	caps := prior.permissionCapabilities
	caps.RuntimeAuthority = domain.NewExecutionPermissionRuntimeAuthority()
	review, err := NewGitHubReviewService(f.state, prior.credentials, prior.executor, caps)
	if err != nil {
		t.Fatal(err)
	}
	review.clientFactory = prior.clientFactory
	f.service = NewThreadPullRequestService(f.state, review, f.service.git)
	return caps.RuntimeAuthority
}

func TestThreadPullRequestThreeModesRequireExactConsentAndNeverResend(t *testing.T) {
	for _, mode := range []domain.RunExecutionPermissionMode{domain.RunExecutionPermissionAsk, domain.RunExecutionPermissionAuto, domain.RunExecutionPermissionFull} {
		for _, lost := range []bool{false, true} {
			t.Run(string(mode)+map[bool]string{false: "/received", true: "/lost"}[lost], func(t *testing.T) {
				f := newThreadPRFixture(t, mode)
				preview, err := f.service.Preview(t.Context(), f.threadID, f.request)
				if err != nil || preview.Preview == nil || preview.Approval == nil {
					t.Fatalf("preview: %v %#v", err, preview)
				}
				request := ThreadPullRequestCreateRequest{Version: ThreadPullRequestVersion, OperationID: preview.Preview.OperationID, ApprovalID: preview.Approval.ID}
				if _, err := f.service.Create(t.Context(), f.threadID, request); err == nil {
					t.Fatal("mode preference published without exact consent")
				}
				row, found, err := f.state.GetRemoteOperation(t.Context(), request.OperationID)
				if err != nil || !found || row.StartedAt != nil || f.remote.creates != 0 {
					t.Fatalf("unapproved draft consumed its dispatch: %v %#v", err, row)
				}
				f.previewAndApprove(t)
				wire := installThreadPRWire(t, &f, lost, nil)
				created, err := f.service.Create(t.Context(), f.threadID, request)
				want := "created"
				if lost {
					want = "unknown"
				}
				if err != nil || created.State != want || created.ReceiptSaved == lost || wire.posts.Load() != 1 {
					t.Fatalf("native result: %v %#v posts=%d", err, created, wire.posts.Load())
				}
				before, _, _ := f.state.GetRemoteOperation(t.Context(), request.OperationID)
				eventsBefore, _ := f.state.ListRunEvents(t.Context(), f.runID)
				// Retained operation recovery does not activate a cold Full grant or
				// recreate a Run fence, and can only read the original remote result.
				cold := restartThreadPRRuntime(t, &f)
				for i := 0; i < 2; i++ {
					replayed, err := f.service.Create(t.Context(), f.threadID, request)
					if err != nil || replayed.State != "created" || !replayed.Replayed || replayed.PullRequest == nil || replayed.PullRequest.Number != 17 {
						t.Fatalf("cold read-only recovery: %v %#v", err, replayed)
					}
				}
				after, _, _ := f.state.GetRemoteOperation(t.Context(), request.OperationID)
				eventsAfter, _ := f.state.ListRunEvents(t.Context(), f.runID)
				beforeJSON, _ := json.Marshal(before)
				afterJSON, _ := json.Marshal(after)
				if string(beforeJSON) != string(afterJSON) || len(eventsBefore) != len(eventsAfter) || wire.posts.Load() != 1 {
					t.Fatal("read-only recovery rewrote receipts or repeated the POST")
				}
				if _, found := cold.RunAuthorizationFence(f.runID); found {
					t.Fatal("cold observation manufactured runtime authority")
				}
			})
		}
	}
}

type threadPRAfterStartStore struct {
	*store.SQLiteStore
	afterStart func()
}

func (s *threadPRAfterStartStore) StartRemoteOperation(ctx context.Context, id, fingerprint string, at time.Time) (gitmutation.RemoteRecord, bool, error) {
	row, first, err := s.SQLiteStore.StartRemoteOperation(ctx, id, fingerprint, at)
	if err == nil && first {
		s.afterStart()
	}
	return row, first, err
}

func TestThreadPullRequestThreeModesRecheckRevocationAtNativeDispatch(t *testing.T) {
	for _, mode := range []domain.RunExecutionPermissionMode{domain.RunExecutionPermissionAsk, domain.RunExecutionPermissionAuto, domain.RunExecutionPermissionFull} {
		for _, point := range []string{"after_start", "after_remote_preflight"} {
			t.Run(string(mode)+"/"+point, func(t *testing.T) {
				f := newThreadPRFixture(t, mode)
				p := f.previewAndApprove(t)
				runtime := f.service.review.permissionCapabilities.RuntimeAuthority
				revoke := func() { runtime.RevokeRun(f.runID) }
				var onList func()
				if point == "after_remote_preflight" {
					onList = revoke
				}
				wire := installThreadPRWire(t, &f, false, onList)
				if point == "after_start" {
					f.service.store = &threadPRAfterStartStore{SQLiteStore: f.state, afterStart: revoke}
				}
				request := ThreadPullRequestCreateRequest{Version: ThreadPullRequestVersion, OperationID: p.Preview.OperationID, ApprovalID: p.Approval.ID}
				result, err := f.service.Create(t.Context(), f.threadID, request)
				if err != nil || result.State != "failed" || !result.ReceiptSaved || wire.posts.Load() != 0 {
					t.Fatalf("revoked native result: %v %#v posts=%d", err, result, wire.posts.Load())
				}
				if point == "after_start" && wire.reads.Load() != 0 || point == "after_remote_preflight" && wire.reads.Load() != 3 {
					t.Fatalf("wrong boundary exercised: %s reads=%d", point, wire.reads.Load())
				}
				if _, found := runtime.RunAuthorizationFence(f.runID); found {
					t.Fatal("dispatch reissued a revoked review fence")
				}
				row, _, _ := f.state.GetRemoteOperation(t.Context(), request.OperationID)
				if row.StartedAt == nil || row.CompletedAt == nil {
					t.Fatal("local dispatch refusal lost the durable attempt")
				}
				reads := wire.reads.Load()
				if _, err := f.service.Create(t.Context(), f.threadID, request); err != nil || wire.posts.Load() != 0 || wire.reads.Load() != reads {
					t.Fatalf("failed attempt was dispatched on retry: %v", err)
				}
			})
		}
	}
}

func TestThreadPullRequestThreeModesRejectColdUnstartedApproval(t *testing.T) {
	for _, mode := range []domain.RunExecutionPermissionMode{domain.RunExecutionPermissionAsk, domain.RunExecutionPermissionAuto, domain.RunExecutionPermissionFull} {
		t.Run(string(mode), func(t *testing.T) {
			f := newThreadPRFixture(t, mode)
			p := f.previewAndApprove(t)
			wire := installThreadPRWire(t, &f, false, nil)
			cold := restartThreadPRRuntime(t, &f)
			if _, err := f.service.Create(t.Context(), f.threadID, ThreadPullRequestCreateRequest{Version: ThreadPullRequestVersion, OperationID: p.Preview.OperationID, ApprovalID: p.Approval.ID}); err == nil {
				t.Fatal("cold process used an old native approval")
			}
			row, _, _ := f.state.GetRemoteOperation(t.Context(), p.Preview.OperationID)
			if row.StartedAt != nil || wire.reads.Load() != 0 || wire.posts.Load() != 0 {
				t.Fatal("cold rejection started a native attempt or network request")
			}
			if _, found := cold.RunAuthorizationFence(f.runID); found {
				t.Fatal("cold execution manufactured a review fence")
			}
		})
	}
}

func TestThreadPullRequestCancellationAfterClaimKeepsFailedAttempt(t *testing.T) {
	f := newThreadPRFixture(t, domain.RunExecutionPermissionAuto)
	p := f.previewAndApprove(t)
	wire := installThreadPRWire(t, &f, false, nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f.service.store = &threadPRAfterStartStore{SQLiteStore: f.state, afterStart: cancel}
	request := ThreadPullRequestCreateRequest{Version: ThreadPullRequestVersion, OperationID: p.Preview.OperationID, ApprovalID: p.Approval.ID}
	// The caller may receive cancellation; the durable local refusal must still
	// be saved without the cancelled context, and must never become a new POST.
	_, _ = f.service.Create(ctx, f.threadID, request)
	row, found, err := f.state.GetRemoteOperation(t.Context(), request.OperationID)
	if err != nil || !found || row.StartedAt == nil || row.CompletedAt == nil || row.PullRequestNumber != 0 || wire.reads.Load() != 0 || wire.posts.Load() != 0 {
		t.Fatalf("cancelled claim lost its failed receipt: %v %#v reads=%d posts=%d", err, row, wire.reads.Load(), wire.posts.Load())
	}
	replayed, err := f.service.Create(t.Context(), f.threadID, request)
	if err != nil || replayed.State != "failed" || !replayed.Replayed || wire.posts.Load() != 0 || wire.reads.Load() != 0 {
		t.Fatalf("cancelled attempt was dispatched on retry: %v %#v", err, replayed)
	}
}

func TestThreadPullRequestLegacyIntentRemainsReadableWithoutNewAuthority(t *testing.T) {
	f := newThreadPRFixture(t, domain.RunExecutionPermissionAuto)
	modern, err := f.service.Preview(t.Context(), f.threadID, f.request)
	if err != nil {
		t.Fatal(err)
	}
	p := *modern.Preview
	legacyKey := "legacy-draft-original-key"
	p.OperationID = "thread-pr-" + threadPRKey(f.threadID, legacyKey)
	p.Draft.Marker = threadPRKey(f.threadID, legacyKey)
	p.ApprovalFingerprint = ""
	oldInput := p
	oldInput.CreatedAt = time.Time{}
	oldJSON, err := json.Marshal(oldInput) // Original format, no envelope fields.
	if err != nil {
		t.Fatal(err)
	}
	p.ApprovalFingerprint = runmutation.Fingerprint(ThreadPullRequestVersion, string(oldJSON))
	if threadPRFingerprint(p) != p.ApprovalFingerprint {
		t.Fatal("new codec changed the original legacy intent digest")
	}
	encoded, _ := json.Marshal(p)
	row, _, err := f.state.CreateRemoteOperation(t.Context(), gitmutation.RemoteRecord{ID: p.OperationID, ProtocolVersion: "repository_remote.v1", OperationKeyDigest: threadPRKey(f.threadID, legacyKey), RequestFingerprint: p.ApprovalFingerprint, RunID: p.RunID, WorkspaceID: p.WorkspaceID, Operation: gitmutation.RemoteCreatePR, SpecJSON: string(encoded), RemoteHost: "github.com", RemotePort: "443", Protocol: "https", Branch: p.Draft.HeadBranch, PreHead: p.Draft.HeadSHA, CreatedAt: p.CreatedAt})
	if err != nil {
		t.Fatal(err)
	}
	f.request.OperationKey = legacyKey
	reviewed := f.previewAndApprove(t)
	if !reviewed.Replayed || reviewed.Preview.ApprovalFingerprint != p.ApprovalFingerprint {
		t.Fatal("retained preview was rewritten")
	}
	request := ThreadPullRequestCreateRequest{Version: ThreadPullRequestVersion, OperationID: p.OperationID, ApprovalID: reviewed.Approval.ID}
	if _, err := f.service.Create(t.Context(), f.threadID, request); err == nil {
		t.Fatal("old unbound approval gained current dispatch authority")
	}
	unstarted, _, _ := f.state.GetRemoteOperation(t.Context(), row.ID)
	if unstarted.StartedAt != nil || f.remote.creates != 0 {
		t.Fatal("old unbound intent consumed its dispatch")
	}
	// Simulate the retained durable marker of a pre-upgrade in-flight attempt.
	// The observer fixture has an existing result; Create itself must only read.
	row, first, err := f.state.StartRemoteOperation(t.Context(), row.ID, row.RequestFingerprint, time.Now().UTC())
	if err != nil || !first {
		t.Fatal(err)
	}
	f.remote.created = &githubreview.PullRequest{Repository: p.Draft.Repository, Number: 23, HeadSHA: p.Draft.HeadSHA}
	cold := restartThreadPRRuntime(t, &f)
	observed, err := f.service.Create(t.Context(), f.threadID, request)
	if err != nil || observed.State != "created" || !observed.Replayed || observed.ReceiptSaved || observed.PullRequest.Number != 23 || f.remote.creates != 0 || f.remote.observes != 1 {
		t.Fatalf("legacy read-only recovery: %v %#v", err, observed)
	}
	after, _, _ := f.state.GetRemoteOperation(t.Context(), row.ID)
	beforeJSON, _ := json.Marshal(row)
	afterJSON, _ := json.Marshal(after)
	if string(beforeJSON) != string(afterJSON) {
		t.Fatal("legacy observation rewrote its immutable operation or result")
	}
	if _, found := cold.RunAuthorizationFence(f.runID); found {
		t.Fatal("legacy observation minted runtime authority")
	}
}
