package store

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"path/filepath"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
)

func promotionFixture(t *testing.T) (*SQLiteStore, domain.Run, domain.PromoteOperatorSteeringRequest) {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "promotion.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	_, created := createWorkItemTestRun(t, t.Context(), s, "promotion")
	run, err := application.NewRunService(s).Start(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	lease := acquireTestRunExecutionLease(t, t.Context(), s, run.ID)
	turn, err := s.BeginSupervisorTurn(t.Context(), lease, "queued correction")
	if err != nil {
		t.Fatal(err)
	}
	queued, err := s.EnqueueOperatorSteering(t.Context(), domain.EnqueueOperatorSteeringRequest{RunID: run.ID, SessionID: run.SessionID, Content: "queued correction", OperationKey: "promotion-fixture-original", RequestedBy: "test_operator"})
	if err != nil {
		t.Fatal(err)
	}
	return s, run, domain.PromoteOperatorSteeringRequest{SessionID: run.SessionID, MessageID: queued.Message.ID, ExpectedContentSHA256: queued.Message.ContentSHA256, ExpectedAttemptID: turn.Checkpoint.AttemptID, ExpectedExecutionID: "thread-execution-fixture", OperationKey: "promotion-fixture-promote", RequestedBy: "test_operator"}
}

func TestPromotionRejectsBoundAttachmentsWithoutChangingQueue(t *testing.T) {
	for _, kind := range []string{"image", "file"} {
		t.Run(kind, func(t *testing.T) {
			st, err := Open(filepath.Join(t.TempDir(), "promotion-attachments.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			if err := st.SaveWorkspace(t.Context(), WorkspaceRecord{ID: "ws-promotion-attachments", Name: "promotion attachments", RootPath: t.TempDir(), CreatedAt: time.Now().UTC()}); err != nil {
				t.Fatal(err)
			}
			runs := application.NewRunService(st)
			_, created, err := runs.Create(t.Context(), application.CreateRunRequest{Goal: "preserve attached queue", Profile: "review", WorkspaceID: "ws-promotion-attachments", Budget: domain.Budget{MaxTurns: 3}})
			if err != nil {
				t.Fatal(err)
			}
			run, err := runs.Start(t.Context(), created.ID)
			if err != nil {
				t.Fatal(err)
			}
			lease := acquireTestRunExecutionLease(t, t.Context(), st, run.ID)
			turn, err := st.BeginSupervisorTurn(t.Context(), lease, "original task")
			if err != nil {
				t.Fatal(err)
			}
			input := domain.EnqueueOperatorSteeringRequest{RunID: run.ID, SessionID: run.SessionID, Content: "keep attachment", OperationKey: "promotion-attachment-queued-0001", RequestedBy: "operator"}
			if kind == "image" {
				var data bytes.Buffer
				if err := png.Encode(&data, image.NewNRGBA(image.Rect(0, 0, 2, 3))); err != nil {
					t.Fatal(err)
				}
				file, err := st.SaveWorkspaceImage(t.Context(), "ws-promotion-attachments", "promotion-image-upload-0001", "image/png", "image.png", data.Bytes())
				if err != nil {
					t.Fatal(err)
				}
				input.Images = []domain.ImageReference{{ID: file.ID, SHA256: file.SHA256}}
			} else {
				file, err := st.SaveWorkspaceFileAttachment(t.Context(), "ws-promotion-attachments", "promotion-file-upload-0001", "text/plain", "note.txt", []byte("attachment must remain"))
				if err != nil {
					t.Fatal(err)
				}
				input.Attachments = []domain.FileAttachmentReference{{ID: file.ID, SHA256: file.SHA256, WorkspaceID: file.WorkspaceID, ByteSize: file.ByteSize}}
			}
			queued, err := st.EnqueueOperatorSteering(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			result, err := st.PromoteOperatorSteering(t.Context(), domain.PromoteOperatorSteeringRequest{SessionID: run.SessionID, MessageID: queued.Message.ID, ExpectedContentSHA256: queued.Message.ContentSHA256, ExpectedAttemptID: turn.Checkpoint.AttemptID, ExpectedExecutionID: "promotion-test-execution", OperationKey: "promotion-attachment-denied-0001", RequestedBy: "operator"})
			if err != nil || result.Rejection == nil {
				t.Fatalf("attachment promotion=%#v %v", result, err)
			}
			snapshot, err := st.ListThreadQueuedMessages(t.Context(), domain.InitialThreadID(run.ID))
			if err != nil || len(snapshot.Messages) != 1 || snapshot.Messages[0].Message.ID != queued.Message.ID || len(snapshot.Messages[0].Images)+len(snapshot.Messages[0].Attachments) != 1 {
				t.Fatalf("attachment lost=%#v %v", snapshot, err)
			}
		})
	}
}

func TestPromotionSerializesWithOldToolExecutionStart(t *testing.T) {
	for _, startedFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(startedFirst), func(t *testing.T) {
			f := newProviderReplayFixture(t)
			f.start(t)
			f.attempt.Outcome = llm.OutcomeSuccess
			if _, err := f.store.RecordSupervisorModelCompleted(t.Context(), f.turn.Checkpoint, f.attempt, f.response); err != nil {
				t.Fatal(err)
			}
			q, err := f.store.EnqueueOperatorSteering(t.Context(), domain.EnqueueOperatorSteeringRequest{RunID: f.turn.Run.ID, SessionID: f.turn.Run.SessionID, Content: "do not execute remaining tools", OperationKey: "promotion-tool-queue-0001", RequestedBy: "operator"})
			if err != nil {
				t.Fatal(err)
			}
			callID := f.response.ToolCalls[0].ID
			if startedFirst {
				if started, superseded, err := f.store.RecordSupervisorToolExecutionStartedWithSteering(t.Context(), f.turn.Checkpoint, callID); err != nil || !started || superseded {
					t.Fatalf("first start=%t %v", started, err)
				}
			}
			promoted, err := f.store.PromoteOperatorSteering(t.Context(), domain.PromoteOperatorSteeringRequest{SessionID: q.Message.SessionID, MessageID: q.Message.ID, ExpectedContentSHA256: q.Message.ContentSHA256, ExpectedAttemptID: f.turn.Checkpoint.AttemptID, ExpectedExecutionID: "execution-tool-test", OperationKey: "promotion-tool-promote-0001", RequestedBy: "operator"})
			if err != nil {
				t.Fatal(err)
			}
			if !startedFirst {
				if started, superseded, err := f.store.RecordSupervisorToolExecutionStartedWithSteering(t.Context(), f.turn.Checkpoint, callID); err != nil || started || !superseded {
					t.Fatalf("outdated start accepted=%t %v", started, err)
				}
			}
			var count int
			if err := f.store.db.QueryRow(`SELECT COUNT(*) FROM run_events WHERE run_id=? AND type='supervisor.tool_execution_started'`, f.turn.Run.ID).Scan(&count); err != nil {
				t.Fatal(err)
			}
			want := 0
			if startedFirst {
				want = 1
			}
			if count != want {
				t.Fatalf("execution starts=%d want=%d", count, want)
			}
			claimed, _, err := f.store.ClaimSupervisorMidTurnSteering(t.Context(), f.turn.Checkpoint)
			if err != nil || len(claimed) != 1 || claimed[0].ID != promoted.Receipt.ReplacementMessageID {
				t.Fatalf("claim=%#v %v", claimed, err)
			}
		})
	}
}

func TestPromotionCompetesWithEditOrCancellation(t *testing.T) {
	for _, action := range []string{"edit", "cancel"} {
		t.Run(action, func(t *testing.T) {
			s, _, r := promotionFixture(t)
			start := make(chan struct{})
			results := make(chan error, 2)
			go func() {
				<-start
				result, err := s.PromoteOperatorSteering(t.Context(), r)
				if err == nil && result.Rejection != nil {
					err = fmt.Errorf("promotion rejected")
				}
				results <- err
			}()
			go func() {
				<-start
				var err error
				if action == "edit" {
					_, err = s.ReviseOperatorSteering(t.Context(), domain.ReviseOperatorSteeringRequest{SessionID: r.SessionID, MessageID: r.MessageID, ExpectedRevision: r.ExpectedRevision, Content: "newer revision wins", OperationKey: "promotion-competing-edit-0001", RequestedBy: r.RequestedBy})
				} else {
					_, err = s.CancelOperatorSteering(t.Context(), domain.CancelOperatorSteeringRequest{MessageID: r.MessageID, OperationKey: "promotion-competing-cancel-0001", RequestedBy: r.RequestedBy, Reason: "cancel pending"})
				}
				results <- err
			}()
			close(start)
			first, second := <-results, <-results
			if (first == nil) == (second == nil) {
				t.Fatalf("expected exactly one winner: %v / %v", first, second)
			}
			var promotions, replacements int
			if err := s.db.QueryRow(`SELECT count(*) FROM operator_steering_promotions`).Scan(&promotions); err != nil {
				t.Fatal(err)
			}
			if err := s.db.QueryRow(`SELECT count(*) FROM operator_steering_messages WHERE delivery_mode='steer'`).Scan(&replacements); err != nil {
				t.Fatal(err)
			}
			if promotions != replacements || promotions > 1 {
				t.Fatalf("partial promotion: %d receipts, %d replacements", promotions, replacements)
			}
		})
	}
}

func TestPromotionSuccessAndRejectionAreExclusiveAcrossStoresAndReopen(t *testing.T) {
	for _, order := range []string{"success_first", "rejection_first", "concurrent"} {
		t.Run(order, func(t *testing.T) {
			s, _, input := promotionFixture(t)
			var path string
			if err := s.db.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path); err != nil {
				t.Fatal(err)
			}
			other, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer other.Close()
			var first, second domain.PromoteOperatorSteeringResult
			switch order {
			case "success_first":
				first, err = s.PromoteOperatorSteering(t.Context(), input)
				if err != nil {
					t.Fatal(err)
				}
				second, err = other.RejectOperatorSteeringPromotion(t.Context(), input)
			case "rejection_first":
				first, err = other.RejectOperatorSteeringPromotion(t.Context(), input)
				if err != nil {
					t.Fatal(err)
				}
				second, err = s.PromoteOperatorSteering(t.Context(), input)
			case "concurrent":
				type answer struct {
					result domain.PromoteOperatorSteeringResult
					err    error
				}
				answers := make(chan answer, 2)
				start := make(chan struct{})
				go func() { <-start; r, e := s.PromoteOperatorSteering(t.Context(), input); answers <- answer{r, e} }()
				go func() {
					<-start
					r, e := other.RejectOperatorSteeringPromotion(t.Context(), input)
					answers <- answer{r, e}
				}()
				close(start)
				a, b := <-answers, <-answers
				if a.err != nil || b.err != nil {
					t.Fatalf("race errors: %v %v", a.err, b.err)
				}
				first, second = a.result, b.result
			}
			if err != nil {
				t.Fatal(err)
			}
			if (first.Rejection == nil) != (second.Rejection == nil) {
				t.Fatalf("split outcome: %#v / %#v", first, second)
			}
			var count int
			if err := s.db.QueryRow(`SELECT (SELECT count(*) FROM operator_steering_promotions)+(SELECT count(*) FROM operator_steering_promotion_rejections)`).Scan(&count); err != nil || count != 1 {
				t.Fatalf("mutually exclusive receipts=%d %v", count, err)
			}
			reopened, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			observation, err := reopened.InspectOperatorSteeringPromotion(t.Context(), input.SessionID, input.MessageID, input.OperationKey, input.RequestedBy)
			if err != nil {
				t.Fatal(err)
			}
			changed := input
			changed.ExpectedExecutionID = "different-execution"
			if _, err := reopened.PromoteOperatorSteering(t.Context(), changed); apperror.CodeOf(err) != apperror.CodeConflict {
				t.Fatalf("changed replay accepted: %v", err)
			}
			if first.Rejection == nil {
				if first.Receipt.ID != second.Receipt.ID || observation.State != "sealed" {
					t.Fatal("success receipt changed")
				}
			} else {
				if first.Rejection.ID != second.Rejection.ID || observation.State != "rejected" {
					t.Fatal("rejection receipt changed")
				}
				for _, sql := range []string{`UPDATE operator_steering_promotion_rejections SET expected_revision=expected_revision+1`, `DELETE FROM operator_steering_promotion_rejections`} {
					if _, err := s.db.Exec(sql); err == nil {
						t.Fatal("rejection was mutable")
					}
				}
				old, err := s.GetOperatorSteering(t.Context(), input.MessageID)
				if err != nil || old.Status != domain.OperatorSteeringPending {
					t.Fatalf("rejection lost queue=%#v %v", old, err)
				}
				fresh := input
				fresh.OperationKey = "promotion-after-rejection-fresh-0001"
				admitted, err := s.PromoteOperatorSteering(t.Context(), fresh)
				if err != nil || admitted.Rejection != nil || admitted.Receipt.ID == "" {
					t.Fatalf("new intent blocked=%#v %v", admitted, err)
				}
				oldRetry, err := other.PromoteOperatorSteering(t.Context(), input)
				if err != nil || oldRetry.Rejection == nil || oldRetry.Rejection.ID != first.Rejection.ID {
					t.Fatalf("old rejection changed=%#v %v", oldRetry, err)
				}
			}
		})
	}
}

