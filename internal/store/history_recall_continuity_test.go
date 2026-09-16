package store

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/contextmgr"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/session"
)

func historyStoredSummary(t *testing.T, st *SQLiteStore, run domain.Run, marker string) contextmgr.Summary {
	t.Helper()
	for _, text := range []string{marker + " 原目标：" + strings.Repeat("保留原始中文🙂。", 90), "约束：不扩大权限。", "最近输入保留"} {
		historyMessage(t, st, run, text)
	}
	stored, err := st.ListSessionMessages(t.Context(), run.SessionID, true)
	if err != nil {
		t.Fatal(err)
	}
	var messages []contextmgr.Message
	for _, message := range stored {
		messages = append(messages, contextmgr.Message{Role: message.Role, Content: message.Content, CreatedAt: message.CreatedAt,
			SourceMessageID: message.ID, SourceKind: message.Provenance.SourceKind, SourceRef: message.Provenance.SourceRef,
			ContentSHA256: message.Provenance.ContentSHA256, InstructionAuthorized: message.Provenance.InstructionAuthorized})
	}
	result, err := contextmgr.NewManager(st, contextmgr.Config{PreserveRecentMessages: 1}).Compact(t.Context(), run.SessionID, "ws-history", messages)
	if err != nil {
		t.Fatal(err)
	}
	return result.Summary
}

