package githubreview

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestReadSnapshotRechecksIdentityAfterRemoteCollection(t *testing.T) {
	for _, scenario := range []string{"head_changed", "base_changed", "final_read_failed"} {
		t.Run(scenario, func(t *testing.T) {
			pullReads := 0
			base := strings.Repeat("1", 40)
			head := strings.Repeat("2", 40)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.URL.Path == "/repos/acme/widget/pulls/17":
					pullReads++
					if pullReads > 1 && scenario == "final_read_failed" {
						w.WriteHeader(503)
						_, _ = w.Write([]byte(`{"message":"unavailable"}`))
						return
					}
					value := draftFixtureResponse("body", head)
					if pullReads > 1 {
						if scenario == "head_changed" {
							value["head"].(map[string]any)["sha"] = strings.Repeat("3", 40)
						}
						if scenario == "base_changed" {
							value["base"].(map[string]any)["sha"] = strings.Repeat("4", 40)
						}
					}
					writeFixtureJSON(t, w, value)
				case strings.Contains(r.URL.Path, "/compare/"):
					writeFixtureJSON(t, w, map[string]any{"merge_base_commit": map[string]string{"sha": base}})
				case strings.HasSuffix(r.URL.Path, "/files"), strings.HasSuffix(r.URL.Path, "/reviews"), strings.HasSuffix(r.URL.Path, "/comments"):
					writeFixtureJSON(t, w, []any{})
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client, ref := newPATTestClient(t, server)
			repo, _ := ParseRepository("acme/widget")
			snapshot, err := client.ReadSnapshot(t.Context(), SnapshotRequest{Repository: repo, Number: 17, Credential: ref, Capability: testCapability(repo, ref, time.Now().UTC(), false)})
			if scenario == "final_read_failed" {
				if err == nil || pullReads != 2 || snapshot.ID != "" {
					t.Fatalf("unverified current identity accepted: %#v %v", snapshot, err)
				}
				return
			}
			if err != nil || pullReads != 2 {
				t.Fatalf("snapshot %s %d %v", snapshot.State, pullReads, err)
			}
			if snapshot.Identity.HeadSHA != head || snapshot.Identity.BaseSHA != base {
				t.Fatal("collection relabeled with later SHA")
			}
			if scenario != "final_read_failed" && snapshot.State != EvidenceStale {
				t.Fatalf("drift accepted: %s", snapshot.State)
			}
		})
	}
}
