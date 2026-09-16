package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
)

func TestOriginalFileInputSelectionIsReadOnlyExactAndReopenable(t *testing.T) {
	dbpath := filepath.Join(t.TempDir(), "input-selection.db")
	st, err := Open(dbpath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err = st.SaveWorkspace(t.Context(), WorkspaceRecord{ID: "workspace-input-selection", Name: "inputs", RootPath: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	create := func() domain.Run {
		_, run, err := application.NewRunService(st).Create(t.Context(), application.CreateRunRequest{Goal: "Observe exact raw inputs", Profile: "review", WorkspaceID: "workspace-input-selection", Interactive: true})
		if err != nil {
			t.Fatal(err)
		}
		run, err = application.NewRunService(st).Start(t.Context(), run.ID)
		if err != nil {
			t.Fatal(err)
		}
		return run
	}
	run, otherRun := create(), create()
	files := make([]domain.WorkspaceFileAttachment, 4)
	for i, key := range []string{"sent-input-original-key", "queued-input-original-key", "other-thread-original-key", "unsent-input-original-key"} {
		files[i], err = st.SaveWorkspaceFileAttachment(t.Context(), "workspace-input-selection", key, "application/zip", "archive.zip", []byte("PK\x03\x04"+key))
		if err != nil {
			t.Fatal(err)
		}
	}
	queue := func(run domain.Run, file domain.WorkspaceFileAttachment, key string) string {
		result, err := st.EnqueueOperatorSteering(t.Context(), domain.EnqueueOperatorSteeringRequest{RunID: run.ID, SessionID: run.SessionID, OperationKey: key, RequestedBy: "test_operator", Attachments: []domain.FileAttachmentReference{{ID: file.ID, WorkspaceID: file.WorkspaceID, SHA256: file.SHA256, ByteSize: file.ByteSize}}})
		if err != nil {
			t.Fatal(err)
		}
		return result.Message.ID
	}
	first := queue(run, files[0], "sent-operator-original-key")
	queue(run, files[1], "queued-operator-original-key")
	queue(otherRun, files[2], "other-operator-original-key")
	acquired, err := st.AcquireRunExecutionLease(t.Context(), domain.AcquireRunExecutionLeaseRequest{RunID: run.ID, OwnerID: "input-selection-owner", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := st.BeginSupervisorSteeringTurnForMessage(t.Context(), acquired.Lease, first)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(dbpath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err = st.db.Exec(`PRAGMA query_only=ON`); err != nil {
		t.Fatal(err)
	}
	set, err := st.ListSupervisorFileAttachmentInputs(t.Context(), turn.Checkpoint)
	if err != nil || len(set.Files) != 1 || set.Files[0] != domain.NewFileAttachmentInput(files[0]) || set.OmittedCount != 0 {
		t.Fatalf("exact read-only set %#v %v", set, err)
	}
	if _, err := os.Stat(st.FileAttachmentInputDirectory()); !os.IsNotExist(err) {
		t.Fatal("observation materialized raw bytes", err)
	}
	for _, mutate := range []func(*domain.SupervisorCheckpoint){func(cp *domain.SupervisorCheckpoint) { cp.NextTurn++ }, func(cp *domain.SupervisorCheckpoint) { cp.LeaseGeneration++ }, func(cp *domain.SupervisorCheckpoint) { cp.AttemptID = "different-attempt" }, func(cp *domain.SupervisorCheckpoint) { cp.PendingAttachmentCount = 0 }} {
		changed := turn.Checkpoint
		mutate(&changed)
		if _, err := st.ListSupervisorFileAttachmentInputs(t.Context(), changed); apperror.CodeOf(err) != apperror.CodeConflict {
			t.Fatal("changed checkpoint accepted", err)
		}
	}
	var count int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM workspace_file_attachments`).Scan(&count); err != nil || count != 4 {
		t.Fatal("read-only selection changed imports", count, err)
	}
}
