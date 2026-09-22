package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
)

func TestOperatorSteeringRevisionUnchangedProofAndSealedReplayPriority(t *testing.T) {
	st := openWorkItemTestStore(t)
	ctx := context.Background()
	_, created := createWorkItemTestRun(t, ctx, st, "revision unchanged proof")
	run, err := application.NewRunService(st).Start(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	queued, err := st.EnqueueOperatorSteering(ctx, domain.EnqueueOperatorSteeringRequest{
		RunID: run.ID, SessionID: run.SessionID, Content: "same normalized content",
		OperationKey: "unchanged-proof-submit-0001", RequestedBy: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	rawContent := "  same normalized content\r\n"
	operationKey := "unchanged-proof-revision-0001"
	_, err = st.ReviseOperatorSteering(ctx, domain.ReviseOperatorSteeringRequest{
		SessionID: run.SessionID, MessageID: queued.Message.ID, ExpectedRevision: 0,
		Content: rawContent, OperationKey: operationKey, RequestedBy: "operator"})
	var unchanged *domain.OperatorSteeringRevisionUnchangedError
	if apperror.CodeOf(err) != apperror.CodeInvalidArgument || !errors.As(err, &unchanged) || unchanged.RunID != run.ID ||
		unchanged.SessionID != run.SessionID || unchanged.MessageID != queued.Message.ID ||
		unchanged.ExpectedRevision != 0 ||
		unchanged.OperationKeySHA256 != domain.OperatorSteeringContentSHA256(operationKey) ||
		unchanged.RequestContentSHA256 != domain.OperatorSteeringContentSHA256(rawContent) ||
		unchanged.NormalizedContentSHA256 != queued.Message.ContentSHA256 ||
		unchanged.CurrentContentSHA256 != queued.Message.ContentSHA256 {
		t.Fatalf("unchanged revision proof is not exact: %#v err=%v", unchanged, err)
	}
	var receiptCount int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM operator_steering_revisions
		WHERE message_id=?`, queued.Message.ID).Scan(&receiptCount); err != nil || receiptCount != 0 {
		t.Fatalf("unchanged revision persisted a receipt: count=%d err=%v", receiptCount, err)
	}
	revision := domain.ReviseOperatorSteeringRequest{SessionID: run.SessionID,
		MessageID: queued.Message.ID, ExpectedRevision: 0, Content: "changed content",
		OperationKey: "sealed-replay-before-unchanged-0001", RequestedBy: "operator"}
	sealed, err := st.ReviseOperatorSteering(ctx, revision)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := st.ReviseOperatorSteering(ctx, revision)
	if err != nil || !replayed.Replayed || replayed.Receipt.ID != sealed.Receipt.ID {
		t.Fatalf("sealed receipt did not take priority over unchanged current content: %#v err=%v",
			replayed, err)
	}
}

func TestOperatorSteeringRevisionConvergesAcrossStoresAndPreservesOriginalReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "operator-steering-revision.db")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	ctx := context.Background()
	_, created := createWorkItemTestRun(t, ctx, first, "revise queued message")
	run, err := application.NewRunService(first).Start(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	thread, err := first.GetThreadByRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	original := domain.EnqueueOperatorSteeringRequest{RunID: run.ID, SessionID: run.SessionID,
		Content: "original queued content", OperationKey: "revision-original-submit-0001",
		RequestedBy: "operator"}
	queued, err := first.EnqueueOperatorSteering(ctx, original)
	if err != nil {
		t.Fatal(err)
	}
	absent, err := first.InspectOperatorSteeringRevision(ctx, run.SessionID,
		queued.Message.ID, "revision-not-yet-sealed-0001", "operator")
	if err != nil || absent.State != domain.OperatorSteeringRevisionAbsent ||
		absent.Receipt != nil || absent.Message == nil || absent.Message.Revision != 0 ||
		absent.Message.Status != domain.OperatorSteeringPending {
		t.Fatalf("absent revision did not carry the same-snapshot message: %#v err=%v", absent, err)
	}
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	requests := []domain.ReviseOperatorSteeringRequest{
		{SessionID: run.SessionID, MessageID: queued.Message.ID, ExpectedRevision: 0,
			Content: "first revised content", OperationKey: "revision-race-first-0001", RequestedBy: "operator"},
		{SessionID: run.SessionID, MessageID: queued.Message.ID, ExpectedRevision: 0,
			Content: "second revised content", OperationKey: "revision-race-second-0001", RequestedBy: "operator"},
	}
	stores := []*SQLiteStore{first, second}
	start := make(chan struct{})
	results := make(chan domain.ReviseOperatorSteeringResult, 2)
	errs := make(chan error, 2)
	var ready, done sync.WaitGroup
	ready.Add(2)
	done.Add(2)
	for i := range stores {
		i := i
		go func() {
			defer done.Done()
			ready.Done()
			<-start
			result, reviseErr := stores[i].ReviseOperatorSteering(ctx, requests[i])
			if reviseErr != nil {
				errs <- reviseErr
				return
			}
			results <- result
		}()
	}
	ready.Wait()
	close(start)
	done.Wait()
	close(results)
	close(errs)
	var winner domain.ReviseOperatorSteeringResult
	for result := range results {
		if winner.Message.ID != "" {
			t.Fatalf("both competing revisions succeeded: %#v %#v", winner, result)
		}
		winner = result
	}
	var loser error
	for err := range errs {
		if loser != nil {
			t.Fatalf("both competing revisions failed: %v / %v", loser, err)
		}
		loser = err
	}
	if winner.Message.ID == "" || apperror.CodeOf(loser) != apperror.CodeConflict {
		t.Fatalf("revision race did not converge: winner=%#v loser=%v", winner, loser)
	}
	if winner.Message.Revision != 1 || winner.Message.OriginalContent != original.Content ||
		winner.Message.OriginalContentSHA256 != queued.Message.ContentSHA256 || winner.Message.EditedAt == nil {
		t.Fatalf("revision lost immutable original identity: %#v", winner.Message)
	}
	losingRequest := requests[0]
	if winner.Message.Content == requests[0].Content {
		losingRequest = requests[1]
	}
	losingInspection, err := first.InspectOperatorSteeringRevision(ctx, run.SessionID,
		queued.Message.ID, losingRequest.OperationKey, losingRequest.RequestedBy)
	if err != nil || losingInspection.State != domain.OperatorSteeringRevisionAbsent ||
		losingInspection.Message == nil || losingInspection.Message.Revision != 1 {
		t.Fatalf("unsealed competing key did not observe the winning revision: %#v err=%v",
			losingInspection, err)
	}

	replayed, err := first.EnqueueOperatorSteering(ctx, original)
	if err != nil || !replayed.Replayed || replayed.Message.ID != queued.Message.ID ||
		replayed.Message.Revision != 1 {
		t.Fatalf("original submission key no longer observed revised message: %#v err=%v", replayed, err)
	}
	winningRequest := requests[0]
	if winner.Message.Content == requests[1].Content {
		winningRequest = requests[1]
	}
	revisionReplay, err := second.ReviseOperatorSteering(ctx, winningRequest)
	if err != nil || !revisionReplay.Replayed || revisionReplay.Receipt.ID != winner.Receipt.ID {
		t.Fatalf("revision receipt replay did not converge: %#v err=%v", revisionReplay, err)
	}
	inspection, err := first.InspectOperatorSteeringRevision(ctx, run.SessionID,
		queued.Message.ID, winningRequest.OperationKey, winningRequest.RequestedBy)
	if err != nil || inspection.State != domain.OperatorSteeringRevisionSealed ||
		inspection.Receipt == nil || inspection.Receipt.ID != winner.Receipt.ID {
		t.Fatalf("sealed revision was not observable: %#v err=%v", inspection, err)
	}

	snapshot, err := first.ListThreadQueuedMessages(ctx, thread.ID)
	if err != nil || snapshot.RunID != run.ID || snapshot.RunStatus != domain.RunRunning ||
		len(snapshot.Messages) != 1 || snapshot.Messages[0].Message.Content != winner.Message.Content ||
		len(snapshot.Messages[0].Images) != 0 || len(snapshot.Messages[0].Attachments) != 0 {
		t.Fatalf("queued snapshot is inconsistent: %#v err=%v", snapshot, err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	broken, err := second.InspectOperatorSteeringRevision(ctx, run.SessionID,
		queued.Message.ID, winningRequest.OperationKey, winningRequest.RequestedBy)
	if err == nil || broken.State == domain.OperatorSteeringRevisionAbsent {
		t.Fatalf("receipt read failure was reported absent: %#v err=%v", broken, err)
	}
}

func TestOperatorSteeringCancellationInspectionDistinguishesExactOperationAndTerminalState(t *testing.T) {
	st := openWorkItemTestStore(t)
	ctx := context.Background()
	_, created := createWorkItemTestRun(t, ctx, st, "observe cancellation operation")
	run, err := application.NewRunService(st).Start(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	queued, err := st.EnqueueOperatorSteering(ctx, domain.EnqueueOperatorSteeringRequest{
		RunID: run.ID, SessionID: run.SessionID, Content: "cancel this pending message",
		OperationKey: "cancel-inspection-submit-0001", RequestedBy: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	const cancellationKey = "cancel-inspection-operation-0001"
	cancelled, err := st.CancelOperatorSteering(ctx, domain.CancelOperatorSteeringRequest{
		MessageID: queued.Message.ID, OperationKey: cancellationKey,
		RequestedBy: "operator", Reason: "withdraw exact pending input"})
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := st.InspectOperatorSteeringCancellation(ctx, run.SessionID,
		queued.Message.ID, cancellationKey, "operator")
	if err != nil || sealed.State != domain.OperatorSteeringCancellationSealed ||
		sealed.Message != nil || sealed.Receipt == nil ||
		sealed.Receipt.CancellationID != cancelled.Cancellation.ID ||
		sealed.Receipt.ReasonSHA256 != cancelled.Cancellation.ReasonSHA256 {
		t.Fatalf("exact cancellation receipt was not observable: %#v err=%v", sealed, err)
	}
	absent, err := st.InspectOperatorSteeringCancellation(ctx, run.SessionID,
		queued.Message.ID, "different-cancellation-operation-0001", "operator")
	if err != nil || absent.State != domain.OperatorSteeringCancellationAbsent ||
		absent.Receipt != nil || absent.Message == nil ||
		absent.Message.Status != domain.OperatorSteeringCancelled {
		t.Fatalf("different cancellation key did not return current state: %#v err=%v", absent, err)
	}
	if _, err := st.InspectOperatorSteeringCancellation(ctx, run.SessionID,
		queued.Message.ID, cancellationKey, "another_operator"); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("cancellation receipt actor mismatch was not rejected: %v", err)
	}
	if _, err := st.InspectOperatorSteeringCancellation(ctx, run.SessionID,
		"missing-steering-message", cancellationKey, "operator"); apperror.CodeOf(err) != apperror.CodeNotFound {
		t.Fatalf("missing cancellation observation message was not 404: %v", err)
	}
}

func TestPreparedAndTerminalCancellationObservationsRemainAbsentForUnrelatedKey(t *testing.T) {
	st := openWorkItemTestStore(t)
	ctx := context.Background()
	_, created := createWorkItemTestRun(t, ctx, st, "prepared cancellation observation")
	run, err := application.NewRunService(st).Start(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := st.EnqueueOperatorSteering(ctx, domain.EnqueueOperatorSteeringRequest{
		RunID: run.ID, SessionID: run.SessionID, Content: "prepare but do not cancel",
		OperationKey: "prepared-cancel-observation-submit-0001", RequestedBy: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	lease := acquireTestRunExecutionLease(t, ctx, st, run.ID)
	if _, err := st.BeginSupervisorTurn(ctx, lease, ""); err != nil {
		t.Fatal(err)
	}
	inspection, err := st.InspectOperatorSteeringCancellation(ctx, run.SessionID,
		prepared.Message.ID, "prepared-has-no-cancel-operation-0001", "operator")
	if err != nil || inspection.State != domain.OperatorSteeringCancellationAbsent ||
		inspection.Message == nil || inspection.Message.Status != domain.OperatorSteeringPending {
		t.Fatalf("prepared delivery was treated as a sealed cancellation: %#v err=%v", inspection, err)
	}

	_, terminalCreated := createWorkItemTestRun(t, ctx, st, "terminal cancellation observation")
	terminalRun, err := application.NewRunService(st).Start(ctx, terminalCreated.ID)
	if err != nil {
		t.Fatal(err)
	}
	terminal, err := st.EnqueueOperatorSteering(ctx, domain.EnqueueOperatorSteeringRequest{
		RunID: terminalRun.ID, SessionID: terminalRun.SessionID, Content: "cancel on terminal run",
		OperationKey: "terminal-cancel-observation-submit-0001", RequestedBy: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewRunService(st).Fail(ctx, terminalRun.ID, "terminal fixture"); err != nil {
		t.Fatal(err)
	}
	inspection, err = st.InspectOperatorSteeringCancellation(ctx, terminalRun.SessionID,
		terminal.Message.ID, "terminal-cancel-has-no-operator-key-0001", "operator")
	if err != nil || inspection.State != domain.OperatorSteeringCancellationAbsent ||
		inspection.Message == nil || inspection.Message.Status != domain.OperatorSteeringCancelled {
		t.Fatalf("terminal cancellation impersonated an operator receipt: %#v err=%v", inspection, err)
	}
}

func TestOperatorSteeringRevisionRejectsPreparedMessageAndUnreceiptedMutation(t *testing.T) {
	st := openWorkItemTestStore(t)
	ctx := context.Background()
	_, created := createWorkItemTestRun(t, ctx, st, "prepared revision fence")
	run, err := application.NewRunService(st).Start(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	queued, err := st.EnqueueOperatorSteering(ctx, domain.EnqueueOperatorSteeringRequest{
		RunID: run.ID, SessionID: run.SessionID, Content: "prepare this exact content",
		OperationKey: "revision-prepared-submit-0001", RequestedBy: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE operator_steering_messages
		SET content='unreceipted',content_sha256=?,revision=1,edited_at=? WHERE id=?`,
		domain.OperatorSteeringContentSHA256("unreceipted"), ts(queued.Message.CreatedAt), queued.Message.ID); err == nil {
		t.Fatal("unreceipted pending mutation unexpectedly succeeded")
	}
	lease := acquireTestRunExecutionLease(t, ctx, st, run.ID)
	started, err := st.BeginSupervisorTurn(ctx, lease, "")
	if err != nil || started.Checkpoint.PendingInput != queued.Message.Content {
		t.Fatalf("queued message was not prepared: %#v err=%v", started, err)
	}
	_, err = st.ReviseOperatorSteering(ctx, domain.ReviseOperatorSteeringRequest{
		SessionID: run.SessionID, MessageID: queued.Message.ID, ExpectedRevision: 0,
		Content: "too late to revise", OperationKey: "revision-after-prepare-0001", RequestedBy: "operator"})
	if apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("prepared revision was not rejected: code=%s err=%v", apperror.CodeOf(err), err)
	}
	var receipts int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM operator_steering_revisions
		WHERE message_id=?`, queued.Message.ID).Scan(&receipts); err != nil || receipts != 0 {
		t.Fatalf("failed revisions left durable receipts: count=%d err=%v", receipts, err)
	}
}

func TestOperatorSteeringRevisionAndPreparationConvergeAcrossStores(t *testing.T) {
	path := filepath.Join(t.TempDir(), "operator-steering-revise-prepare.db")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	ctx := context.Background()
	_, created := createWorkItemTestRun(t, ctx, first, "revise prepare race")
	run, err := application.NewRunService(first).Start(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	queued, err := first.EnqueueOperatorSteering(ctx, domain.EnqueueOperatorSteeringRequest{
		RunID: run.ID, SessionID: run.SessionID, Content: "content before prepare race",
		OperationKey: "revise-prepare-submit-0001", RequestedBy: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	lease := acquireTestRunExecutionLease(t, ctx, first, run.ID)
	start := make(chan struct{})
	var ready, done sync.WaitGroup
	ready.Add(2)
	done.Add(2)
	var revise domain.ReviseOperatorSteeringResult
	var reviseErr error
	var turn domain.SupervisorTurn
	var turnErr error
	go func() {
		defer done.Done()
		ready.Done()
		<-start
		revise, reviseErr = first.ReviseOperatorSteering(ctx,
			domain.ReviseOperatorSteeringRequest{SessionID: run.SessionID,
				MessageID: queued.Message.ID, ExpectedRevision: 0,
				Content:      "content after prepare race",
				OperationKey: "revise-prepare-edit-0001", RequestedBy: "operator"})
	}()
	go func() {
		defer done.Done()
		ready.Done()
		<-start
		turn, turnErr = second.BeginSupervisorTurn(ctx, lease, "")
	}()
	ready.Wait()
	close(start)
	done.Wait()
	if turnErr != nil {
		t.Fatal(turnErr)
	}
	stored, err := first.GetOperatorSteering(ctx, queued.Message.ID)
	if err != nil || !stored.Prepared {
		t.Fatalf("prepare race lost its durable delivery: %#v err=%v", stored, err)
	}
	var receiptCount int
	if err := first.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM operator_steering_revisions
		WHERE message_id=?`, queued.Message.ID).Scan(&receiptCount); err != nil {
		t.Fatal(err)
	}
	if reviseErr == nil {
		if revise.Message.Revision != 1 || stored.Revision != 1 || receiptCount != 1 ||
			turn.Checkpoint.PendingInput != revise.Message.Content {
			t.Fatalf("revision won but prepare did not load it exactly: revise=%#v stored=%#v turn=%#v receipts=%d",
				revise, stored, turn, receiptCount)
		}
		return
	}
	if apperror.CodeOf(reviseErr) != apperror.CodeFailedPrecondition ||
		stored.Revision != 0 || receiptCount != 0 ||
		turn.Checkpoint.PendingInput != queued.Message.Content {
		t.Fatalf("prepare won without fencing revision: revise_err=%v stored=%#v turn=%#v receipts=%d",
			reviseErr, stored, turn, receiptCount)
	}
}
