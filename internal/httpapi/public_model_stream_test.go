package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
)

type staticPublicModelStreamSource struct {
	snapshot application.PublicModelStreamSnapshot
	found    bool
}

func (s staticPublicModelStreamSource) PublicModelStream(
	string,
) (application.PublicModelStreamSnapshot, bool) {
	return s.snapshot, s.found
}

func TestPublicModelStreamReturnsOnlyBoundProvisionalSnapshot(t *testing.T) {
	fixture := newAPIFixture(t)
	now := time.Now().UTC()
	snapshot := application.PublicModelStreamSnapshot{
		Version: application.PublicModelStreamVersion,
		Call: application.ActiveCallInfo{
			RunID: fixture.run.ID, SessionID: fixture.run.SessionID,
			AttemptID: "attempt-public", ModelAttempt: 1, TransportAttempt: 1,
			MaxAttempts: 3, Provider: "provider", Model: "model", StartedAt: now,
			StreamChunks: 2, StreamBytes: 128,
		},
		Revision: 2, Text: "safe public preview", Provisional: true, UpdatedAt: now,
	}
	api, err := New(fixture.store, Config{AccessToken: testAccessToken,
		PublicModelStreamSource: staticPublicModelStreamSource{snapshot: snapshot, found: true}})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/runs/" + fixture.run.ID + "/active-call"
	response := performRequest(t, api, http.MethodGet, path, testAccessToken,
		"127.0.0.1:8765", "127.0.0.1:45000", nil)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "raw provider") {
		t.Fatalf("public stream response failed: status=%d body=%s", response.Code, response.Body.String())
	}
	var envelope apiTestEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	var got application.PublicModelStreamSnapshot
	if err := json.Unmarshal(envelope.Data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Text != snapshot.Text || got.Call.RunID != fixture.run.ID || !got.Provisional {
		t.Fatalf("unexpected public stream snapshot: %#v", got)
	}

	unauthorized := performRequest(t, api, http.MethodGet, path, "",
		"127.0.0.1:8765", "127.0.0.1:45000", nil)
	assertAPIError(t, unauthorized, http.StatusUnauthorized, "POLICY_DENIED")
	withQuery := performRequest(t, api, http.MethodGet, path+"?cursor=1", testAccessToken,
		"127.0.0.1:8765", "127.0.0.1:45000", nil)
	assertAPIError(t, withQuery, http.StatusBadRequest, "INVALID_ARGUMENT")
}

func TestPublicModelStreamFailsClosedWhenInactiveOrMismatched(t *testing.T) {
	fixture := newAPIFixture(t)
	path := "/api/v1/runs/" + fixture.run.ID + "/active-call"
	for name, source := range map[string]PublicModelStreamSource{
		"inactive": staticPublicModelStreamSource{},
		"mismatch": staticPublicModelStreamSource{found: true,
			snapshot: application.PublicModelStreamSnapshot{
				Version: application.PublicModelStreamVersion,
				Call: application.ActiveCallInfo{
					RunID: "run-other", SessionID: "session-other", AttemptID: "attempt-other",
					ModelAttempt: 1, TransportAttempt: 1, MaxAttempts: 1,
					Provider: "provider", Model: "model", StartedAt: time.Now().UTC(),
				},
				Revision: 1, Provisional: true, UpdatedAt: time.Now().UTC(),
			}},
	} {
		t.Run(name, func(t *testing.T) {
			api, err := New(fixture.store, Config{AccessToken: testAccessToken,
				PublicModelStreamSource: source})
			if err != nil {
				t.Fatal(err)
			}
			response := performRequest(t, api, http.MethodGet, path, testAccessToken,
				"127.0.0.1:8765", "127.0.0.1:45000", nil)
			wantStatus := http.StatusNotFound
			wantCode := "NOT_FOUND"
			if name == "mismatch" {
				wantStatus = http.StatusConflict
				wantCode = "CONFLICT"
			}
			assertAPIError(t, response, wantStatus, wantCode)
		})
	}
}
