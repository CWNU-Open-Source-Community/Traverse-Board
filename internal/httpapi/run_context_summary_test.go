package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"cyberagent-workbench/internal/contextmgr"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/session"
	"cyberagent-workbench/internal/store"
)

func TestRunContextSummaryReadOnlyCurrentChainAndAbsence(t *testing.T) {
	f := newAPIFixture(t)
	path := "/api/v1/runs/" + f.run.ID + "/context-summary"
	var empty RunContextSummaryView
	decodeData(t, f.get(t, path), &empty)
	thread, err := f.store.GetThreadByRun(t.Context(), f.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if empty.RunID != f.run.ID || empty.ThreadID != thread.ID || empty.SessionID != f.run.SessionID ||
		empty.WorkspaceID != f.workspace.ID || empty.CurrentSummary != nil || empty.InheritedContext != nil || empty.CapabilityGrant {
		t.Fatalf("unexpected empty projection: %#v", empty)
	}
	first, err := f.store.SaveContextSummary(t.Context(), contextmgr.Summary{TaskID: f.run.SessionID, WorkspaceID: f.workspace.ID,
		Content: "First goal: preserve the interface.", SourceMessageCount: 22, PreservedMessageCount: 4})
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.store.SaveContextSummary(t.Context(), contextmgr.Summary{TaskID: f.run.SessionID, WorkspaceID: f.workspace.ID,
		PreviousSummaryID: first.ID, Content: "Later correction: keep Chinese examples.", SourceMessageCount: 26, PreservedMessageCount: 4})
	if err != nil {
		t.Fatal(err)
	}
	messagesBefore, err := f.store.ListSessionMessages(t.Context(), f.run.SessionID, true)
	if err != nil {
		t.Fatal(err)
	}
	runBefore, err := f.store.GetRun(t.Context(), f.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var got RunContextSummaryView
	for range 2 {
		decodeData(t, f.get(t, path), &got)
		if got.CurrentSummary == nil || got.CurrentSummary.ID != second.ID || got.CurrentSummary.PreviousSummaryID != first.ID ||
			got.CurrentSummary.Content != second.Content || got.CurrentSummary.ContentSHA256 != second.ContentSHA256 ||
			got.CurrentSummary.CompactedMessageCount != second.CompactedMessageCount ||
			got.CurrentSummary.SourceMessageCount != second.SourceMessageCount ||
			got.CurrentSummary.PreservedMessageCount != second.PreservedMessageCount ||
			got.CurrentSummary.ContentRedacted || got.CurrentSummary.ContentTruncated || got.CapabilityGrant {
			t.Fatalf("summary lost exact stored identity: %#v", got)
		}
	}
	messagesAfter, err := f.store.ListSessionMessages(t.Context(), f.run.SessionID, true)
	if err != nil {
		t.Fatal(err)
	}
	runAfter, err := f.store.GetRun(t.Context(), f.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	after, found, err := f.store.LatestContextSummary(t.Context(), f.run.SessionID)
	if err != nil || !found || !reflect.DeepEqual(second, after) || !reflect.DeepEqual(messagesBefore, messagesAfter) || !reflect.DeepEqual(runBefore, runAfter) {
		t.Fatalf("GET changed history, summary or Run: found=%t err=%v", found, err)
	}
	unauthorized := performRequest(t, f.api, http.MethodGet, path, "", "localhost", "127.0.0.1:2345", nil)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("missing read authentication accepted: %d", unauthorized.Code)
	}
	post := performRequest(t, f.api, http.MethodPost, path, testControlToken, "localhost", "127.0.0.1:2345", strings.NewReader(`{}`))
	if post.Code == http.StatusOK || post.Code == http.StatusCreated || post.Code == http.StatusAccepted {
		t.Fatalf("read projection accepted a write: %d", post.Code)
	}
}

type contextSummaryProjectionStore struct {
	*store.SQLiteStore
	summary contextmgr.Summary
	run     *domain.Run
}

func (s *contextSummaryProjectionStore) LatestContextSummary(context.Context, string) (contextmgr.Summary, bool, error) {
	return s.summary, s.summary.ID != 0, nil
}

func (s *contextSummaryProjectionStore) GetRun(ctx context.Context, id string) (domain.Run, error) {
	if s.run != nil && s.run.ID == id {
		return *s.run, nil
	}
	return s.SQLiteStore.GetRun(ctx, id)
}

func TestRunContextSummaryRejectsForeignSessionAndAuthority(t *testing.T) {
	f := newAPIFixture(t)
	path := "/api/v1/runs/" + f.run.ID + "/context-summary"
	summary := contextmgr.Summary{ID: 10, TaskID: "another-session", WorkspaceID: f.workspace.ID,
		ProtocolVersion: contextmgr.LegacyHandoffProtocolVersion, Content: "foreign private content", SourceMessageCount: 5,
		PreservedMessageCount: 1, CreatedAt: time.Now().UTC()}
	stub := &contextSummaryProjectionStore{SQLiteStore: f.store, summary: summary}
	f.api.store = stub
	response := f.get(t, path)
	if response.Code != http.StatusConflict || strings.Contains(response.Body.String(), summary.Content) {
		t.Fatalf("foreign summary exposed: %d %s", response.Code, response.Body.String())
	}
	stub.summary = contextmgr.Summary{}
	run := f.run
	inherited, err := contextmgr.SealContinuitySnapshot(contextmgr.ContinuitySnapshot{SourceRunID: run.ID,
		SourceSessionID: run.SessionID, WorkspaceID: f.workspace.ID, SummaryID: 9, SummaryContent: "Earlier exact constraint",
		SummaryContentSHA256: session.ContentSHA256("Earlier exact constraint")})
	if err != nil {
		t.Fatal(err)
	}
	run.Config.ContinuityContext, _ = json.Marshal(inherited)
	run.Config.ContinuityContextFingerprint = inherited.Fingerprint
	stub.run = &run
	var got RunContextSummaryView
	decodeData(t, f.get(t, path), &got)
	if got.CurrentSummary != nil || got.InheritedContext == nil || got.InheritedContext.Fingerprint != inherited.Fingerprint ||
		got.InheritedContext.SourceSessionID != inherited.SourceSessionID || got.InheritedContext.SummaryContent != inherited.SummaryContent ||
		got.InheritedContext.SummaryContentSHA256 != inherited.SummaryContentSHA256 || got.CapabilityGrant {
		t.Fatalf("inherited metadata not bound: %#v", got)
	}
	run.Config.ContinuityContextFingerprint = strings.Repeat("f", 64)
	if response := f.get(t, path); response.Code != http.StatusConflict {
		t.Fatalf("mismatched inherited fingerprint accepted: %d", response.Code)
	}
	run.Config.ContinuityContextFingerprint = inherited.Fingerprint
	var raw map[string]any
	_ = json.Unmarshal(run.Config.ContinuityContext, &raw)
	authority := raw["authority"].(map[string]any)
	// An existing authority field must not be restored by a display endpoint.
	for key := range authority {
		authority[key] = true
		break
	}
	run.Config.ContinuityContext, _ = json.Marshal(raw)
	if response := f.get(t, path); response.Code == http.StatusOK || strings.Contains(response.Body.String(), inherited.SummaryContent) {
		t.Fatalf("invalid inherited authority displayed: %d %s", response.Code, response.Body.String())
	}
}

func TestRunContextSummaryLegacyPublicTextIsBoundedWithoutChangingOriginalHash(t *testing.T) {
	f := newAPIFixture(t)
	secret := "sk-" + strings.Repeat("x", 40)
	content := "original public text " + secret + strings.Repeat("文", 8000)
	stored := contextmgr.Summary{ID: 10, TaskID: f.run.SessionID, WorkspaceID: f.workspace.ID,
		ProtocolVersion: contextmgr.LegacyHandoffProtocolVersion, Content: content, SourceMessageCount: 8,
		PreservedMessageCount: 2, CreatedAt: time.Now().UTC()}
	f.api.store = &contextSummaryProjectionStore{SQLiteStore: f.store, summary: stored}
	var got RunContextSummaryView
	decodeData(t, f.get(t, "/api/v1/runs/"+f.run.ID+"/context-summary"), &got)
	if got.CurrentSummary == nil || !got.CurrentSummary.ContentRedacted || !got.CurrentSummary.ContentTruncated ||
		got.CurrentSummary.ContentSHA256 != session.ContentSHA256(content) ||
		!utf8.ValidString(got.CurrentSummary.Content) || len(got.CurrentSummary.Content) > 16*1024 || strings.Contains(got.CurrentSummary.Content, secret) {
		t.Fatalf("unbounded, unredacted or incorrectly identified legacy text: %#v", got)
	}
	if stored.Content != content {
		t.Fatal("public projection modified the original")
	}
}
