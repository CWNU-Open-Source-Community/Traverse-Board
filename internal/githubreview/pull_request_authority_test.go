package githubreview

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"cyberagent-workbench/internal/toolcontract"
)

type draftResolveHook struct {
	tokenResolver
	afterResolve func()
}

func (r draftResolveHook) resolve(ctx context.Context, ref CredentialReference) (tokenLease, error) {
	lease, err := r.tokenResolver.resolve(ctx, ref)
	if err == nil {
		r.afterResolve()
	}
	return lease, err
}

func TestDraftDispatchGuardRunsAfterCredentialResolution(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		t.Error("revocation during credential resolution reached the network")
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	client, ref := newPATTestClient(t, server)
	client.network.WriteEnabled = true
	repo, _ := ParseRepository("acme/widget")
	draft := PullRequestDraft{Repository: repo, Credential: ref, HeadBranch: "feature/pr", HeadSHA: strings.Repeat("2", 40), BaseBranch: "main", BaseSHA: strings.Repeat("1", 40), Title: "exact draft", Marker: Fingerprint("credential-boundary")}
	op, err := DraftOperation(draft)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, _ := toolcontract.FingerprintOperation(op)
	revoked := false
	checks := 0
	client.resolver = draftResolveHook{tokenResolver: client.resolver, afterResolve: func() { revoked = true }}
	_, err = client.CreateDraft(t.Context(), draft, func(_ context.Context, actual string) error {
		checks++
		if actual != fingerprint {
			t.Errorf("guard received another native input: %s", actual)
		}
		if revoked {
			return errors.New("revoked during credential read")
		}
		return nil
	})
	state, found := DraftDispatchState(err)
	if err == nil || !found || state != toolcontract.ReceiptNotDispatched || calls.Load() != 0 || checks != 2 {
		t.Fatalf("credential dispatch boundary: state=%s err=%v network=%d checks=%d", state, err, calls.Load(), checks)
	}
}

func TestDraftDispatchFreezesInputsAndCannotFollowPostRedirect(t *testing.T) {
	var posts, redirects atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/repeat":
			redirects.Add(1)
			w.WriteHeader(http.StatusForbidden)
		case r.Method == http.MethodPost:
			posts.Add(1)
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			if payload["title"] != "original title" || payload["body"] != "original body\n\n<!-- traverse-board-pr:"+Fingerprint("frozen-draft")+" -->" {
				t.Errorf("caller mutation changed dispatched body: %#v", payload)
			}
			w.Header().Set("Location", "/repeat")
			w.WriteHeader(http.StatusTemporaryRedirect)
		case strings.Contains(r.URL.Path, "/branches/"):
			branch := strings.TrimPrefix(r.URL.Path, "/repos/acme/widget/branches/")
			sha := strings.Repeat("1", 40)
			if branch == "feature/pr" {
				sha = strings.Repeat("2", 40)
			}
			writeFixtureJSON(t, w, map[string]any{"name": branch, "commit": map[string]string{"sha": sha}})
		default:
			writeFixtureJSON(t, w, []any{})
		}
	}))
	defer server.Close()
	client, ref := newPATTestClient(t, server)
	client.network.WriteEnabled = true
	repo, _ := ParseRepository("acme/widget")
	draft := PullRequestDraft{Repository: repo, Credential: ref, HeadBranch: "feature/pr", HeadSHA: strings.Repeat("2", 40), BaseBranch: "main", BaseSHA: strings.Repeat("1", 40), Title: "original title", Body: "original body", Marker: Fingerprint("frozen-draft")}
	op, err := DraftOperation(draft)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, _ := toolcontract.FingerprintOperation(op)
	_, err = client.CreateDraft(t.Context(), draft, func(_ context.Context, actual string) error {
		if actual != fingerprint {
			t.Errorf("guard lost frozen native inputs: %s", actual)
		}
		draft.Title = "changed caller title"
		draft.Body = "changed caller body"
		return nil
	})
	state, found := DraftDispatchState(err)
	if err == nil || !found || state != toolcontract.ReceiptOutcomeUnknown || posts.Load() != 1 || redirects.Load() != 0 {
		t.Fatalf("redirect repeated or erased POST: state=%s err=%v posts=%d redirects=%d", state, err, posts.Load(), redirects.Load())
	}
}
