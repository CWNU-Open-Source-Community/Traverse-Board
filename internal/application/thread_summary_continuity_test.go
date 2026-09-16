package application

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/contextmgr"
	"cyberagent-workbench/internal/session"
)

func TestThreadSummaryContinuityRollsOnlyWithExactOriginalReceipt(t *testing.T) {
	old := strings.Repeat("旧要求中文", 600)
	snapshot := contextmgr.ContinuitySnapshot{SourceSessionID: "session-capacity", WorkspaceID: "workspace-capacity",
		SummaryContent: old, SummaryContentSHA256: session.ContentSHA256(old)}
	current := contextmgr.Summary{ID: 2, TaskID: snapshot.SourceSessionID, WorkspaceID: snapshot.WorkspaceID,
		ProtocolVersion: contextmgr.LegacyHandoffProtocolVersion, Content: strings.Repeat("新修正中文", 600),
		SourceMessageCount: 2, PreservedMessageCount: 1, CreatedAt: time.Now().UTC()}
	current.ContentSHA256 = session.ContentSHA256(current.Content)
	ref, _ := json.Marshal(struct {
		Run         string `json:"r"`
		Fingerprint string `json:"f"`
	}{"run-original-holder", strings.Repeat("a", 64)})
	source := contextmgr.ContinuitySummarySource{SourceID: "continuity:" + base64.RawURLEncoding.EncodeToString(ref),
		Part: "summary", ContentSHA256: snapshot.SummaryContentSHA256}
	before := snapshot
	bad := source
	bad.ContentSHA256 = strings.Repeat("b", 64)
	if err := mergeThreadContinuitySummary(&snapshot, current, bad); err == nil || !reflect.DeepEqual(snapshot, before) {
		t.Fatalf("mismatched original receipt accepted or changed snapshot: %v", err)
	}
	if err := mergeThreadContinuitySummary(&snapshot, current, source); err != nil {
		t.Fatal(err)
	}
	var window struct {
		Version string                               `json:"version"`
		Sources []contextmgr.ContinuitySummarySource `json:"sources"`
	}
	if err := json.Unmarshal([]byte(snapshot.SummaryContent), &window); err != nil || window.Version != "thread_summary_window.v1" ||
		len(window.Sources) != 2 || window.Sources[0] != source || window.Sources[1].ContentSHA256 != current.ContentSHA256 {
		t.Fatalf("rolling source chain changed: %+v %v", window, err)
	}
	if len(snapshot.SummaryContent) > contextmgr.MaxContinuitySummaryBytes || snapshot.SummaryID != 0 ||
		snapshot.SummaryContentSHA256 != session.ContentSHA256(snapshot.SummaryContent) {
		t.Fatal("rolling snapshot size, identity or SHA is invalid")
	}
}

