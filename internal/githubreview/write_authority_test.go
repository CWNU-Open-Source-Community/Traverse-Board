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
	"time"

	"cyberagent-workbench/internal/toolcontract"
)

type reviewDispatchCounts struct {
	requests, observations, mutations, redirects atomic.Int64
	revoked                                      atomic.Bool
}

func reviewWriteDispatchFixture(t *testing.T, operation WriteOperation, scenario string) (*Client, WriteSpec, WritePreview, *reviewDispatchCounts) {
	t.Helper()
	now := time.Now().UTC()
	base, head, merge := strings.Repeat("1", 40), strings.Repeat("2", 40), strings.Repeat("3", 40)
	counts := &reviewDispatchCounts{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counts.requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/repeat":
			counts.redirects.Add(1)
			w.WriteHeader(http.StatusForbidden)
		case r.URL.Path == "/repos/acme/widget/pulls/7":
			writeFixtureJSON(t, w, writePullFixture(now, base, head))
		case strings.Contains(r.URL.Path, "/compare/"):
			writeFixtureJSON(t, w, map[string]any{"merge_base_commit": map[string]any{"sha": merge}})
		case r.URL.Path == "/graphql":
			var payload struct {
				Query string `json:"query"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
				return
			}
			if strings.HasPrefix(payload.Query, "query") {
				counts.observations.Add(1)
				if scenario == "revoke-before-mutation" {
					counts.revoked.Store(true)
				}
				if scenario == "read-failure" {
					writeFixtureJSON(t, w, map[string]any{"data": nil})
					return
				}
				writeFixtureJSON(t, w, map[string]any{"data": map[string]any{"node": map[string]any{
					"id": "thread_node", "isResolved": operation == WriteUnresolve,
					"comments": map[string]any{"nodes": []any{}}}}})
				return
			}
			counts.mutations.Add(1)
			if scenario == "redirect" {
				w.Header().Set("Location", "/repeat")
				w.WriteHeader(http.StatusTemporaryRedirect)
				return
			}
			if scenario == "malformed-result" {
				writeFixtureJSON(t, w, map[string]any{"data": map[string]any{}})
				return
			}
			if operation == WriteReply {
				writeFixtureJSON(t, w, map[string]any{"data": map[string]any{
					"addPullRequestReviewThreadReply": map[string]any{"comment": map[string]any{"id": "reply_node"}}}})
			} else {
				field := "resolveReviewThread"
				if operation == WriteUnresolve {
					field = "unresolveReviewThread"
				}
				writeFixtureJSON(t, w, map[string]any{"data": map[string]any{field: map[string]any{
					"thread": map[string]any{"id": "thread_node", "isResolved": operation == WriteResolve}}}})
			}
		case strings.HasSuffix(r.URL.Path, "/reviews"):
			if r.Method == http.MethodGet {
				writeFixtureJSON(t, w, []any{})
			} else {
				counts.mutations.Add(1)
				writeFixtureJSON(t, w, map[string]any{"id": 1, "node_id": "review_node"})
			}
		case strings.HasSuffix(r.URL.Path, "/requested_reviewers"):
			if r.Method == http.MethodGet {
				writeFixtureJSON(t, w, map[string]any{"users": []any{}, "teams": []any{}})
			} else {
				counts.mutations.Add(1)
				var payload struct {
					Reviewers []string `json:"reviewers"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || len(payload.Reviewers) != 1 || payload.Reviewers[0] != "original" {
					t.Errorf("caller changed the frozen reviewer request: %v %#v", err, payload)
				}
				writeFixtureJSON(t, w, map[string]any{"requested_reviewers": []any{map[string]any{"login": "original"}}})
			}
		default:
			t.Errorf("unexpected fixture request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	client, ref := newPATTestClient(t, server)
	client.network.WriteEnabled = true
	repository, _ := ParseRepository("acme/widget")
	spec := WriteSpec{ProtocolVersion: WriteProtocolVersion, Operation: operation,
		Identity: PullRequestIdentity{Repository: repository, Number: 7, NodeID: "PR_node_7", State: "open",
			BaseRef: "main", BaseSHA: base, HeadRef: "feature", HeadSHA: head, MergeBaseSHA: merge, UpdatedAt: now},
		Credential: ref, CapabilityGeneration: strings.Repeat("a", 64), Reviewers: []string{}}
	switch operation {
	case WriteReply:
		spec.TargetID, spec.Body = "thread_node", "Exact reviewed reply"
	case WriteResolve, WriteUnresolve:
		spec.TargetID = "thread_node"
	case WriteSubmitReview:
		spec.ReviewEvent, spec.Body = "COMMENT", "Exact reviewed comment"
	case WriteRequestReviewer:
		spec.Reviewers = []string{"original"}
	}
	preview, err := NewWritePreview(spec, now)
	if err != nil {
		t.Fatal(err)
	}
	return client, spec, preview, counts
}

func TestReviewWriteDispatchBindsEveryNativeMutationAndFreezesSlices(t *testing.T) {
	for _, operation := range []WriteOperation{WriteReply, WriteResolve, WriteUnresolve, WriteSubmitReview, WriteRequestReviewer} {
		t.Run(string(operation), func(t *testing.T) {
			client, spec, preview, counts := reviewWriteDispatchFixture(t, operation, "success")
			op, err := ReviewWriteOperation(spec, preview)
			if err != nil {
				t.Fatal(err)
			}
			fingerprint, _ := toolcontract.FingerprintOperation(op)
			checks := 0
			receipt, err := client.ExecuteWrite(t.Context(), spec, preview, func(_ context.Context, actual string) error {
				checks++
				if actual != fingerprint {
					return errors.New("native inputs changed")
				}
				if operation == WriteRequestReviewer {
					spec.Reviewers[0], preview.Reviewers[0] = "changed", "changed"
				}
				return nil
			})
			if err != nil || receipt.Status != ReceiptSucceeded || counts.mutations.Load() != 1 || checks < 8 {
				t.Fatalf("native write: %v %#v mutations=%d checks=%d", err, receipt, counts.mutations.Load(), checks)
			}
		})
	}
}

func TestReviewWriteDispatchRechecksAfterCredentialResolution(t *testing.T) {
	client, spec, preview, counts := reviewWriteDispatchFixture(t, WriteReply, "success")
	revoked, checks := false, 0
	client.resolver = draftResolveHook{tokenResolver: client.resolver, afterResolve: func() { revoked = true }}
	_, err := client.ExecuteWrite(t.Context(), spec, preview, func(context.Context, string) error {
		checks++
		if revoked {
			return errors.New("revoked during credential resolution")
		}
		return nil
	})
	state, found := WriteDispatchState(err)
	if err == nil || !found || state != toolcontract.ReceiptNotDispatched || counts.requests.Load() != 0 || checks != 2 {
		t.Fatalf("credential boundary: state=%s err=%v network=%d checks=%d", state, err, counts.requests.Load(), checks)
	}
}

func TestReviewWriteDispatchPreservesUnknownAndNeverRedirects(t *testing.T) {
	for _, scenario := range []string{"revoke-before-mutation", "read-failure", "malformed-result", "redirect"} {
		t.Run(scenario, func(t *testing.T) {
			client, spec, preview, counts := reviewWriteDispatchFixture(t, WriteReply, scenario)
			_, err := client.ExecuteWrite(t.Context(), spec, preview, func(context.Context, string) error {
				if counts.revoked.Load() {
					return errors.New("revoked after GraphQL observation")
				}
				return nil
			})
			state, found := WriteDispatchState(err)
			wantState, wantMutations := toolcontract.ReceiptNotDispatched, int64(0)
			if scenario == "malformed-result" || scenario == "redirect" {
				wantState, wantMutations = toolcontract.ReceiptOutcomeUnknown, 1
			}
			if err == nil || !found || state != wantState || counts.mutations.Load() != wantMutations || counts.redirects.Load() != 0 || counts.observations.Load() != 1 {
				t.Fatalf("dispatch evidence: %v state=%s mutations=%d redirects=%d observations=%d", err, state, counts.mutations.Load(), counts.redirects.Load(), counts.observations.Load())
			}
		})
	}
}
