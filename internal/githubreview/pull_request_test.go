package githubreview

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func draftFixtureResponse(body, head string) map[string]any {
	return map[string]any{"number": 17, "node_id": "PR_draft_17", "state": "open", "title": "中文 draft", "body": body, "draft": true, "merged": false, "updated_at": time.Date(2026, 9, 11, 4, 0, 0, 0, time.UTC), "base": map[string]any{"ref": "main", "sha": strings.Repeat("1", 40), "repo": map[string]any{"full_name": "acme/widget", "node_id": "R_widget"}}, "head": map[string]any{"ref": "feature/pr", "sha": head, "repo": map[string]any{"full_name": "acme/widget"}}}
}

func TestDraftCreateLostReplyObservedWithoutAnotherPost(t *testing.T) {
	var mu sync.Mutex
	posts := 0
	body := ""
	found := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("Authorization") == "" || r.Header.Get("X-GitHub-Api-Version") != RESTAPIVersion {
			t.Error("missing authenticated versioned request")
		}
		switch {
		case strings.Contains(r.URL.Path, "/branches/"):
			branch := strings.TrimPrefix(r.URL.Path, "/repos/acme/widget/branches/")
			sha := strings.Repeat("1", 40)
			if branch == "feature/pr" {
				sha = strings.Repeat("2", 40)
			}
			writeFixtureJSON(t, w, map[string]any{"name": branch, "commit": map[string]string{"sha": sha}})
		case r.URL.Path == "/repos/acme/widget/pulls" && r.Method == "GET":
			if r.URL.Query().Get("head") != "acme:feature/pr" || r.URL.Query().Get("base") != "main" {
				t.Error("discovery lost exact branch")
			}
			if found {
				if r.URL.Query().Get("state") != "all" {
					t.Error("recovery must also find closed PRs")
				}
				writeFixtureJSON(t, w, []any{draftFixtureResponse(body, strings.Repeat("3", 40))})
			} else {
				writeFixtureJSON(t, w, []any{})
			}
		case r.URL.Path == "/repos/acme/widget/pulls" && r.Method == "POST":
			posts++
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			if payload["draft"] != true || payload["head"] != "feature/pr" || payload["base"] != "main" || payload["title"] != "中文 draft" {
				t.Errorf("wrong native draft payload: %#v", payload)
			}
			body, _ = payload["body"].(string)
			found = true
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = connection.Close()
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, ref := newPATTestClient(t, server)
	client.network.WriteEnabled = true
	repo, _ := ParseRepository("acme/widget")
	d := PullRequestDraft{Repository: repo, Credential: ref, HeadBranch: "feature/pr", HeadSHA: strings.Repeat("2", 40), BaseBranch: "main", BaseSHA: strings.Repeat("1", 40), Title: "中文 draft", Body: "    indented markdown\nline two\n", Marker: Fingerprint("same original operation")}
	if _, err := client.CreateDraft(t.Context(), d); err == nil {
		t.Fatal("lost reply must not be successful")
	}
	for i := 0; i < 2; i++ {
		pr, ok, err := client.ObserveDraft(t.Context(), d)
		if err != nil || !ok || pr.Number != 17 || pr.HeadSHA != strings.Repeat("3", 40) {
			t.Fatalf("observation: %#v %v %v", pr, ok, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if posts != 1 || body != d.Body+"\n\n"+draftMarker(d) {
		t.Fatalf("posts=%d marker=%v", posts, strings.Contains(body, draftMarker(d)))
	}
}

func TestDraftPreflightRejectsExistingOrChangedRemoteBeforePost(t *testing.T) {
	for _, scenario := range []string{"head-drift", "base-drift", "existing", "denied"} {
		t.Run(scenario, func(t *testing.T) {
			posts := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == "POST" {
					posts++
					w.WriteHeader(http.StatusForbidden)
					return
				}
				if strings.Contains(r.URL.Path, "/branches/") {
					name := strings.TrimPrefix(r.URL.Path, "/repos/acme/widget/branches/")
					sha := strings.Repeat("1", 40)
					if name == "feature/pr" {
						sha = strings.Repeat("2", 40)
					}
					if scenario == "head-drift" && name == "feature/pr" || scenario == "base-drift" && name == "main" {
						sha = strings.Repeat("3", 40)
					}
					writeFixtureJSON(t, w, map[string]any{"name": name, "commit": map[string]string{"sha": sha}})
					return
				}
				if scenario == "existing" {
					writeFixtureJSON(t, w, []any{draftFixtureResponse("old unrelated request", strings.Repeat("2", 40))})
				} else {
					writeFixtureJSON(t, w, []any{})
				}
			}))
			defer server.Close()
			client, ref := newPATTestClient(t, server)
			client.network.WriteEnabled = true
			repo, _ := ParseRepository("acme/widget")
			d := PullRequestDraft{Repository: repo, Credential: ref, HeadBranch: "feature/pr", HeadSHA: strings.Repeat("2", 40), BaseBranch: "main", BaseSHA: strings.Repeat("1", 40), Title: "draft", Marker: Fingerprint(scenario)}
			if _, err := client.CreateDraft(t.Context(), d); err == nil {
				t.Fatal("expected exact failure")
			}
			expected := 0
			if scenario == "denied" {
				expected = 1
			}
			if posts != expected {
				t.Fatalf("posts=%d want%d", posts, expected)
			}
		})
	}
}

func TestDraftRecoveryMissingOrDuplicateMarkerDoesNotCreate(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "duplicate"}[duplicate], func(t *testing.T) {
			posts := 0
			marker := Fingerprint("original")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method != "GET" {
					posts++
					t.Error("recovery wrote remote")
				}
				items := []any{}
				if duplicate {
					body := pullRequestMarkerPrefix + marker + " -->"
					items = []any{draftFixtureResponse(body, strings.Repeat("2", 40)), draftFixtureResponse(body, strings.Repeat("2", 40))}
				}
				writeFixtureJSON(t, w, items)
			}))
			defer server.Close()
			client, ref := newPATTestClient(t, server)
			repo, _ := ParseRepository("acme/widget")
			d := PullRequestDraft{Repository: repo, Credential: ref, HeadBranch: "feature/pr", HeadSHA: strings.Repeat("2", 40), BaseBranch: "main", BaseSHA: strings.Repeat("1", 40), Title: "draft", Marker: marker}
			_, found, err := client.ObserveDraft(t.Context(), d)
			if found || duplicate != (err != nil) || posts != 0 {
				t.Fatalf("found=%v err=%v posts=%d", found, err, posts)
			}
		})
	}
}
