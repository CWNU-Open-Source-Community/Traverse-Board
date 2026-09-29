package store

import (
	"context"
	"path/filepath"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
)

func TestMidTurnSteeringClaimEditCancelReplayAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "midturn.db")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	ctx := context.Background()
	_, created := createWorkItemTestRun(t, ctx, first, "midturn claim")
	run, err := application.NewRunService(first).Start(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	lease := acquireTestRunExecutionLease(t, ctx, first, run.ID)
	turn, err := first.BeginSupervisorTurn(ctx, lease, "original current task")
	if err != nil {
		t.Fatal(err)
	}
	steer := domain.EnqueueOperatorSteeringRequest{RunID: run.ID, SessionID: run.SessionID,
		Content: "first correction", OperationKey: "midturn-store-original-0001",
		RequestedBy: "operator", DeliveryMode: domain.OperatorSteeringCurrentTurn}
	accepted, err := first.EnqueueOperatorSteering(ctx, steer)
	if err != nil || accepted.Message.TargetAttemptID != turn.Checkpoint.AttemptID {
		t.Fatalf("steer binding=%#v err=%v", accepted, err)
	}
	queued, err := first.EnqueueOperatorSteering(ctx, domain.EnqueueOperatorSteeringRequest{
		RunID: run.ID, SessionID: run.SessionID, Content: "next turn only",
		OperationKey: "midturn-store-next-0001", RequestedBy: "operator"})
	if err != nil || queued.Message.DeliveryMode != domain.OperatorSteeringNextTurn {
		t.Fatalf("next-turn compatibility=%#v err=%v", queued, err)
	}
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	revised, err := second.ReviseOperatorSteering(ctx, domain.ReviseOperatorSteeringRequest{
		SessionID: run.SessionID, MessageID: accepted.Message.ID, ExpectedRevision: 0,
		Content: "edited correction", OperationKey: "midturn-store-edit-0001", RequestedBy: "operator"})
	if err != nil || revised.Message.Content != "edited correction" {
		t.Fatalf("preclaim edit=%#v err=%v", revised, err)
	}
	claimed, sequence, err := first.ClaimSupervisorMidTurnSteering(ctx, turn.Checkpoint)
	if err != nil || len(claimed) != 1 || claimed[0].ID != accepted.Message.ID ||
		claimed[0].Content != "edited correction" || !claimed[0].Prepared ||
		sequence != accepted.Message.Sequence {
		t.Fatalf("claim=%#v seq=%d err=%v", claimed, sequence, err)
	}
	reopened, reopenedSequence, err := second.ClaimSupervisorMidTurnSteering(ctx, turn.Checkpoint)
	if err != nil || len(reopened) != 1 || reopened[0].Content != claimed[0].Content ||
		reopenedSequence != sequence {
		t.Fatalf("reopen claim=%#v seq=%d err=%v", reopened, reopenedSequence, err)
	}
	if _, err := second.ReviseOperatorSteering(ctx, domain.ReviseOperatorSteeringRequest{
		SessionID: run.SessionID, MessageID: accepted.Message.ID, ExpectedRevision: 1,
		Content: "too late", OperationKey: "midturn-store-late-edit-0001",
		RequestedBy: "operator"}); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("claimed edit code=%s err=%v", apperror.CodeOf(err), err)
	}
	if _, err := second.CancelOperatorSteering(ctx, domain.CancelOperatorSteeringRequest{
		MessageID: accepted.Message.ID, OperationKey: "midturn-store-late-cancel-0001",
		RequestedBy: "operator", Reason: "withdraw correction"}); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("claimed cancellation code=%s err=%v", apperror.CodeOf(err), err)
	}
	replay, err := second.EnqueueOperatorSteering(ctx, steer)
	if err != nil || !replay.Replayed || replay.Message.ID != accepted.Message.ID ||
		replay.Message.Content != "edited correction" {
		t.Fatalf("original-key replay=%#v err=%v", replay, err)
	}
	observed, found, err := second.InspectOperatorSteeringOperation(ctx, run.SessionID, steer.OperationKey)
	if err != nil || !found || observed.ID != accepted.Message.ID || observed.Content != "edited correction" {
		t.Fatalf("original-key observation=%#v found=%t err=%v", observed, found, err)
	}
	if _, found, err := second.InspectOperatorSteeringOperation(ctx, run.SessionID,
		"midturn-store-never-sent-0001"); err != nil || found {
		t.Fatalf("unknown operation found=%t err=%v", found, err)
	}
	pendingNext, err := second.GetOperatorSteering(ctx, queued.Message.ID)
	if err != nil || pendingNext.Status != domain.OperatorSteeringPending || pendingNext.Prepared {
		t.Fatalf("next-turn item was consumed by steer: %#v err=%v", pendingNext, err)
	}
}