func TestPromotionAtomicFullQueueEditedTextAndImmutableReplay(t *testing.T) {
	s, run, r := promotionFixture(t)
	edit, err := s.ReviseOperatorSteering(t.Context(), domain.ReviseOperatorSteeringRequest{SessionID: r.SessionID, MessageID: r.MessageID, ExpectedRevision: 0, Content: "edited correction", OperationKey: "promotion-edit-original", RequestedBy: r.RequestedBy})
	if err != nil {
		t.Fatal(err)
	}
	r.ExpectedRevision, r.ExpectedContentSHA256 = edit.Message.Revision, edit.Message.ContentSHA256
	for i := 1; i < domain.MaxPendingOperatorSteering; i++ {
		_, err := s.EnqueueOperatorSteering(t.Context(), domain.EnqueueOperatorSteeringRequest{RunID: run.ID, SessionID: run.SessionID, Content: "other", OperationKey: fmt.Sprintf("promotion-other-%03d", i), RequestedBy: r.RequestedBy})
		if err != nil {
			t.Fatal(err)
		}
	}
	result, err := s.PromoteOperatorSteering(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	old, err := s.GetOperatorSteering(t.Context(), r.MessageID)
	if err != nil || old.Status != domain.OperatorSteeringCancelled || old.OriginalContent != "queued correction" || old.Content != "edited correction" {
		t.Fatalf("old=%#v err=%v", old, err)
	}
	replacement, err := s.GetOperatorSteering(t.Context(), result.Receipt.ReplacementMessageID)
	if err != nil || replacement.DeliveryMode != domain.OperatorSteeringCurrentTurn || replacement.Content != "edited correction" || replacement.TargetAttemptID != r.ExpectedAttemptID {
		t.Fatalf("replacement=%#v err=%v", replacement, err)
	}
	summary, err := s.GetOperatorSteeringQueueSummary(t.Context(), run.ID)
	if err != nil || summary.Pending != domain.MaxPendingOperatorSteering {
		t.Fatalf("quota=%#v err=%v", summary, err)
	}
	for _, sql := range []string{`UPDATE operator_steering_promotions SET requested_by='other'`, `DELETE FROM operator_steering_promotions`} {
		if _, err := s.db.ExecContext(t.Context(), sql); err == nil {
			t.Fatal("mutable promotion receipt")
		}
	}
	if _, err := s.db.ExecContext(t.Context(), `UPDATE runs SET status='cancelled' WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	replay, err := s.PromoteOperatorSteering(t.Context(), r)
	if err != nil || !replay.Replayed || replay.Receipt.ID != result.Receipt.ID {
		t.Fatalf("terminal replay=%#v err=%v", replay, err)
	}
	observation, err := s.InspectOperatorSteeringPromotion(t.Context(), r.SessionID, r.MessageID, r.OperationKey, r.RequestedBy)
	if err != nil || observation.State != domain.OperatorSteeringRevisionSealed || observation.Receipt.ID != result.Receipt.ID {
		t.Fatalf("observation=%#v err=%v", observation, err)
	}
	r.ExpectedExecutionID = "another-execution"
	if _, err := s.PromoteOperatorSteering(t.Context(), r); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("changed intent replay=%v", err)
	}
}

func TestPromotionRollsBackCancellationWhenReplacementOrReceiptFails(t *testing.T) {
	for _, table := range []string{"operator_steering_messages", "operator_steering_promotions"} {
		t.Run(table, func(t *testing.T) {
			s, run, r := promotionFixture(t)
			if _, err := s.db.ExecContext(t.Context(), `CREATE TRIGGER promotion_injected_failure BEFORE INSERT ON `+table+` BEGIN SELECT RAISE(ABORT,'injected failure'); END;`); err != nil {
				t.Fatal(err)
			}
			if _, err := s.PromoteOperatorSteering(t.Context(), r); err == nil {
				t.Fatal("injected failure was ignored")
			}
			old, err := s.GetOperatorSteering(t.Context(), r.MessageID)
			if err != nil || old.Status != domain.OperatorSteeringPending {
				t.Fatalf("lost queue item: %#v %v", old, err)
			}
			for _, table := range []string{"operator_steering_cancellations", "operator_steering_promotions"} {
				var count int
				if err := s.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM `+table+` WHERE run_id=?`, run.ID).Scan(&count); err != nil || count != 0 {
					t.Fatalf("partial %s=%d err=%v", table, count, err)
				}
			}
			var messages int
			if err := s.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM operator_steering_messages WHERE run_id=? AND delivery_mode='steer'`, run.ID).Scan(&messages); err != nil || messages != 0 {
				t.Fatalf("orphan correction=%d err=%v", messages, err)
			}
		})
	}
}

func TestPromotionRejectsChangedRevisionAttemptAndPausedRun(t *testing.T) {
	for _, change := range []string{"revision", "digest", "attempt", "paused", "claimed", "old_run"} {
		t.Run(change, func(t *testing.T) {
			s, run, r := promotionFixture(t)
			switch change {
			case "revision":
				r.ExpectedRevision++
			case "digest":
				r.ExpectedContentSHA256 = domain.OperatorSteeringContentSHA256("other")
			case "attempt":
				r.ExpectedAttemptID = "attempt-old"
			case "paused":
				if _, err := s.db.Exec(`UPDATE runs SET status='paused' WHERE id=?`, run.ID); err != nil {
					t.Fatal(err)
				}
			case "claimed":
				if _, err := s.db.Exec(`INSERT INTO operator_steering_deliveries(id,message_id,run_id,attempt_id,turn,status,prepared_at)
					SELECT 'delivery-promote-test',?,?,?,next_turn,'prepared',? FROM run_supervisor_checkpoints WHERE run_id=?`, r.MessageID, run.ID, r.ExpectedAttemptID, ts(run.UpdatedAt), run.ID); err != nil {
					t.Fatal(err)
				}
			case "old_run":
				if _, err := s.db.Exec(`UPDATE threads SET active_run_id=NULL WHERE active_run_id=?`, run.ID); err != nil {
					t.Fatal(err)
				}
			}
			if result, err := s.PromoteOperatorSteering(context.Background(), r); err != nil || result.Rejection == nil {
				t.Fatalf("invalid promotion not sealed=%#v %v", result, err)
			}
			old, err := s.GetOperatorSteering(t.Context(), r.MessageID)
			if err != nil || old.Status != domain.OperatorSteeringPending {
				t.Fatalf("rejected promotion changed queue: %#v %v", old, err)
			}
		})
	}
}
