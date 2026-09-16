package store

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/session"
	"cyberagent-workbench/internal/workspace"
)

func threadIntentFixture(t *testing.T) (*SQLiteStore, *SQLiteStore, domain.Run, domain.ThreadMessageIntentRequest, []session.PreparedEvidenceAttachment) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "thread-intent.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	other, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	root := t.TempDir()
	if err := st.SaveWorkspace(t.Context(), WorkspaceRecord{ID: "ws-intent", Name: "intent", RootPath: root, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	_, created, err := application.NewRunService(st).Create(t.Context(), application.CreateRunRequest{Goal: "intent file review", Profile: "review", WorkspaceID: "ws-intent", ModelRoute: "test/model", Interactive: true})
	if err != nil {
		t.Fatal(err)
	}
	run, err := application.NewRunService(st).Start(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	request := domain.ThreadMessageIntentRequest{ThreadID: domain.InitialThreadID(run.ID), Content: "Review files", OperationKey: "thread-intent-operation-0001", RequestedBy: "test_operator"}
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("fixture "+name), 0600); err != nil {
			t.Fatal(err)
		}
		projection, err := workspace.Explore(root, "ws-intent", name)
		if err != nil {
			t.Fatal(err)
		}
		request.Files = append(request.Files, domain.WorkspaceFileReference{SourceKind: session.SourceWorkspaceFile, Path: name, ExpectedSHA256: projection.Provenance.ContentSHA256})
	}
	intent, err := st.ReserveThreadMessageIntent(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	var prepared []session.PreparedEvidenceAttachment
	for index, file := range request.Files {
		item, err := application.NewEvidenceAttachmentService(st).PrepareForThread(t.Context(), application.AttachEvidenceRequest{
			Version: session.EvidenceAttachmentProtocolVersion, RunID: run.ID, SourceKind: file.SourceKind, SourceRef: file.Path, ContentSHA256: file.ExpectedSHA256,
			OperationKey: domain.ThreadMessageFileOperationKey(intent.OperationKeyDigest, index), AttachedBy: request.RequestedBy})
		if err != nil {
			t.Fatal(err)
		}
		prepared = append(prepared, item)
	}
	return st, other, run, request, prepared
}

func assertThreadIntentCounts(t *testing.T, st *SQLiteStore, run domain.Run, wantFiles, wantMessages int) {
	t.Helper()
	items, err := st.ListEvidenceAttachments(t.Context(), run.ID, 10)
	if err != nil || len(items) != wantFiles {
		t.Fatalf("evidence count=%d want=%d err=%v", len(items), wantFiles, err)
	}
	queued, err := st.ListOperatorSteering(t.Context(), run.ID, 10)
	if err != nil || len(queued) != wantMessages {
		t.Fatalf("message count=%d want=%d err=%v", len(queued), wantMessages, err)
	}
	messages, err := st.ListSessionMessages(t.Context(), run.SessionID, true)
	if err != nil || len(messages) != wantFiles {
		t.Fatalf("context count=%d want=%d err=%v", len(messages), wantFiles, err)
	}
}

func TestThreadMessageIntentAtomicEvidenceAndQueueRollback(t *testing.T) {
	st, other, run, request, prepared := threadIntentFixture(t)
	broken := append([]session.PreparedEvidenceAttachment(nil), prepared...)
	broken[1].Message.Content = "tampered snapshot"
	if _, err := st.CommitThreadMessage(t.Context(), request, run.ID, broken); err == nil {
		t.Fatal("invalid second snapshot accepted")
	}
	assertThreadIntentCounts(t, other, run, 0, 0)
	intent, err := other.ReserveThreadMessageIntent(t.Context(), request)
	if err != nil || intent.RunID != "" || intent.MessageID != "" {
		t.Fatalf("partial intent binding=%#v %v", intent, err)
	}
	queued, err := other.CommitThreadMessage(t.Context(), request, run.ID, prepared)
	if err != nil || queued.Replayed {
		t.Fatalf("atomic commit=%#v %v", queued, err)
	}
	assertThreadIntentCounts(t, st, run, 2, 1)
	var attachedSequence, queuedSequence int64
	if err := st.db.QueryRowContext(t.Context(), `SELECT MAX(sequence) FROM run_events WHERE run_id=? AND type='session.evidence_attached'`, run.ID).Scan(&attachedSequence); err != nil {
		t.Fatal(err)
	}
	if err := st.db.QueryRowContext(t.Context(), `SELECT MIN(sequence) FROM run_events WHERE run_id=? AND type='operator.steering_queued'`, run.ID).Scan(&queuedSequence); err != nil {
		t.Fatal(err)
	}
	if attachedSequence >= queuedSequence {
		t.Fatalf("evidence sequence=%d queue sequence=%d", attachedSequence, queuedSequence)
	}
}

func TestThreadMessageIntentConcurrentStoresCommitExactlyOnce(t *testing.T) {
	st, other, run, request, prepared := threadIntentFixture(t)
	start := make(chan struct{})
	results := make(chan domain.OperatorSteeringEnqueueResult, 2)
	errs := make(chan error, 2)
	var done sync.WaitGroup
	for _, current := range []*SQLiteStore{st, other} {
		done.Add(1)
		go func(current *SQLiteStore) {
			defer done.Done()
			<-start
			result, err := current.CommitThreadMessage(context.Background(), request, run.ID, prepared)
			results <- result
			errs <- err
		}(current)
	}
	close(start)
	done.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	first := <-results
	second := <-results
	if first.Message.ID == "" || first.Message.ID != second.Message.ID || first.Replayed == second.Replayed {
		t.Fatalf("concurrent results=%#v %#v", first, second)
	}
	assertThreadIntentCounts(t, st, run, 2, 1)
}

func TestThreadMessageIntentRejectionAndCommitAreMutuallyExclusiveAcrossStores(t *testing.T) {
	for _, rejectFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "rejection_wins", false: "commit_wins"}[rejectFirst], func(t *testing.T) {
			st, other, run, request, prepared := threadIntentFixture(t)
			if rejectFirst {
				rejected, err := other.RejectThreadMessageIntent(t.Context(), request)
				if err != nil || !rejected {
					t.Fatalf("rejection=%t %v", rejected, err)
				}
				if _, err := st.CommitThreadMessage(t.Context(), request, run.ID, prepared); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
					t.Fatalf("rejected intent committed: %v", err)
				}
				intent, err := st.ReserveThreadMessageIntent(t.Context(), request)
				if err != nil || !intent.Rejected {
					t.Fatalf("rejected state=%#v %v", intent, err)
				}
				assertThreadIntentCounts(t, st, run, 0, 0)
			} else {
				accepted, err := st.CommitThreadMessage(t.Context(), request, run.ID, prepared)
				if err != nil {
					t.Fatal(err)
				}
				rejected, err := other.RejectThreadMessageIntent(t.Context(), request)
				if err != nil || rejected {
					t.Fatalf("accepted intent falsely rejected: %t %v", rejected, err)
				}
				replay, err := other.CommitThreadMessage(t.Context(), request, run.ID, nil)
				if err != nil || !replay.Replayed || replay.Message.ID != accepted.Message.ID {
					t.Fatalf("replay=%#v %v", replay, err)
				}
				assertThreadIntentCounts(t, st, run, 2, 1)
			}
			changed := request
			changed.Content = "different intent"
			if rejected, err := other.RejectThreadMessageIntent(t.Context(), changed); err != nil || rejected {
				t.Fatalf("different intent received false acknowledgement: %t %v", rejected, err)
			}
		})
	}
}

