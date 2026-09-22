package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/session"
)

func TestSchemaV168BackfillsOriginalMessageIdentityAndKeepsRevisionReceiptGuard(t *testing.T) {
	ctx := context.Background()
	state := openUnmigratedSQLiteStore(t, filepath.Join(t.TempDir(), "v168-upgrade.db"))
	plan := migrationPlan()
	if err := applyMigrationPrefixForTest(ctx, state, plan, 167); err != nil {
		t.Fatal(err)
	}
	_, created, err := application.NewRunService(state).Create(ctx,
		application.CreateRunRequest{Goal: "v168 queued message upgrade", Profile: "review",
			Interactive: true, Budget: domain.Budget{MaxTurns: 8}})
	if err != nil {
		t.Fatal(err)
	}
	run, err := application.NewRunService(state).Start(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	content := "legacy immutable queued content"
	digest := domain.OperatorSteeringContentSHA256(content)
	now := time.Now().UTC()
	committedSession, err := state.SaveSessionMessage(ctx,
		session.NewMessage(run.SessionID, "user", content))
	if err != nil {
		t.Fatal(err)
	}
	insertLegacy := func(id string, sequence int, status domain.OperatorSteeringStatus,
		sessionMessage any, committedAt any, cancelledAt any,
	) {
		t.Helper()
		if _, err := state.db.ExecContext(ctx, `INSERT INTO operator_steering_messages
		(id,run_id,session_id,sequence,status,content,content_sha256,requested_by,
		 session_message_id,created_at,committed_at,cancelled_at,image_count,attachment_count)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,0,0)`, id, run.ID, run.SessionID, sequence,
			status, content, digest, "operator", sessionMessage, ts(now), committedAt,
			cancelledAt); err != nil {
			t.Fatal(err)
		}
	}
	insertLegacy("steer-v168-pending", 1, domain.OperatorSteeringPending, nil, nil, nil)
	insertLegacy("steer-v168-committed", 2, domain.OperatorSteeringCommitted,
		committedSession.ID, ts(now), nil)
	insertLegacy("steer-v168-cancelled", 3, domain.OperatorSteeringCancelled,
		nil, nil, ts(now))
	if _, err := state.db.ExecContext(ctx, `INSERT INTO operator_steering_operations
		(operation_key_digest,request_fingerprint,message_id,run_id,requested_by,created_at)
		VALUES(?,?,?,?,?,?)`, strings.Repeat("a", 64), strings.Repeat("b", 64),
		"steer-v168-pending", run.ID, "operator", ts(now)); err != nil {
		t.Fatal(err)
	}
	if err := state.applyMigration(ctx, plan[167]); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"steer-v168-pending", "steer-v168-committed", "steer-v168-cancelled"} {
		message, err := state.GetOperatorSteering(ctx, id)
		if err != nil || message.Revision != 0 || message.OriginalContent != content ||
			message.OriginalContentSHA256 != digest || message.EditedAt != nil {
			t.Fatalf("v168 original identity backfill id=%s value=%#v err=%v", id, message, err)
		}
	}
	message, err := state.GetOperatorSteering(ctx, "steer-v168-pending")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.db.ExecContext(ctx, `UPDATE operator_steering_messages
		SET content='unreceipted',content_sha256=?,revision=1,edited_at=? WHERE id=?`,
		domain.OperatorSteeringContentSHA256("unreceipted"), ts(now), message.ID); err == nil {
		t.Fatal("v168 trigger accepted a revision without its exact receipt")
	}
	rows, err := state.db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("v168 upgrade left a foreign key violation")
	}
}

