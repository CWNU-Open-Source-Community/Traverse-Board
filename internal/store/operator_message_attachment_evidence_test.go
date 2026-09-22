package store

import (
	"path/filepath"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/session"
)

func TestAttachmentEvidenceReceiptRejectsAnotherMessageOwner(t *testing.T) {
	state, err := Open(filepath.Join(t.TempDir(), "attachment-evidence-owner.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	ctx := t.Context()
	if err := state.SaveWorkspace(ctx, WorkspaceRecord{ID: "workspace-evidence-owner",
		Name: "evidence-owner", RootPath: t.TempDir(), CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	_, created, err := application.NewRunService(state).Create(ctx,
		application.CreateRunRequest{Goal: "bind exact attachment evidence owner",
			WorkspaceID: "workspace-evidence-owner", Profile: "review", Interactive: true,
			Budget: domain.Budget{MaxTurns: 8}})
	if err != nil {
		t.Fatal(err)
	}
	run, err := application.NewRunService(state).Start(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	file, err := state.SaveWorkspaceFileAttachment(ctx, "workspace-evidence-owner",
		"evidence-owner-upload", "text/plain", "owner.txt", []byte("exact owner"))
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.FileAttachmentReference{ID: file.ID, WorkspaceID: file.WorkspaceID,
		SHA256: file.SHA256, ByteSize: file.ByteSize}
	first, err := state.EnqueueOperatorSteering(ctx, domain.EnqueueOperatorSteeringRequest{
		RunID: run.ID, SessionID: run.SessionID, Content: "first owner",
		Attachments:  []domain.FileAttachmentReference{ref},
		OperationKey: "evidence-owner-first-0001", RequestedBy: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := state.EnqueueOperatorSteering(ctx, domain.EnqueueOperatorSteeringRequest{
		RunID: run.ID, SessionID: run.SessionID, Content: "second owner",
		Attachments:  []domain.FileAttachmentReference{ref},
		OperationKey: "evidence-owner-second-0001", RequestedBy: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	ownerEvidence, err := state.SaveSessionMessage(ctx,
		fileAttachmentEvidenceMessage(run.SessionID, first.Message.ID, file, "exact owner"))
	if err != nil {
		t.Fatal(err)
	}
	secondUser, err := state.SaveSessionMessage(ctx,
		session.NewMessage(run.SessionID, "user", second.Message.Content))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := state.db.ExecContext(ctx, `UPDATE operator_steering_messages
		SET status='committed',session_message_id=?,committed_at=? WHERE id=?`,
		secondUser.ID, ts(now), second.Message.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := state.db.ExecContext(ctx, `INSERT INTO operator_message_attachment_evidence
		(message_id,kind,attachment_id,session_message_id) VALUES(?,?,?,?)`,
		second.Message.ID, "file", file.ID, ownerEvidence.ID); err == nil {
		t.Fatal("attachment evidence receipt accepted another operator message owner")
	}
}
