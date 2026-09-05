package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
)

func TestThreadEpochTransitionFencesActiveLeaseAndReplaysExactly(t *testing.T) {
	ctx := context.Background()
	state, err := Open(filepath.Join(t.TempDir(), "thread-epoch-transition.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	_, created, err := application.NewRunService(state).Create(ctx,
		application.CreateRunRequest{Goal: "advance a quiescent execution epoch",
			Profile: "review", Interactive: true, Budget: domain.Budget{MaxTurns: 4}})
	if err != nil {
		t.Fatal(err)
	}
	started, err := application.NewRunService(state).Start(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	threadRecord, err := state.GetThreadByRun(ctx, started.ID)
	if err != nil {
		t.Fatal(err)
	}
	queued, err := state.EnqueueOperatorSteering(ctx, domain.EnqueueOperatorSteeringRequest{
		RunID: started.ID, SessionID: started.SessionID,
		Content:      "cancel this old pending input when the epoch advances",
		OperationKey: "thread-epoch-old-steering-0001",
		RequestedBy:  "thread_epoch_test_operator",
	})
	if err != nil {
		t.Fatal(err)
	}
	acquired, err := state.AcquireRunExecutionLease(ctx,
		domain.AcquireRunExecutionLeaseRequest{
			RunID: started.ID, OwnerID: "thread-epoch-test-worker", TTL: time.Minute,
		})
	if err != nil {
		t.Fatal(err)
	}
	const operationKey = "thread-epoch-transition-control-0001"
	if _, _, _, err := state.AdvanceThreadRunForPendingConfiguration(ctx,
		threadRecord.ID, started.ID, "thread_epoch_test_operator",
		operationKey); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("active lease transition code=%s err=%v", apperror.CodeOf(err), err)
	}
	storedRun, err := state.GetRun(ctx, started.ID)
	if err != nil || storedRun.Status != domain.RunRunning {
		t.Fatalf("active lease terminalized Run=%+v err=%v", storedRun, err)
	}
	storedThread, err := state.GetThread(ctx, threadRecord.ID)
	if err != nil || storedThread.ActiveRunID != started.ID ||
		storedThread.LastRunID != started.ID {
		t.Fatalf("active lease changed Thread projection=%+v err=%v", storedThread, err)
	}
	storedMessage, err := state.GetOperatorSteering(ctx, queued.Message.ID)
	if err != nil || storedMessage.Status != domain.OperatorSteeringPending {
		t.Fatalf("active lease cancelled pending input=%+v err=%v", storedMessage, err)
	}
	if _, _, err := state.ReleaseRunExecutionLease(ctx, acquired.Lease); err != nil {
		t.Fatal(err)
	}

	advancedThread, superseded, replayed, err :=
		state.AdvanceThreadRunForPendingConfiguration(ctx, threadRecord.ID,
			started.ID, "thread_epoch_test_operator", operationKey)
	if err != nil || replayed || superseded.Status != domain.RunCancelled ||
		advancedThread.ActiveRunID != "" || advancedThread.LastRunID != started.ID {
		t.Fatalf("epoch transition thread=%+v run=%+v replayed=%t err=%v",
			advancedThread, superseded, replayed, err)
	}
	storedMessage, err = state.GetOperatorSteering(ctx, queued.Message.ID)
	if err != nil || storedMessage.Status != domain.OperatorSteeringCancelled {
		t.Fatalf("superseded pending input=%+v err=%v", storedMessage, err)
	}
	replayedThread, replayedRun, replayed, err :=
		state.AdvanceThreadRunForPendingConfiguration(ctx, threadRecord.ID,
			started.ID, "thread_epoch_test_operator", operationKey)
	if err != nil || !replayed || replayedThread != advancedThread ||
		replayedRun.ID != superseded.ID || replayedRun.Status != domain.RunCancelled {
		t.Fatalf("epoch replay thread=%+v run=%+v replayed=%t err=%v",
			replayedThread, replayedRun, replayed, err)
	}
	if _, _, _, err := state.AdvanceThreadRunForPendingConfiguration(ctx,
		threadRecord.ID, started.ID, "thread_epoch_test_operator",
		"thread-epoch-transition-other-0001"); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("different transition key code=%s err=%v", apperror.CodeOf(err), err)
	}
}
