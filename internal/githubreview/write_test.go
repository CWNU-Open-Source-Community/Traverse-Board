package githubreview

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestExecuteWriteUsesExactPreviewAndRecoversReplyWithoutDuplicate(t *testing.T) {
	const (
		baseSHA  = "1111111111111111111111111111111111111111"
		headSHA  = "2222222222222222222222222222222222222222"
		mergeSHA = "3333333333333333333333333333333333333333"
	)
	now := time.Date(2026, 8, 21, 6, 0, 0, 0, time.UTC)
	var mutations atomic.Int32
	var mu sync.Mutex
	storedBody := ""
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/repos/acme/widget/pulls/7":
			writeFixtureJSON(t, writer, writePullFixture(now, baseSHA, headSHA))
		case "/repos/acme/widget/compare/" + baseSHA + "..." + headSHA:
			writeFixtureJSON(t, writer, map[string]any{"merge_base_commit": map[string]any{"sha": mergeSHA}})
		case "/graphql":
			var payload struct {
				Query     string         `json:"query"`
				Variables map[string]any `json:"variables"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(payload.Query, "query($id") {
				mu.Lock()
				body := storedBody
				mu.Unlock()
				comments := []map[string]any{}
				if body != "" {
					comments = append(comments, map[string]any{"id": "reply_node", "body": body,
						"url": "https://github.com/acme/widget/pull/7#discussion_r99"})
				}
				writeFixtureJSON(t, writer, map[string]any{"data": map[string]any{"node": map[string]any{
					"id": "thread_node", "isResolved": false,
					"comments": map[string]any{"nodes": comments}}}})
				return
			}
			if strings.Contains(payload.Query, "addPullRequestReviewThreadReply") {
				mutations.Add(1)
				mu.Lock()
				storedBody, _ = payload.Variables["body"].(string)
				mu.Unlock()
				writeFixtureJSON(t, writer, map[string]any{"data": map[string]any{
					"addPullRequestReviewThreadReply": map[string]any{"comment": map[string]any{
						"id": "reply_node", "databaseId": 99,
						"url": "https://github.com/acme/widget/pull/7#discussion_r99"}}}})
				return
			}
			http.Error(writer, "unexpected GraphQL", http.StatusBadRequest)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, ref := newPATTestClient(t, server)
	client.network.WriteEnabled = true
	client.now = func() time.Time { return now }
	repo, _ := ParseRepository("acme/widget")
	identity := PullRequestIdentity{Repository: repo, Number: 7, NodeID: "PR_node_7",
		State: "open", Fork: true, BaseRef: "main", BaseSHA: baseSHA,
		HeadRef: "feature", HeadSHA: headSHA, MergeBaseSHA: mergeSHA, UpdatedAt: now}
	spec := WriteSpec{ProtocolVersion: WriteProtocolVersion, Operation: WriteReply,
		Identity: identity, Credential: ref, CapabilityGeneration: strings.Repeat("a", 64),
		TargetID: "thread_node", Body: "Fixed in the latest commit.", Reviewers: []string{},
		LocalChangeSummary: "one bounded hunk", ValidationSummary: "go test ./internal/githubreview passed"}
	preview, err := NewWritePreview(spec, now)
	if err != nil {
		t.Fatal(err)
	}
	first, err := client.ExecuteWrite(context.Background(), spec, preview)
	if err != nil || first.Status != ReceiptSucceeded || first.Recovered ||
		mutations.Load() != 1 || first.ResultID != "reply_node" {
		t.Fatalf("first GitHub write failed: %v %#v mutations=%d", err, first, mutations.Load())
	}
	second, err := client.ExecuteWrite(context.Background(), spec, preview)
	if err != nil || second.Status != ReceiptRecovered || !second.Recovered || mutations.Load() != 1 {
		t.Fatalf("GitHub write was duplicated instead of recovered: %v %#v mutations=%d",
			err, second, mutations.Load())
	}
	mu.Lock()
	body := storedBody
	mu.Unlock()
	if !strings.Contains(body, preview.IdempotencyMarker) || strings.Contains(body, "github_pat_") {
		t.Fatalf("outbound body has invalid idempotency evidence: %q", body)
	}
}

func TestExecuteWriteRejectsRemoteHeadDriftBeforeMutation(t *testing.T) {
	base := strings.Repeat("1", 40)
	head := strings.Repeat("2", 40)
	driftedHead := strings.Repeat("4", 40)
	merge := strings.Repeat("3", 40)
	now := time.Date(2026, 8, 21, 7, 0, 0, 0, time.UTC)
	var mutations atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/repos/acme/widget/pulls/7":
			writeFixtureJSON(t, writer, writePullFixture(now, base, driftedHead))
		case "/repos/acme/widget/compare/" + base + "..." + driftedHead:
			writeFixtureJSON(t, writer, map[string]any{"merge_base_commit": map[string]any{"sha": merge}})
		case "/graphql":
			mutations.Add(1)
			writeFixtureJSON(t, writer, map[string]any{"data": map[string]any{}})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, ref := newPATTestClient(t, server)
	client.network.WriteEnabled = true
	client.now = func() time.Time { return now }
	repo, _ := ParseRepository("acme/widget")
	identity := PullRequestIdentity{Repository: repo, Number: 7, NodeID: "PR_node_7",
		State: "open", Fork: true, BaseRef: "main", BaseSHA: base,
		HeadRef: "feature", HeadSHA: head, MergeBaseSHA: merge, UpdatedAt: now}
	spec := WriteSpec{ProtocolVersion: WriteProtocolVersion, Operation: WriteResolve,
		Identity: identity, Credential: ref, CapabilityGeneration: strings.Repeat("a", 64),
		TargetID: "thread_node", Reviewers: []string{}}
	preview, err := NewWritePreview(spec, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ExecuteWrite(context.Background(), spec, preview)
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != FailureDrift || mutations.Load() != 0 {
		t.Fatalf("remote head drift did not fail before mutation: %v mutations=%d", err, mutations.Load())
	}
}

func TestWriteSpecRejectsSecretAndReservedMarker(t *testing.T) {
	repo, _ := ParseRepository("acme/widget")
	now := time.Now().UTC()
	identity := PullRequestIdentity{Repository: repo, Number: 1, NodeID: "PR_1",
		State: "open", BaseRef: "main", BaseSHA: strings.Repeat("1", 40),
		HeadRef: "head", HeadSHA: strings.Repeat("2", 40),
		MergeBaseSHA: strings.Repeat("3", 40), UpdatedAt: now}
	base := WriteSpec{ProtocolVersion: WriteProtocolVersion, Operation: WriteReply,
		Identity: identity, Credential: CredentialReference{Name: "github", Kind: AuthFineGrainedPAT},
		CapabilityGeneration: strings.Repeat("a", 64), TargetID: "thread", Reviewers: []string{}}
	for _, body := range []string{"github_pat_abcdefghijklmnopqrstuvwxyz0123456789",
		"<!-- prayu-github-review:forged -->"} {
		spec := base
		spec.Body = body
		if _, err := NewWritePreview(spec, now); err == nil {
			t.Fatalf("unsafe GitHub write body was accepted: %q", body)
		}
	}
}

func writePullFixture(now time.Time, base, head string) map[string]any {
	return map[string]any{"number": 7, "node_id": "PR_node_7", "state": "open",
		"title": "title", "body": "body", "draft": false, "merged": false,
		"updated_at": now, "user": map[string]any{"login": "octocat"},
		"base": map[string]any{"ref": "main", "sha": base,
			"repo": map[string]any{"node_id": "R_node", "full_name": "acme/widget", "private": false}},
		"head": map[string]any{"ref": "feature", "sha": head,
			"repo": map[string]any{"full_name": "fork/widget"}}}
}
