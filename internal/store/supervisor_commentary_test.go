package store

import (
	"context"
	"path/filepath"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
)

func TestSupervisorModelPublicCommentaryIsBoundIdempotentAndPreTerminal(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "supervisor-commentary.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	_, run := createStructuredToolTestRun(t, ctx, st, "public commentary ledger")
	if _, err := application.NewRunService(st).Start(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	turn, err := st.BeginSupervisorTurn(ctx,
		acquireTestRunExecutionLease(t, ctx, st, run.ID), "commentary")
	if err != nil {
		t.Fatal(err)
	}
	attempt := llm.ModelAttempt{
		Number: 1, TransportAttempt: 1, MaxAttempts: 3, Provider: "test", Model: "model",
	}
	if inserted, err := st.RecordSupervisorModelStarted(ctx, turn.Checkpoint, attempt); err != nil || !inserted {
		t.Fatalf("model start failed: inserted=%t err=%v", inserted, err)
	}
	commentary := domain.ModelPublicCommentary{
		Version: domain.ModelPublicCommentaryVersion, RunID: run.ID,
		AttemptID: turn.Checkpoint.AttemptID, ModelAttempt: 1, ToolRound: 1,
		Phase: domain.PublicCommentaryBeforeTools, Text: "下一步执行安全工具。",
	}
	inserted, err := st.RecordSupervisorModelPublicCommentary(ctx, turn.Checkpoint, attempt, commentary)
	if err != nil || !inserted {
		t.Fatalf("commentary insert failed: inserted=%t err=%v", inserted, err)
	}
	inserted, err = st.RecordSupervisorModelPublicCommentary(ctx, turn.Checkpoint, attempt, commentary)
	if err != nil || inserted {
		t.Fatalf("commentary replay failed: inserted=%t err=%v", inserted, err)
	}
	mismatch := commentary
	mismatch.Text = "changed"
	if _, err := st.RecordSupervisorModelPublicCommentary(ctx, turn.Checkpoint, attempt, mismatch); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("commentary mismatch code=%s err=%v", apperror.CodeOf(err), err)
	}
	terminal := attempt
	terminal.Outcome = llm.OutcomeSuccess
	if _, err := st.RecordSupervisorModelCompleted(ctx, turn.Checkpoint, terminal, llm.ChatResponse{}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RecordSupervisorModelPublicCommentary(ctx, turn.Checkpoint, attempt, commentary); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("post-terminal commentary code=%s err=%v", apperror.CodeOf(err), err)
	}
}
