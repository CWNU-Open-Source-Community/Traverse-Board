package desktop

import (
	"context"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/httpapi"
	"cyberagent-workbench/internal/runmutation"
)

// Read the failed fixture before its deferred ControlPlane.Close. These reads
// share a bounded context and are a diagnostic snapshot, not an atomic view.
// Only state metadata is logged: never Provider bodies, credentials, pending
// input, event payloads, or raw Store errors.
func logDesktopSourceWiringTurnFailure(t *testing.T, plane *ControlPlane,
	thread httpapi.ThreadCreationControlView, operationKey string,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	runID := thread.Run.ID
	run, err := plane.stateStore.GetRun(ctx, runID)
	t.Logf("Thread turn diagnostic run=%s status=%s updated_at=%s read_error=%s",
		runID, run.Status, run.UpdatedAt.UTC().Format(time.RFC3339Nano), desktopWiringReadError(err))
	checkpoint, found, err := plane.stateStore.GetSupervisorCheckpoint(ctx, runID)
	t.Logf("Thread turn diagnostic checkpoint found=%t phase=%s turn=%d pending_input=%t read_error=%s",
		found, checkpoint.Phase, checkpoint.NextTurn, checkpoint.HasPendingInput(), desktopWiringReadError(err))
	lease, found, err := plane.stateStore.GetRunExecutionLease(ctx, runID)
	t.Logf("Thread turn diagnostic lease found=%t status=%s generation=%d active_now=%t renewed_at=%s expires_at=%s read_error=%s",
		found, lease.Status, lease.Generation, lease.ActiveAt(time.Now().UTC()),
		lease.RenewedAt.UTC().Format(time.RFC3339Nano), lease.ExpiresAt.UTC().Format(time.RFC3339Nano),
		desktopWiringReadError(err))
	// The fixture expects its first handoff to complete its one model-only turn.
	handoffKey := "thread-turn-handoff-" + runmutation.Fingerprint(
		"thread_turn_handoff_operation.v1", thread.Thread.ID, runID, operationKey, "1")
	handoff, found, err := plane.stateStore.GetRunExecutionHandoff(ctx,
		runmutation.RunExecutionHandoffOperationDigest(runID, handoffKey))
	var afterSequence int64
	if err != nil || !found || handoff.Result == nil {
		t.Logf("Thread turn diagnostic first_handoff found=%t completed=%t read_error=%s",
			found, handoff.Result != nil, desktopWiringReadError(err))
	} else {
		result := handoff.Result
		afterSequence = max(int64(0), result.CompletionEventSequence-16)
		t.Logf("Thread turn diagnostic first_handoff status=%s error_code=%s stop_reason=%s steps=%d model_called=%t tool_called=%t pending=%d prepared=%d committed=%d cancelled=%d completed_at=%s",
			result.Status, result.ErrorCode, result.StopReason, result.StepsCompleted,
			result.ModelCalled, result.ToolCalled, result.PendingCount, result.PreparedCount,
			result.CommittedCount, result.CancelledCount, result.CompletedAt.UTC().Format(time.RFC3339Nano))
	}
	timeline, err := plane.stateStore.ListRunEventsAfterSequence(ctx, runID, afterSequence, 32)
	if err != nil {
		t.Logf("Thread turn diagnostic timeline read_error=%s", desktopWiringReadError(err))
		return
	}
	t.Logf("Thread turn diagnostic timeline after_sequence=%d limit=32 observed=%d", afterSequence, len(timeline))
	for _, event := range timeline {
		t.Logf("Thread turn diagnostic event sequence=%d type=%s source=%s created_at=%s",
			event.Sequence, event.Type, event.Source, event.CreatedAt.UTC().Format(time.RFC3339Nano))
	}
}

func desktopWiringReadError(err error) string {
	if err == nil {
		return ""
	}
	return string(apperror.CodeOf(apperror.Normalize(err)))
}
