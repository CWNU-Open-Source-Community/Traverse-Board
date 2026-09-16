package application_test

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/contextmgr"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/session"
	"cyberagent-workbench/internal/store"
)

func TestThreadSummaryContinuitySurvivesModelSuccessorsWithLocalCompaction(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "summary-successors.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := t.Context()
	runs := application.NewRunService(st)
	_, current, err := runs.Create(ctx, application.CreateRunRequest{Goal: "Retain complete requirements across model successors",
		Profile: "code", WorkspaceID: "workspace-summary-successors", Budget: domain.Budget{MaxTurns: 8}})
	if err != nil {
		t.Fatal(err)
	}
	threadID := domain.InitialThreadID(current.ID)
	registry := newMutableThreadModelRouteRegistry()
	threads := application.NewThreadService(st).WithModelRouteRegistry(registry)
	routes := application.NewThreadModelRouteService(st, registry)
	var summaries []contextmgr.Summary
	compactedIDs := map[int64]bool{}
	var rawSnapshots [][]session.Message
	var sourceSessions []string
	for index := 0; index < 3; index++ {
		// Explicitly seed legitimate local Session history and its existing
		// compactor result, not a fabricated provider answer or tool success.
		old, err := st.SaveSessionMessage(ctx, session.NewMessage(current.SessionID, "user", fmt.Sprintf("Session %c original acceptance marker", 'A'+index)))
		if err != nil {
			t.Fatal(err)
		}
		_, err = st.SaveSessionMessage(ctx, session.NewMessage(current.SessionID, "assistant", fmt.Sprintf("Session %c recent progress", 'A'+index)))
		if err != nil {
			t.Fatal(err)
		}
		manager := contextmgr.NewManager(st, contextmgr.Config{MaxMessagesBeforeCompact: 2, PreserveRecentMessages: 1})
		active, err := st.ListSessionMessages(ctx, current.SessionID, false)
		if err != nil {
			t.Fatal(err)
		}
		contextMessages := make([]contextmgr.Message, 0, len(active))
		for _, message := range active {
			contextMessages = append(contextMessages, session.ProjectContextMessage(message))
		}
		compacted, err := manager.Compact(ctx, current.SessionID, "workspace-summary-successors", contextMessages)
		if err != nil || !compacted.Compacted {
			t.Fatalf("local compaction=%#v %v", compacted, err)
		}
		if _, err := st.MarkSessionMessagesCompacted(ctx, current.SessionID, old.ID); err != nil {
			t.Fatal(err)
		}
		for _, message := range active[:len(active)-1] {
			compactedIDs[message.ID] = true
		}
		summaries = append(summaries, compacted.Summary)
		raw, err := st.ListSessionMessages(ctx, current.SessionID, true)
		if err != nil {
			t.Fatal(err)
		}
		rawSnapshots = append(rawSnapshots, raw)
		sourceSessions = append(sourceSessions, current.SessionID)
		action := domain.ThreadModelRouteSelect
		provider, model := "selected-provider", "selected-model"
		if index == 1 {
			action, provider, model = domain.ThreadModelRouteReset, "", ""
		}
		if _, err := routes.Change(ctx, application.ChangeThreadModelRouteRequest{Version: domain.ThreadModelRouteControlProtocolVersion,
			ThreadID: threadID, Action: action, Provider: provider, Model: model, RequestedBy: "test_operator", OperationKey: fmt.Sprintf("summary-model-change-%d", index)}); err != nil {
			t.Fatal(err)
		}
		predecessor := current
		if _, err := runs.Cancel(ctx, current.ID); err != nil {
			t.Fatal(err)
		}
		next, err := threads.Submit(ctx, application.SubmitThreadMessageRequest{Version: domain.ThreadMessageProtocolVersion,
			ThreadID: threadID, Content: "Continue the original task with retained context", OperationKey: fmt.Sprintf("summary-successor-%d", index), RequestedBy: "test_operator"})
		if err != nil || !next.SuccessorCreated || next.PredecessorRunID != predecessor.ID || next.Run.SessionID == predecessor.SessionID {
			t.Fatalf("normal successor=%#v %v", next, err)
		}
		var snapshot contextmgr.ContinuitySnapshot
		if err := json.Unmarshal(next.Run.Config.ContinuityContext, &snapshot); err != nil {
			t.Fatal(err)
		}
		if err := snapshot.Validate(); err != nil {
			t.Fatal(err)
		}
		if snapshot.Fingerprint != next.Run.Config.ContinuityContextFingerprint || snapshot.Authority != (contextmgr.ContinuityAuthority{}) {
			t.Fatal("successor changed snapshot integrity or restored authority")
		}
		wantReference := fmt.Sprintf("compaction:%d:%s", compacted.Summary.ID, compacted.Summary.ContentSHA256)
		foundReference := false
		for _, reference := range snapshot.InheritedContext {
			if reference == wantReference {
				foundReference = true
			}
		}
		if !foundReference {
			t.Fatal("local summary reference was rebound to the aggregate hash")
		}
		for _, want := range summaries {
			if !strings.Contains(snapshot.SummaryContent, fmt.Sprintf("Session %c original acceptance marker", 'A'+len(summaries)-1)) {
				t.Fatal("latest local summary disappeared")
			}
			if index == 0 {
				if snapshot.SummaryID != want.ID || snapshot.SummaryContent != want.Content {
					t.Fatal("single summary shape changed")
				}
			} else {
				var window struct {
					Version string                               `json:"version"`
					Sources []contextmgr.ContinuitySummarySource `json:"sources"`
				}
				if err := json.Unmarshal([]byte(snapshot.SummaryContent), &window); err != nil {
					t.Fatal(err)
				}
				if snapshot.SummaryID != 0 || window.Version != "thread_summary_window.v1" || len(window.Sources) != 2 {
					t.Fatal("missing bounded rolling summary sources")
				}
				// Complete historical summaries are now reached through their
				// original source, instead of requiring every body in the window.
				original, err := st.ReadThreadHistory(ctx, next.Run.ID, domain.HistoryReadRequest{
					SourceID: fmt.Sprintf("summary:%d", want.ID), ExpectedSHA256: want.ContentSHA256,
				})
				if err != nil || original.HasMore || original.Content != want.Content || original.ContentSHA256 != want.ContentSHA256 {
					t.Fatalf("original summary %d changed or was dropped: %v", want.ID, err)
				}
			}
		}
		for _, message := range snapshot.RecentMessages {
			if compactedIDs[message.ID] {
				t.Fatalf("already summarized source %d duplicated into recent history", message.ID)
			}
		}
		permission, err := st.GetRunExecutionPermission(ctx, next.Run.ID)
		if err != nil || permission.ProcessEnabled || permission.ExecutionAuthorized || permission.CapabilityGrant {
			t.Fatalf("authority inherited: %#v %v", permission, err)
		}
		current = next.Run
	}
	for index, sessionID := range sourceSessions {
		after, err := st.ListSessionMessages(ctx, sessionID, true)
		if err != nil || !reflect.DeepEqual(after, rawSnapshots[index]) {
			t.Fatalf("old Session history changed: %s %v", sessionID, err)
		}
	}
}
