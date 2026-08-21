package githubreview

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/credential"
)

func TestReadSnapshotCollectsBoundedUntrustedReviewAndCIEvidence(t *testing.T) {
	const (
		baseSHA  = "1111111111111111111111111111111111111111"
		headSHA  = "2222222222222222222222222222222222222222"
		mergeSHA = "3333333333333333333333333333333333333333"
	)
	now := time.Date(2026, 8, 21, 5, 6, 7, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer github_pat_abcdefghijklmnopqrstuvwxyz0123456789" {
			http.Error(writer, "missing auth", http.StatusUnauthorized)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("X-RateLimit-Limit", "5000")
		writer.Header().Set("X-RateLimit-Remaining", "4990")
		switch request.URL.Path {
		case "/repos/acme/widget/pulls/7":
			writeFixtureJSON(t, writer, map[string]any{"number": 7, "node_id": "PR_node_7",
				"state": "open", "title": "Review <b>me</b>",
				"body":  "untrusted https://evil.example github_pat_abcdefghijklmnopqrstuvwxyz9876543210",
				"draft": false, "merged": false, "updated_at": now,
				"user": map[string]any{"login": "octocat"},
				"base": map[string]any{"ref": "main", "sha": baseSHA,
					"repo": map[string]any{"node_id": "R_node", "full_name": "acme/widget", "private": true}},
				"head": map[string]any{"ref": "feature", "sha": headSHA,
					"repo": map[string]any{"full_name": "fork/widget"}}})
		case "/repos/acme/widget/compare/" + baseSHA + "..." + headSHA:
			writeFixtureJSON(t, writer, map[string]any{"merge_base_commit": map[string]any{"sha": mergeSHA}})
		case "/repos/acme/widget/pulls/7/files":
			writeFixtureJSON(t, writer, []map[string]any{{"sha": "4444444444444444444444444444444444444444",
				"filename": "internal/app.go", "status": "modified", "additions": 2,
				"deletions": 1, "changes": 3, "blob_url": "https://github.com/acme/widget/blob/x/internal/app.go",
				"raw_url": "https://raw.githubusercontent.com/acme/widget/x/internal/app.go",
				"patch":   "@@ -1 +1 @@\n-unsafe\n+safe\x1b[31m"}})
		case "/repos/acme/widget/pulls/7/reviews":
			writeFixtureJSON(t, writer, []map[string]any{{"id": 8, "node_id": "review_node",
				"body": "please fix", "state": "CHANGES_REQUESTED", "commit_id": headSHA,
				"submitted_at": now, "user": map[string]any{"login": "reviewer"}}})
		case "/repos/acme/widget/pulls/7/comments":
			writeFixtureJSON(t, writer, []map[string]any{{"id": 9, "node_id": "comment_node",
				"body": "line comment", "path": "internal/app.go", "side": "RIGHT", "line": 2,
				"original_line": 2, "commit_id": headSHA, "original_commit_id": headSHA,
				"html_url":   "https://github.com/acme/widget/pull/7#discussion_r9",
				"created_at": now, "updated_at": now,
				"user": map[string]any{"login": "reviewer"}}})
		case "/graphql":
			writeFixtureJSON(t, writer, map[string]any{"data": map[string]any{
				"repository": map[string]any{"pullRequest": map[string]any{
					"reviewThreads": map[string]any{"nodes": []map[string]any{{"id": "thread_node",
						"isResolved": false, "isOutdated": false, "path": "internal/app.go",
						"line": 2, "startLine": 2, "diffSide": "RIGHT", "startDiffSide": "RIGHT",
						"comments": map[string]any{"nodes": []map[string]any{{"id": "comment_node",
							"databaseId": 9, "body": "line comment", "url": "https://github.com/acme/widget/pull/7#discussion_r9",
							"createdAt": now, "updatedAt": now, "path": "internal/app.go", "line": 2,
							"originalLine": 2, "author": map[string]any{"login": "reviewer"},
							"commit": map[string]any{"oid": headSHA}, "originalCommit": map[string]any{"oid": headSHA}}},
							"pageInfo": map[string]any{"hasNextPage": false, "endCursor": ""}}}},
						"pageInfo": map[string]any{"hasNextPage": false, "endCursor": ""}}}},
				"rateLimit": map[string]any{"limit": 5000, "remaining": 4980, "used": 20,
					"resetAt": now.Add(time.Hour), "resource": "graphql"}}})
		case "/repos/acme/widget/pulls/7/requested_reviewers":
			writeFixtureJSON(t, writer, map[string]any{"users": []map[string]any{{"login": "alice"}},
				"teams": []map[string]any{{"slug": "security"}}})
		case "/repos/acme/widget/commits/" + headSHA + "/check-suites":
			writeFixtureJSON(t, writer, map[string]any{"total_count": 1,
				"check_suites": []map[string]any{{"id": 20, "node_id": "suite_node", "status": "completed",
					"conclusion": "failure", "head_sha": headSHA, "created_at": now, "updated_at": now,
					"app": map[string]any{"name": "GitHub Actions"}}}})
		case "/repos/acme/widget/commits/" + headSHA + "/check-runs":
			writeFixtureJSON(t, writer, map[string]any{"total_count": 1,
				"check_runs": []map[string]any{{"id": 21, "node_id": "check_node", "name": "test",
					"status": "completed", "conclusion": "failure", "head_sha": headSHA,
					"details_url": "https://github.com/acme/widget/actions/runs/44",
					"started_at":  now, "completed_at": now.Add(time.Minute),
					"output": map[string]any{"title": "failed", "summary": "bad [link](https://evil.example)",
						"text": "::error::not a command"}}}})
		case "/repos/acme/widget/actions/runs":
			if request.URL.Query().Get("head_sha") != headSHA {
				http.Error(writer, "wrong head", http.StatusBadRequest)
				return
			}
			writeFixtureJSON(t, writer, map[string]any{"total_count": 1,
				"workflow_runs": []map[string]any{{"id": 44, "head_sha": headSHA,
					"status": "completed", "conclusion": "failure"}}})
		case "/repos/acme/widget/actions/runs/44/jobs":
			writeFixtureJSON(t, writer, map[string]any{"total_count": 1,
				"jobs": []map[string]any{{"id": 45, "run_id": 44, "name": "go test",
					"status": "completed", "conclusion": "failure", "head_sha": headSHA,
					"html_url":   "https://github.com/acme/widget/actions/runs/44/job/45",
					"started_at": now, "completed_at": now.Add(time.Minute),
					"steps": []map[string]any{{"number": 1, "name": "test", "status": "completed",
						"conclusion": "failure", "started_at": now, "completed_at": now.Add(time.Minute)}}}}})
		case "/repos/acme/widget/actions/runs/44/artifacts":
			writeFixtureJSON(t, writer, map[string]any{"total_count": 1,
				"artifacts": []map[string]any{{"id": 46, "name": "report", "size_in_bytes": 123,
					"expired": false, "digest": "sha256:abc", "created_at": now,
					"expires_at": now.Add(24 * time.Hour)}}})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, ref := newPATTestClient(t, server)
	repo, err := ParseRepository("acme/widget")
	if err != nil {
		t.Fatal(err)
	}
	capability := testCapability(repo, ref, now, false)
	snapshot, err := client.ReadSnapshot(context.Background(), SnapshotRequest{
		Repository: repo, Number: 7, Credential: ref, Capability: capability})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Identity.HeadSHA != headSHA || snapshot.Identity.MergeBaseSHA != mergeSHA ||
		!snapshot.Identity.Fork || len(snapshot.Threads) != 1 || snapshot.Threads[0].Resolved ||
		len(snapshot.Reviews) != 1 || snapshot.Reviews[0].State != "CHANGES_REQUESTED" ||
		len(snapshot.CheckRuns) != 1 || len(snapshot.Jobs) != 1 || len(snapshot.Artifacts) != 1 ||
		snapshot.Jobs[0].LogState != EvidenceUnavailable || snapshot.State != EvidencePartial {
		t.Fatalf("snapshot evidence is incomplete: %#v", snapshot)
	}
	encoded := string(mustJSON(t, snapshot))
	if strings.Contains(encoded, "github_pat_") || strings.Contains(encoded, "evil.example") ||
		strings.Contains(encoded, "\x1b") || !strings.Contains(encoded, "[REDACTED:github-token]") {
		t.Fatalf("snapshot leaked unsafe remote data: %s", encoded)
	}
	if err := snapshot.Validate(); err != nil {
		t.Fatalf("snapshot failed its durable contract: %v", err)
	}
}

func TestReviewThreadsQueryHasBalancedDelimiters(t *testing.T) {
	for _, pair := range [][2]string{{"{", "}"}, {"(", ")"}} {
		if strings.Count(reviewThreadsQuery, pair[0]) != strings.Count(reviewThreadsQuery, pair[1]) {
			t.Fatalf("review thread GraphQL query has unbalanced %s%s delimiters", pair[0], pair[1])
		}
	}
	if strings.Contains(reviewThreadsQuery, "resetAt resource") {
		t.Fatal("review thread GraphQL query requested the REST-only rate-limit resource field")
	}
}

func TestSanitizeZipLogBoundsAndRedacts(t *testing.T) {
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	entry, err := archive.Create("1_test.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = entry.Write([]byte("\x1b[31mfailed github_pat_abcdefghijklmnopqrstuvwxyz0123456789 https://evil.example"))
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	evidence, err := sanitizeZipLog(buffer.Bytes())
	if err != nil || !evidence.Untrusted || strings.Contains(evidence.Text, "github_pat_") ||
		strings.Contains(evidence.Text, "https://") || strings.Contains(evidence.Text, "\x1b") {
		t.Fatalf("zip log was not sanitized: %v %#v", err, evidence)
	}
}

func TestQualifyDiagnosesReadOnlyCredentialWithoutExposingScopes(t *testing.T) {
	now := time.Date(2026, 8, 21, 1, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/user":
			writeFixtureJSON(t, writer, map[string]any{"login": "octocat", "id": 1, "node_id": "U_1"})
		case "/repos/acme/widget":
			writeFixtureJSON(t, writer, map[string]any{"id": 2, "node_id": "R_2", "name": "widget",
				"full_name": "acme/widget", "private": true, "owner": map[string]any{"login": "acme"},
				"permissions": map[string]any{"admin": false, "maintain": false, "push": false,
					"triage": true, "pull": true}})
		case "/repos/acme/widget/pulls/7":
			writeFixtureJSON(t, writer, map[string]any{"number": 7})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, ref := newPATTestClient(t, server)
	client.now = func() time.Time { return now }
	repo, _ := ParseRepository("acme/widget")
	qualified, err := client.Qualify(context.Background(), repo, 7, ref)
	if err != nil || !qualified.Eligible || qualified.Capability.Reply ||
		qualified.Capability.Push || qualified.Capability.Permissions["pull_requests"] != "read" {
		t.Fatalf("read-only qualification failed: %v %#v", err, qualified)
	}
	if strings.Contains(string(mustJSON(t, qualified)), "github_pat_") {
		t.Fatal("qualification leaked the credential")
	}
}

func TestQualifyGitHubAppRequiresExactInstallationRepositoryAndPermissions(t *testing.T) {
	now := time.Date(2026, 8, 21, 1, 30, 0, 0, time.UTC)
	includeRepository := true
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/user":
			writeFixtureJSON(t, writer, map[string]any{"login": "octocat", "id": 1, "node_id": "U_1"})
		case "/repos/acme/widget":
			writeFixtureJSON(t, writer, map[string]any{"id": 2, "node_id": "R_2", "name": "widget",
				"full_name": "acme/widget", "private": true, "owner": map[string]any{"login": "acme"},
				"permissions": map[string]any{"admin": false, "maintain": false, "push": true,
					"triage": true, "pull": true}})
		case "/user/installations":
			writeFixtureJSON(t, writer, map[string]any{"total_count": 1,
				"installations": []any{map[string]any{"id": 42,
					"account": map[string]any{"login": "acme"},
					"permissions": map[string]any{"metadata": "read", "contents": "read",
						"pull_requests": "write", "checks": "read", "actions": "read"}}}})
		case "/user/installations/42/repositories":
			repositories := []any{}
			if includeRepository {
				repositories = append(repositories, map[string]any{"node_id": "R_2", "full_name": "acme/widget"})
			}
			writeFixtureJSON(t, writer, map[string]any{"total_count": len(repositories),
				"repositories": repositories})
		case "/repos/acme/widget/pulls/7":
			writeFixtureJSON(t, writer, map[string]any{"number": 7})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	store := credential.NewMemoryStore()
	manager, err := NewAuthManagerForTest(store, "Iv1.fixture-client", server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	manager.now = func() time.Time { return now }
	ref := CredentialReference{Name: "github-review-app", Kind: AuthGitHubAppDevice}
	if err := manager.storeBundle(t.Context(), ref, tokenBundle{ProtocolVersion: DeviceFlowProtocolVersion,
		AccessToken: "ghu_app_abcdefghijklmnopqrstuvwxyz", TokenType: "bearer",
		ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	client, err := NewClientForTest(manager, server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	client.now = func() time.Time { return now }
	repo, _ := ParseRepository("acme/widget")
	readOnly, err := client.Qualify(t.Context(), repo, 7, ref)
	if err != nil || !readOnly.Eligible || readOnly.Capability.Reply {
		t.Fatalf("default read-only connection admitted write-back: %v %#v", err, readOnly)
	}
	client.network.WriteEnabled = true
	qualified, err := client.Qualify(t.Context(), repo, 7, ref)
	if err != nil || !qualified.Eligible || qualified.Capability.InstallationID != 42 ||
		!qualified.Capability.Reply || qualified.Capability.Push ||
		qualified.Capability.Logs ||
		qualified.Capability.Permissions["pull_requests"] != "write" {
		t.Fatalf("exact GitHub App qualification failed: %v %#v", err, qualified)
	}
	client.network.AllowedLogHosts = []string{"pipelines.actions.githubusercontent.com"}
	withLogHost, err := client.Qualify(t.Context(), repo, 7, ref)
	if err != nil || !withLogHost.Eligible || !withLogHost.Capability.Logs {
		t.Fatalf("reviewed Actions log host was not admitted: %v %#v", err, withLogHost)
	}
	includeRepository = false
	missing, err := client.Qualify(t.Context(), repo, 7, ref)
	if err != nil || missing.Eligible || missing.Capability.InstallationID != 0 ||
		missing.Capability.Read {
		t.Fatalf("unselected repository was not rejected: %v %#v", err, missing)
	}
}

func newPATTestClient(t *testing.T, server *httptest.Server) (*Client, CredentialReference) {
	t.Helper()
	store := credential.NewMemoryStore()
	ref := CredentialReference{Name: "github-review-pat", Kind: AuthFineGrainedPAT}
	if err := store.Put(context.Background(), ref.Name,
		"github_pat_abcdefghijklmnopqrstuvwxyz0123456789"); err != nil {
		t.Fatal(err)
	}
	manager, err := NewAuthManager(store, "Iv1.fixture-client")
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewClientForTest(manager, server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	return client, ref
}

func testCapability(repo RepositoryIdentity, ref CredentialReference, now time.Time,
	logs bool,
) CapabilitySnapshot {
	capability := CapabilitySnapshot{ProtocolVersion: CapabilityProtocolVersion,
		APIHost: "api.github.com", APIVersion: RESTAPIVersion, AccountLogin: "octocat",
		Repository: repo, Credential: ref,
		Permissions: map[string]string{"metadata": "read", "contents": "read",
			"pull_requests": "read", "checks": "read", "actions": "read"},
		Read: true, Logs: logs, CapturedAt: now}
	capability.Generation = capabilityFingerprint(capability)
	return capability
}

func writeFixtureJSON(t *testing.T, writer http.ResponseWriter, value any) {
	t.Helper()
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		t.Fatal(err)
	}
}