func historyContinuityHolder(t *testing.T, st *SQLiteStore, predecessor, source domain.Run, summaryID int64, content string) (domain.Run, contextmgr.ContinuitySnapshot) {
	t.Helper()
	snapshot, err := contextmgr.SealContinuitySnapshot(contextmgr.ContinuitySnapshot{
		SourceRunID: source.ID, SourceSessionID: source.SessionID, WorkspaceID: "ws-history",
		SummaryID: summaryID, SummaryContent: content, SummaryContentSHA256: session.ContentSHA256(content),
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	predecessor.Config.ContinuityContext, err = json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	predecessor.Config.ContinuityContextFingerprint = snapshot.Fingerprint
	return historySuccessor(t, st, predecessor), snapshot
}

func historyContinuitySource(holder domain.Run, snapshot contextmgr.ContinuitySnapshot) string {
	return "continuity:" + encodeHistoryToken(historyContinuityRef{Run: holder.ID, Fingerprint: snapshot.Fingerprint})
}

func TestHistoryRecallStoredSummariesRemainExactAcrossSuccessorAndRestart(t *testing.T) {
	st, first, path := historyFixture(t)
	old := historyStoredSummary(t, st, first, "摘要回读标记")
	latest := historyStoredSummary(t, st, first, "最新摘要标记")
	if latest.ID == old.ID || latest.PreviousSummaryID != old.ID {
		t.Fatal("fixture did not create two true cumulative summaries")
	}
	next := historySuccessor(t, st, first)
	// Keep the whole raw summary table image, not just the latest projection.
	var beforeJSON string
	if err := st.db.QueryRow(`SELECT json_group_array(json_object('id',id,'body',content,'sha',content_sha256,'previous',previous_summary_id)) FROM context_summaries`).Scan(&beforeJSON); err != nil {
		t.Fatal(err)
	}
	st.db.SetMaxOpenConns(1)
	if _, err := st.db.Exec(`PRAGMA query_only=ON`); err != nil {
		t.Fatal(err)
	}
	for _, summary := range []contextmgr.Summary{old, latest} {
		source := fmt.Sprintf("summary:%d", summary.ID)
		page, err := st.ReadThreadHistory(t.Context(), next.ID, domain.HistoryReadRequest{SourceID: source, Limit: 37})
		if err != nil || page.Part != "content" || page.Record.Kind != "summary" || page.Record.RunID != first.ID || page.Record.SessionID != first.SessionID || page.Record.SummaryID != summary.ID || page.Record.PreviousSummaryID != summary.PreviousSummaryID || page.Record.ContentSHA256 != summary.ContentSHA256 || page.Record.OriginalInstructionAuthorized || page.InstructionAuthorized {
			t.Fatalf("original summary identity changed: %+v %v", page, err)
		}
		if got := historyReadAll(t, st, next.ID, page.Record, "content", 37); got != summary.Content {
			t.Fatal("summary pages changed original stored bytes")
		}
	}
	search, err := st.SearchThreadHistory(t.Context(), next.ID, domain.HistorySearchRequest{Query: "摘要回读标记"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, record := range search.Records {
		found = found || record.Kind == "summary" && record.SummaryID == old.ID
	}
	if !found {
		t.Fatal("stored summaries were not searchable")
	}
	var afterJSON string
	if err := st.db.QueryRow(`SELECT json_group_array(json_object('id',id,'body',content,'sha',content_sha256,'previous',previous_summary_id)) FROM context_summaries`).Scan(&afterJSON); err != nil || afterJSON != beforeJSON {
		t.Fatalf("reads changed summaries: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	page, err := reopened.ReadThreadHistory(t.Context(), next.ID, domain.HistoryReadRequest{SourceID: fmt.Sprintf("summary:%d", old.ID), ExpectedSHA256: old.ContentSHA256})
	if err != nil || page.ContentSHA256 != old.ContentSHA256 {
		t.Fatalf("restart lost original summary: %+v %v", page, err)
	}
}

func TestHistoryRecallContinuityKeepsOpaqueAndBundleOriginalBytes(t *testing.T) {
	for _, kind := range []string{"opaque", "bundle"} {
		t.Run(kind, func(t *testing.T) {
			st, first, path := historyFixture(t)
			content := "零 ID 原摘要\n" + strings.Repeat("中文🙂保留原文。", 360)
			if kind == "bundle" {
				encoded, _ := json.Marshal(map[string]any{"kind": "thread_summary_bundle", "summaries": []map[string]any{
					{"summary_id": 0, "content": content, "content_sha256": session.ContentSHA256(content)},
					{"summary_id": 7, "content": "旧导入摘要原文", "content_sha256": session.ContentSHA256("旧导入摘要原文")},
				}})
				content = string(encoded)
			}
			holder, snapshot := historyContinuityHolder(t, st, first, first, 0, content)
			source := historyContinuitySource(holder, snapshot)
			stored, err := st.GetRun(t.Context(), holder.ID)
			if err != nil {
				t.Fatal(err)
			}
			raw := string(stored.Config.ContinuityContext)
			remarshalled, _ := json.Marshal(snapshot)
			if raw == string(remarshalled) || session.ContentSHA256(raw) == snapshot.Fingerprint {
				t.Fatal("fixture must distinguish original stored JSON, remarshalling, and semantic fingerprint")
			}
			page, err := st.ReadThreadHistory(t.Context(), holder.ID, domain.HistoryReadRequest{SourceID: source, Part: "summary", ExpectedSHA256: snapshot.SummaryContentSHA256, Limit: 41})
			if err != nil || page.Record.Kind != "continuity" || page.Record.ContinuityFingerprint != snapshot.Fingerprint || page.Record.SummaryID != 0 || page.Record.RunID != holder.ID || page.Record.SessionID != holder.SessionID || page.Record.SourceRef != first.ID || page.Record.ContentSHA256 != session.ContentSHA256(raw) || page.ContentSHA256 != snapshot.SummaryContentSHA256 {
				t.Fatalf("opaque/bundle binding changed: %+v %v", page, err)
			}
			if got := historyReadAll(t, st, holder.ID, page.Record, "summary", 41); got != content {
				t.Fatal("original inherited summary changed")
			}
			if got := historyReadAll(t, st, holder.ID, page.Record, "content", 47); got != raw {
				t.Fatal("original snapshot JSON was reserialized")
			}
			// The successor builder probes this terminal predecessor before its
			// next Run exists; active_run_id has already been cleared.
			if _, err := application.NewRunService(st).Cancel(t.Context(), holder.ID); err != nil {
				t.Fatal(err)
			}
			terminal, err := st.ReadThreadHistory(t.Context(), holder.ID, domain.HistoryReadRequest{SourceID: source, Part: "summary", ExpectedSHA256: snapshot.SummaryContentSHA256, Limit: 4})
			if err != nil || terminal.TotalBytes != len(content) {
				t.Fatalf("terminal predecessor cannot prove retained original: %+v %v", terminal, err)
			}
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			defaultPage, err := reopened.ReadThreadHistory(t.Context(), holder.ID, domain.HistoryReadRequest{SourceID: source})
			if err != nil || defaultPage.Part != "content" || defaultPage.ContentSHA256 != session.ContentSHA256(raw) {
				t.Fatalf("restart/default part lost snapshot: %+v %v", defaultPage, err)
			}
		})
	}
}

func TestHistoryRecallSummaryCursorKeepsLegacyCandidateSetAndNewWatermark(t *testing.T) {
	st, first, _ := historyFixture(t)
	summary := historyStoredSummary(t, st, first, "watermark summary")
	holder, _ := historyContinuityHolder(t, st, first, first, summary.ID, summary.Content)
	for i := 0; i < 4; i++ {
		historyMessage(t, st, holder, fmt.Sprintf("fresh original %d", i))
	}
	initial, err := st.SearchThreadHistory(t.Context(), holder.ID, domain.HistorySearchRequest{Limit: 1})
	if err != nil || !initial.HasMore {
		t.Fatalf("missing initial page: %+v %v", initial, err)
	}
	// This exact pre-extension shape is also used by sealed v1 result replay.
	var cursor struct {
		Run        string `json:"r"`
		QuerySHA   string `json:"q"`
		MaxMessage int64  `json:"m"`
		MaxTool    int64  `json:"t"`
		MaxEvent   int64  `json:"e"`
		Offset     int    `json:"o"`
	}
	raw, _ := base64.RawURLEncoding.DecodeString(initial.SnapshotCursor)
	_ = json.Unmarshal(raw, &cursor)
	legacyRaw, _ := json.Marshal(cursor)
	legacyCursor := base64.RawURLEncoding.EncodeToString(legacyRaw)
	var legacyMessages []int64
	for {
		page, err := st.SearchThreadHistory(t.Context(), holder.ID, domain.HistorySearchRequest{Cursor: legacyCursor, Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		for _, record := range page.Records {
			if record.Kind != "message" {
				t.Fatalf("new kind shifted a legacy cursor: %+v", record)
			}
			legacyMessages = append(legacyMessages, record.MessageID)
		}
		if !page.HasMore {
			break
		}
		legacyCursor = page.NextCursor
	}
	if len(legacyMessages) != 7 || len(mapUniqueHistoryIDs(legacyMessages)) != 7 {
		t.Fatalf("legacy cursor omitted/duplicated originals: %v", legacyMessages)
	}
	late := historyStoredSummary(t, st, holder, "late appended summary")
	request := domain.HistorySearchRequest{Cursor: initial.SnapshotCursor, Limit: 20}
	oldWindow, err := st.SearchThreadHistory(t.Context(), holder.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	haveSummary, haveContinuity := false, false
	for _, record := range oldWindow.Records {
		if record.Kind == "summary" && record.SummaryID == late.ID {
			t.Fatal("later summary entered an issued search snapshot")
		}
		haveSummary = haveSummary || record.Kind == "summary" && record.SummaryID == summary.ID
		haveContinuity = haveContinuity || record.Kind == "continuity"
	}
	if !haveSummary || !haveContinuity {
		t.Fatalf("new cursors must include retained summary and continuity: %+v", oldWindow)
	}
}

func mapUniqueHistoryIDs(ids []int64) map[int64]bool {
	values := map[int64]bool{}
	for _, id := range ids {
		values[id] = true
	}
	return values
}

func TestHistoryRecallContinuityAndSummaryRejectForeignAndChangedBindings(t *testing.T) {
	st, first, _ := historyFixture(t)
	summary := historyStoredSummary(t, st, first, "scoped summary")
	holder, snapshot := historyContinuityHolder(t, st, first, first, summary.ID, summary.Content)
	source := historyContinuitySource(holder, snapshot)
	for _, workspace := range []string{"ws-history", "ws-foreign"} {
		if workspace != "ws-history" {
			if err := st.SaveWorkspace(t.Context(), WorkspaceRecord{ID: workspace, Name: "Foreign", RootPath: t.TempDir(), CreatedAt: time.Now().UTC()}); err != nil {
				t.Fatal(err)
			}
		}
		other := historyNewRun(t, st, workspace)
		for _, id := range []string{source, fmt.Sprintf("summary:%d", summary.ID)} {
			if _, err := st.ReadThreadHistory(t.Context(), other.ID, domain.HistoryReadRequest{SourceID: id}); apperror.CodeOf(err) != apperror.CodeNotFound {
				t.Fatalf("foreign source was readable: %s %v", workspace, err)
			}
		}
	}
	wrong := "continuity:" + encodeHistoryToken(historyContinuityRef{Run: holder.ID, Fingerprint: strings.Repeat("a", 64)})
	if _, err := st.ReadThreadHistory(t.Context(), holder.ID, domain.HistoryReadRequest{SourceID: wrong}); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("wrong requested fingerprint accepted: %v", err)
	}
	if _, err := st.ReadThreadHistory(t.Context(), holder.ID, domain.HistoryReadRequest{SourceID: source, Part: "summary", ExpectedSHA256: strings.Repeat("b", 64)}); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("wrong part digest accepted: %v", err)
	}
	// A legitimate fingerprint for a foreign fork still grants no read scope.
	foreign := historyNewRun(t, st, "ws-history")
	foreignHolder, foreignSnapshot := historyContinuityHolder(t, st, foreign, first, 0, "foreign imported old snapshot")
	if _, err := st.ReadThreadHistory(t.Context(), foreignHolder.ID, domain.HistoryReadRequest{SourceID: historyContinuitySource(foreignHolder, foreignSnapshot), Part: "summary"}); apperror.CodeOf(err) != apperror.CodeNotFound {
		t.Fatalf("foreign snapshot origin expanded its holder scope: %v", err)
	}
	local := historyMessage(t, st, foreignHolder, "Local message remains searchable beside a foreign fork")
	localSearch, err := st.SearchThreadHistory(t.Context(), foreignHolder.ID, domain.HistorySearchRequest{})
	if err != nil || localSearch.Scanned != 2 || len(localSearch.Records) != 1 || localSearch.Records[0].MessageID != local.ID || localSearch.HasMore {
		t.Fatalf("foreign fork poisoned local search or candidate accounting: %+v %v", localSearch, err)
	}
	if _, err := st.db.Exec(`UPDATE runs SET config_json=json_set(config_json,'$.continuity_context_fingerprint',?) WHERE id=?`, strings.Repeat("c", 64), holder.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ReadThreadHistory(t.Context(), holder.ID, domain.HistoryReadRequest{SourceID: source}); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("changed stored fingerprint accepted: %v", err)
	}
	if _, err := st.SearchThreadHistory(t.Context(), holder.ID, domain.HistorySearchRequest{}); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("search swallowed corrupt in-scope continuity: %v", err)
	}
}

func TestHistoryRecallLegacySummaryDigestIsComputedWithoutBackfill(t *testing.T) {
	st, run, _ := historyFixture(t)
	// Reproduce an existing v0 row with no stored digest. New production saves
	// remain v1; this fixture alone lifts the v1-only insert guard.
	if _, err := st.db.Exec(`DROP TRIGGER trg_context_summaries_v1_insert`); err != nil {
		t.Fatal(err)
	}
	body := " Legacy opaque summary\npassword=[REDACTED:secret]\n中文原文与尾部空格 "
	result, err := st.db.Exec(`INSERT INTO context_summaries(task_id,workspace_id,protocol_version,content,content_sha256,
		source_message_count,preserved_message_count,token_estimate,created_at) VALUES(?,?,?,?,?,3,1,20,?)`,
		run.SessionID, "ws-history", contextmgr.LegacyHandoffProtocolVersion, body, "", ts(time.Now().UTC()))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := result.LastInsertId()
	page, err := st.ReadThreadHistory(t.Context(), run.ID, domain.HistoryReadRequest{SourceID: fmt.Sprintf("summary:%d", id), Limit: 19})
	if err != nil || page.Record.ContentSHA256 != session.ContentSHA256(body) || page.ContentSHA256 != session.ContentSHA256(body) {
		t.Fatalf("legacy original digest not available: %+v %v", page, err)
	}
	if got := historyReadAll(t, st, run.ID, page.Record, "content", 19); got != body {
		t.Fatal("legacy body was trimmed or re-redacted")
	}
	var rawBody, rawSHA string
	if err := st.db.QueryRow(`SELECT content,content_sha256 FROM context_summaries WHERE id=?`, id).Scan(&rawBody, &rawSHA); err != nil || rawSHA != "" || rawBody != body {
		t.Fatalf("read backfilled the legacy row: %v", err)
	}
	if _, err := st.db.Exec(`DROP TRIGGER trg_context_summaries_update_immutable`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE context_summaries SET content_sha256=? WHERE id=?`, strings.Repeat("e", 64), id); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ReadThreadHistory(t.Context(), run.ID, domain.HistoryReadRequest{SourceID: page.Record.SourceID}); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("mismatched legacy digest accepted: %v", err)
	}
}

func TestHistoryRecallSummarySearchVerifiesNewKindsAtOriginalWatermark(t *testing.T) {
	st, first, _ := historyFixture(t)
	summary := historyStoredSummary(t, st, first, "original summary search")
	holder, _ := historyContinuityHolder(t, st, first, first, summary.ID, summary.Content)
	request := domain.HistorySearchRequest{}
	checkpoint, callID := prepareHistoryResultCall(t, st, holder, "history_search", request)
	page, err := st.SearchThreadHistory(t.Context(), holder.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, record := range page.Records {
		kinds[record.Kind] = true
	}
	if !kinds["summary"] || !kinds["continuity"] {
		t.Fatal("fixture omitted the new searchable source kinds")
	}
	historyStoredSummary(t, st, holder, "later summary outside saved watermark")
	saved, _, err := st.RecordSupervisorToolResult(t.Context(), checkpoint, domain.SupervisorToolResult{CallID: callID,
		Status: domain.SupervisorToolCompleted, CompletedAt: time.Now().UTC(), ResultJSON: historyResultEnvelope(t, "history_search", page)})
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(page)
	var envelope struct {
		Stdout string `json:"stdout"`
	}
	if json.Unmarshal([]byte(saved.ResultJSON), &envelope) != nil || envelope.Stdout != string(want) {
		t.Fatal("summary/snapshot search was rewritten during exact persistence verification")
	}
}

func TestHistoryRecallNewSourcePartsAreVerifiedBeforeResultPersistence(t *testing.T) {
	for _, part := range []string{"summary", "content"} {
		t.Run(part, func(t *testing.T) {
			st, first, _ := historyFixture(t)
			holder, snapshot := historyContinuityHolder(t, st, first, first, 0, "password=[REDACTED:secret]\nexact 中文🙂 saved summary")
			request := domain.HistoryReadRequest{SourceID: historyContinuitySource(holder, snapshot), Part: part, Limit: 29}
			checkpoint, callID := prepareHistoryResultCall(t, st, holder, "history_read", request)
			page, err := st.ReadThreadHistory(t.Context(), holder.ID, request)
			if err != nil {
				t.Fatal(err)
			}
			forged := page
			forged.Content += " changed"
			if _, _, err := st.RecordSupervisorToolResult(t.Context(), checkpoint, domain.SupervisorToolResult{CallID: callID, Status: domain.SupervisorToolCompleted, CompletedAt: time.Now().UTC(), ResultJSON: historyResultEnvelope(t, "history_read", forged)}); apperror.CodeOf(err) != apperror.CodeConflict {
				t.Fatalf("unverified new-source bytes persisted: %v", err)
			}
			saved, _, err := st.RecordSupervisorToolResult(t.Context(), checkpoint, domain.SupervisorToolResult{CallID: callID, Status: domain.SupervisorToolCompleted, CompletedAt: time.Now().UTC(), ResultJSON: historyResultEnvelope(t, "history_read", page)})
			if err != nil {
				t.Fatal(err)
			}
			var envelope struct {
				Stdout string `json:"stdout"`
			}
			var observed domain.HistoryReadResult
			if json.Unmarshal([]byte(saved.ResultJSON), &envelope) != nil || json.Unmarshal([]byte(envelope.Stdout), &observed) != nil || !reflect.DeepEqual(page, observed) {
				t.Fatal("verified new-source projection changed in durable output")
			}
		})
	}
}
