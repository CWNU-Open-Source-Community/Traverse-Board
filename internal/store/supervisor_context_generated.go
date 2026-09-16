package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/contextmgr"
	"cyberagent-workbench/internal/domain"
)

type supervisorSummaryGeneratorFunc func(context.Context, contextmgr.SummaryGenerationRequest) (contextmgr.SummaryGenerationResponse, error)

func (f supervisorSummaryGeneratorFunc) Generate(ctx context.Context, request contextmgr.SummaryGenerationRequest) (contextmgr.SummaryGenerationResponse, error) {
	return f(ctx, request)
}

// Generated compaction permits only the accounting mutation proven by this
// exact source's terminal receipt. sameSources remains strict for every caller.
func (s *SQLiteStore) CompactSupervisorContextGenerated(ctx context.Context, checkpoint domain.SupervisorCheckpoint,
	preserveRecent int, generator contextmgr.SummaryGenerator,
) (contextmgr.Result, domain.SupervisorCheckpoint, error) {
	abort := func(err error) (contextmgr.Result, domain.SupervisorCheckpoint, error) {
		// No unverified checkpoint on an error: the caller may already hold the
		// terminal's charged checkpoint. Returning the source copy would roll it back.
		return contextmgr.Result{}, domain.SupervisorCheckpoint{}, fmt.Errorf("%w: %w", contextmgr.ErrSummaryGenerationAborted, err)
	}
	if err := checkpoint.Validate(); err != nil {
		return abort(err)
	}
	if generator == nil || preserveRecent < 1 || checkpoint.Phase != domain.SupervisorTurnStarted {
		return abort(apperror.New(apperror.CodeInvalidArgument, "generated compaction requires a generator and started turn"))
	}
	reader, finish, err := s.beginThreadRequestObservation(ctx)
	if err != nil {
		return abort(err)
	}
	snapshot, err := readSupervisorCompactionSnapshot(ctx, reader, checkpoint)
	if err != nil {
		finish()
		return abort(err)
	}
	sourceSHA := supervisorCompactionSourceHash(snapshot)
	previousReceipt, attempted, err := readSupervisorCompactionReceipt(ctx, reader, checkpoint.RunID, sourceSHA)
	finish()
	if err != nil {
		return abort(err)
	}
	checkpoint = snapshot.Checkpoint
	history := snapshot.contextMessages()
	if len(history) <= preserveRecent {
		return contextmgr.Result{Preserved: history}, checkpoint, nil
	}
	if attempted && previousReceipt.Sequence == 0 {
		return abort(errors.New("context compaction has an unresolved potentially billed model attempt"))
	}
	var response contextmgr.SummaryGenerationResponse
	called, returnedCandidate := false, false
	wrapped := supervisorSummaryGeneratorFunc(func(callCtx context.Context, request contextmgr.SummaryGenerationRequest) (contextmgr.SummaryGenerationResponse, error) {
		if attempted {
			return contextmgr.SummaryGenerationResponse{}, errors.New("prior compaction attempt is already terminal; using source-preserving rule fallback")
		}
		called = true
		var generationErr error
		response, generationErr = generator.Generate(callCtx, request)
		returnedCandidate = generationErr == nil
		return response, generationErr
	})
	config := contextmgr.DefaultConfig()
	config.PreserveRecentMessages = preserveRecent
	config.MaxMessagesBeforeCompact = max(config.MaxMessagesBeforeCompact, preserveRecent)
	result, err := contextmgr.NewManager(nil, config).WithSummaryGenerator(wrapped, sourceSHA).PrepareCandidate(ctx, snapshot.SessionID, snapshot.WorkspaceID, history, snapshot.Summary, snapshot.HasSummary)
	if err != nil {
		return abort(err)
	}
	if err := ctx.Err(); err != nil {
		return abort(err)
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return abort(err)
	}
	defer tx.Rollback()
	current, err := readSupervisorCompactionSnapshot(ctx, tx, checkpoint)
	if err != nil {
		return abort(err)
	}
	receipt, exists, err := readSupervisorCompactionReceipt(ctx, tx, checkpoint.RunID, sourceSHA)
	if err != nil {
		return abort(err)
	}
	if exists && receipt.Sequence == 0 {
		return abort(errors.New("context compaction model call has no terminal receipt"))
	}
	if attempted && (!exists || receipt.Sequence != previousReceipt.Sequence || receipt.Subject != previousReceipt.Subject) {
		return abort(errors.New("prior compaction receipt changed"))
	}
	expected := snapshot
	if current.Checkpoint != snapshot.Checkpoint {
		// No counter tolerance: the Go terminal transaction hashes the complete
		// checkpoint immediately before/after its own usage update.
		if !called || attempted || !exists || receipt.BeforeSHA != supervisorCompactionHash(snapshot.Checkpoint) || receipt.AfterSHA != supervisorCompactionHash(current.Checkpoint) {
			return abort(errors.New("context compaction checkpoint mutation is not explained by its exact receipt"))
		}
		expected.Checkpoint = current.Checkpoint
	}
	if !expected.sameSources(current) {
		return abort(apperror.New(apperror.CodeConflict, "Supervisor generated compaction sources changed"))
	}
	if result.Generated {
		if !called || !returnedCandidate || !exists {
			return abort(errors.New("generated summary has no source-bound model receipt"))
		}
		if err := verifySupervisorGeneratedResponse(receipt, checkpoint.RunID, checkpoint.AttemptID, sourceSHA, response); err != nil {
			return abort(err)
		}
	}
	if err := requireSupervisorCheckpointLeaseTx(ctx, tx, checkpoint, current.Checkpoint); err != nil {
		return abort(err)
	}
	if result.Summary.ID == 0 {
		result.Summary, err = saveContextSummaryTx(ctx, tx, result.Summary)
		if err != nil {
			return abort(err)
		}
	}
	if result.Compacted && result.RemovedMessages > 0 {
		throughID := history[result.RemovedMessages-1].SourceMessageID
		updated, err := tx.ExecContext(ctx, `UPDATE session_messages SET compacted=1 WHERE session_id=? AND compacted=0 AND id<=?`, snapshot.SessionID, throughID)
		if err != nil {
			return abort(err)
		}
		count, err := updated.RowsAffected()
		if err != nil {
			return abort(err)
		}
		if count != int64(result.RemovedMessages) {
			return abort(apperror.New(apperror.CodeConflict, "Supervisor compaction history changed"))
		}
	}
	run, _, err := requireActiveSupervisorAttemptTx(ctx, tx, current.Checkpoint)
	if err != nil {
		return abort(err)
	}
	if err := appendSupervisorEventTx(ctx, tx, run, "session.context_compacted", "context_manager", checkpoint.AttemptID, map[string]any{
		"turn": checkpoint.NextTurn, "attempt_id": checkpoint.AttemptID, "summary_id": result.Summary.ID, "source_sha256": sourceSHA,
		"generated": result.Generated, "generation_fallback_reason": result.GenerationFallbackReason, "removed_messages": result.RemovedMessages,
	}); err != nil {
		return abort(err)
	}
	if err := tx.Commit(); err != nil {
		return abort(err)
	}
	return result, current.Checkpoint, nil
}