func TestThreadMessageIntentLegacyPlainMessageCannotChangeToFiles(t *testing.T) {
	st, _, run, request, _ := threadIntentFixture(t)
	plain := request
	plain.Files = nil
	if _, err := application.NewThreadService(st).Submit(t.Context(), application.SubmitThreadMessageRequest{Version: domain.ThreadMessageProtocolVersion, ThreadID: request.ThreadID, Content: request.Content, OperationKey: request.OperationKey, RequestedBy: request.RequestedBy}); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("legacy endpoint bypass=%v", err)
	}
	plain.OperationKey = "thread-intent-legacy-operation-0002"
	legacy, err := st.EnqueueOperatorSteering(t.Context(), domain.EnqueueOperatorSteeringRequest{RunID: run.ID, SessionID: run.SessionID, Content: plain.Content, OperationKey: plain.OperationKey, RequestedBy: plain.RequestedBy})
	if err != nil {
		t.Fatal(err)
	}
	intent, err := st.ReserveThreadMessageIntent(t.Context(), plain)
	if err != nil || intent.MessageID != legacy.Message.ID {
		t.Fatalf("legacy adoption=%#v %v", intent, err)
	}
	plain.Files = request.Files
	if _, err := st.ReserveThreadMessageIntent(t.Context(), plain); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("plain to files=%v", err)
	}
}