func TestSchemaV168LegacyCancelledAttachmentNeverEntersModelHistoryOrCompaction(t *testing.T) {
	ctx := context.Background()
	state := openUnmigratedSQLiteStore(t, filepath.Join(t.TempDir(), "v168-legacy-context.db"))
	plan := migrationPlan()
	if err := applyMigrationPrefixForTest(ctx, state, plan, 167); err != nil {
		t.Fatal(err)
	}
	if err := state.SaveWorkspace(ctx, WorkspaceRecord{ID: "workspace-v168-context",
		Name: "v168 context", RootPath: t.TempDir(), CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	_, created, err := application.NewRunService(state).Create(ctx,
		application.CreateRunRequest{Goal: "exclude withdrawn legacy attachment evidence",
			WorkspaceID: "workspace-v168-context", Profile: "review", Interactive: true,
			Budget: domain.Budget{MaxTurns: 8}})
	if err != nil {
		t.Fatal(err)
	}
	run, err := application.NewRunService(state).Start(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	first, err := state.SaveSessionMessage(ctx,
		session.NewMessage(run.SessionID, "user", "eligible history before withdrawn attachment"))
	if err != nil {
		t.Fatal(err)
	}
	const marker = "LEGACY_CANCELLED_ATTACHMENT_MUST_NOT_REACH_MODEL"
	file, err := state.SaveWorkspaceFileAttachment(ctx, "workspace-v168-context",
		"legacy-context-file", "text/plain", "withdrawn.txt", []byte(marker))
	if err != nil {
		t.Fatal(err)
	}
	messageID := "steer-v168-cancelled-attachment"
	now := time.Now().UTC()
	if _, err := state.db.ExecContext(ctx, `INSERT INTO operator_steering_messages
		(id,run_id,session_id,sequence,status,content,content_sha256,requested_by,
		 session_message_id,created_at,committed_at,cancelled_at,image_count,attachment_count)
		VALUES(?,?,?,?,?,?,?,?,NULL,?,NULL,NULL,0,1)`, messageID, run.ID, run.SessionID,
		1, domain.OperatorSteeringPending, "", domain.OperatorSteeringContentSHA256(""),
		"operator", ts(now)); err != nil {
		t.Fatal(err)
	}
	if _, err := state.db.ExecContext(ctx, `INSERT INTO thread_message_attachments
		(message_id,ordinal,attachment_id) VALUES(?,0,?)`, messageID, file.ID); err != nil {
		t.Fatal(err)
	}
	legacyEvidence, err := state.SaveSessionMessage(ctx,
		fileAttachmentEvidenceMessage(run.SessionID, messageID, file, marker))
	if err != nil {
		t.Fatal(err)
	}
	ordinary, err := state.SaveSessionMessage(ctx, session.NewEvidenceMessage(run.SessionID,
		session.SourceUploadedFile, "ordinary-project-evidence", `{"text":"ordinary project attachment evidence"}`))
	if err != nil {
		t.Fatal(err)
	}
	for i := 2; i <= 6; i++ {
		if _, err := state.SaveSessionMessage(ctx, session.NewMessage(run.SessionID, "user",
			"eligible history "+string(rune('0'+i)))); err != nil {
			t.Fatal(err)
		}
	}
	if err := state.applyMigration(ctx, plan[167]); err != nil {
		t.Fatal(err)
	}
	if _, err := state.CancelOperatorSteering(ctx, domain.CancelOperatorSteeringRequest{
		MessageID: messageID, OperationKey: "cancel-legacy-context-attachment",
		RequestedBy: "operator", Reason: "withdraw before delivery"}); err != nil {
		t.Fatal(err)
	}
	var receiptCount int
	if err := state.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM operator_message_attachment_evidence
		WHERE message_id=? AND session_message_id=?`, messageID, legacyEvidence.ID).Scan(&receiptCount); err != nil || receiptCount != 1 {
		t.Fatalf("legacy attachment receipt count=%d err=%v", receiptCount, err)
	}
	visible, err := state.ListSessionMessages(ctx, run.SessionID, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range visible {
		if message.ID == legacyEvidence.ID || strings.Contains(message.Content, marker) {
			t.Fatalf("withdrawn legacy attachment entered model history: %#v", message)
		}
	}
	if !containsSessionMessageID(visible, first.ID) || !containsSessionMessageID(visible, ordinary.ID) {
		t.Fatalf("eligible or ordinary attachment evidence was filtered: %#v", visible)
	}
	lease := acquireTestRunExecutionLease(t, ctx, state, run.ID)
	turn, err := state.BeginSupervisorTurn(ctx, lease, "current input")
	if err != nil {
		t.Fatal(err)
	}
	result, err := state.CompactSupervisorContext(ctx, turn.Checkpoint, 2)
	if err != nil || !result.Compacted || strings.Contains(result.Summary.Content, marker) {
		t.Fatalf("legacy marker reached compaction result=%#v err=%v", result, err)
	}
	var legacyCompacted int
	if err := state.db.QueryRowContext(ctx, `SELECT compacted FROM session_messages WHERE id=?`,
		legacyEvidence.ID).Scan(&legacyCompacted); err != nil || legacyCompacted != 0 {
		t.Fatalf("excluded legacy evidence compacted=%d err=%v", legacyCompacted, err)
	}
	all, err := state.ListSessionMessages(ctx, run.SessionID, true)
	if err != nil || !containsSessionMessageID(all, legacyEvidence.ID) {
		t.Fatalf("audit history lost excluded legacy evidence: %#v err=%v", all, err)
	}
}

func containsSessionMessageID(messages []session.Message, id int64) bool {
	for _, message := range messages {
		if message.ID == id {
			return true
		}
	}
	return false
}