func summaryContinuityTestValue(t *testing.T, id int64, text string) contextmgr.Summary {
	t.Helper()
	value, err := contextmgr.PrepareSummaryForStorage(contextmgr.Summary{
		TaskID: "session-summary-test", WorkspaceID: "workspace-summary-test", Content: text,
		SourceMessageCount: 2, PreservedMessageCount: 1, CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	value.ID = id
	return value
}

func TestThreadSummaryContinuityFlattensAndDeduplicates(t *testing.T) {
	snapshot := contextmgr.ContinuitySnapshot{SourceSessionID: "session-summary-test", WorkspaceID: "workspace-summary-test"}
	first := summaryContinuityTestValue(t, 1, "Original A requirement")
	second := summaryContinuityTestValue(t, 2, "B correction")
	third := summaryContinuityTestValue(t, 3, "C actual observation")
	if err := mergeThreadContinuitySummary(&snapshot, first); err != nil {
		t.Fatal(err)
	}
	if snapshot.SummaryID != first.ID || snapshot.SummaryContent != first.Content || snapshot.SummaryContentSHA256 != first.ContentSHA256 {
		t.Fatal("single-summary compatibility changed")
	}
	if err := mergeThreadContinuitySummary(&snapshot, second); err != nil {
		t.Fatal(err)
	}
	before := snapshot
	if err := mergeThreadContinuitySummary(&snapshot, second); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshot, before) {
		t.Fatal("same identity/digest replay changed the bundle")
	}
	if err := mergeThreadContinuitySummary(&snapshot, third); err != nil {
		t.Fatal(err)
	}
	var bundle threadSummaryBundle
	if err := json.Unmarshal([]byte(snapshot.SummaryContent), &bundle); err != nil {
		t.Fatal(err)
	}
	if snapshot.SummaryID != 0 || len(bundle.Summaries) != 3 || snapshot.SummaryContentSHA256 != session.ContentSHA256(snapshot.SummaryContent) {
		t.Fatalf("invalid combined summary: %#v", snapshot)
	}
	for i, want := range []contextmgr.Summary{first, second, third} {
		got := bundle.Summaries[i]
		if got.SummaryID != want.ID || got.ContentSHA256 != want.ContentSHA256 || got.Content != want.Content {
			t.Fatalf("original summary %d was rewritten: %#v", i, got)
		}
	}
}

func TestThreadSummaryContinuityRejectsConflictAndInvalidBundle(t *testing.T) {
	first := summaryContinuityTestValue(t, 1, "Original requirement")
	snapshot := contextmgr.ContinuitySnapshot{SourceSessionID: "session-summary-test", WorkspaceID: "workspace-summary-test"}
	if err := mergeThreadContinuitySummary(&snapshot, first); err != nil {
		t.Fatal(err)
	}
	before := snapshot
	conflicting := summaryContinuityTestValue(t, 1, "Different content with the same identity")
	if err := mergeThreadContinuitySummary(&snapshot, conflicting); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("same-ID conflict=%v", err)
	}
	if !reflect.DeepEqual(snapshot, before) {
		t.Fatal("conflict mutated the old summary")
	}
	for _, field := range []string{"session", "workspace"} {
		t.Run("wrong "+field, func(t *testing.T) {
			wrong := first
			if field == "session" {
				wrong.TaskID = "other-session"
			} else {
				wrong.WorkspaceID = "other-workspace"
			}
			if err := mergeThreadContinuitySummary(&snapshot, wrong); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
				t.Fatalf("different owner accepted: %v", err)
			}
			if !reflect.DeepEqual(snapshot, before) {
				t.Fatal("different owner mutated previous context")
			}
		})
	}
	for name, content := range map[string]string{
		"bad member digest": `{"kind":"thread_summary_bundle","summaries":[{"summary_id":1,"content_sha256":"bad","content":"old"},{"summary_id":2,"content_sha256":"bad","content":"new"}]}`,
		"unknown field":     `{"kind":"thread_summary_bundle","summaries":[],"authority":true}`,
		"empty bundle":      `{"kind":"thread_summary_bundle","summaries":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			value := contextmgr.ContinuitySnapshot{SourceSessionID: "session-summary-test", WorkspaceID: "workspace-summary-test", SummaryContent: content, SummaryContentSHA256: session.ContentSHA256(content)}
			if err := mergeThreadContinuitySummary(&value, first); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
				t.Fatalf("invalid bundle accepted: %v", err)
			}
		})
	}
}

func TestThreadSummaryContinuityCapacityFailurePreservesOldContent(t *testing.T) {
	old := strings.Repeat("旧", 3000)
	snapshot := contextmgr.ContinuitySnapshot{SourceSessionID: "session-capacity", WorkspaceID: "workspace-capacity", SummaryID: 1, SummaryContent: old, SummaryContentSHA256: session.ContentSHA256(old)}
	before := snapshot
	// A valid migrated legacy summary can be larger than modern generated
	// records. Keeping each under the old 16 KiB limit does not make their
	// combined UTF-8 representation fit that same unchanged limit.
	current := contextmgr.Summary{ID: 2, TaskID: "session-capacity", WorkspaceID: "workspace-capacity",
		ProtocolVersion: contextmgr.LegacyHandoffProtocolVersion, Content: strings.Repeat("新", 3000),
		SourceMessageCount: 2, PreservedMessageCount: 1}
	if err := mergeThreadContinuitySummary(&snapshot, current); apperror.CodeOf(err) != apperror.CodeResourceExhausted {
		t.Fatalf("over-capacity combination=%v", err)
	}
	if !reflect.DeepEqual(snapshot, before) {
		t.Fatal("capacity failure discarded earlier content")
	}
}

func TestThreadSummaryContinuityPreservesLegacyOpaqueZeroID(t *testing.T) {
	old := "Legacy historical summary without a stored row identity"
	snapshot := contextmgr.ContinuitySnapshot{SourceSessionID: "session-summary-test", WorkspaceID: "workspace-summary-test", SummaryContent: old, SummaryContentSHA256: session.ContentSHA256(old)}
	if err := mergeThreadContinuitySummary(&snapshot, summaryContinuityTestValue(t, 9, "New session")); err != nil {
		t.Fatal(err)
	}
	entries, err := threadContinuitySummaryEntries(snapshot.SummaryID, snapshot.SummaryContentSHA256, snapshot.SummaryContent)
	if err != nil || len(entries) != 2 || entries[0].SummaryID != 0 || entries[0].Content != old {
		t.Fatalf("legacy opaque reference changed: %#v %v", entries, err)
	}
}
