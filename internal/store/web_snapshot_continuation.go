package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/webevidence"
)

// GetThreadPredecessorWebSnapshot resolves only an exact saved source/snapshot
// in this Run's verified predecessor chain. It grants no network or execution
// authority and returns the original evidence identities without copying them.
func (s *SQLiteStore) GetThreadPredecessorWebSnapshot(ctx context.Context,
	runID, missionID, workspaceID, sourceID, snapshotID string,
) (webevidence.Source, webevidence.Snapshot, error) {
	notFound := func() (webevidence.Source, webevidence.Snapshot, error) {
		return webevidence.Source{}, webevidence.Snapshot{}, apperror.New(
			apperror.CodeNotFound, "saved snapshot was not found in this Run's verified Thread history")
	}
	for _, identity := range []string{runID, missionID, workspaceID, sourceID, snapshotID} {
		if strings.TrimSpace(identity) == "" || len(identity) > 256 {
			return notFound()
		}
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return webevidence.Source{}, webevidence.Snapshot{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var threadID, sourceRunID string
	err = tx.QueryRowContext(ctx, `SELECT binding.thread_id FROM thread_runs binding
		JOIN threads thread ON thread.id = binding.thread_id
		JOIN runs run ON run.id = binding.run_id AND run.session_id = binding.session_id
		JOIN missions mission ON mission.id = run.mission_id
		WHERE binding.run_id = ? AND thread.status = 'active'
			AND thread.mission_id = ? AND run.mission_id = ?
			AND thread.workspace_id = ? AND mission.workspace_id = ?`,
		runID, missionID, missionID, workspaceID, workspaceID).Scan(&threadID)
	if errors.Is(err, sql.ErrNoRows) {
		return notFound()
	}
	if err != nil {
		return webevidence.Source{}, webevidence.Snapshot{}, err
	}
	err = tx.QueryRowContext(ctx, `SELECT snapshot.run_id FROM web_evidence_snapshots snapshot
		JOIN web_evidence_sources source ON source.id = snapshot.source_id AND source.run_id = snapshot.run_id
		WHERE snapshot.id = ? AND source.id = ?`, snapshotID, sourceID).Scan(&sourceRunID)
	if errors.Is(err, sql.ErrNoRows) || sourceRunID == runID {
		return notFound()
	}
	if err != nil {
		return webevidence.Source{}, webevidence.Snapshot{}, err
	}
	currentID, previousOrdinal := runID, int64(0)
	// Bound malformed or exceptionally long ancestry rather than accepting a
	// cycle or treating a same-Thread label alone as evidence of continuation.
	for depth := 0; depth < 256; depth++ {
		var predecessor, storedSession, runSession, storedMission, storedWorkspace string
		var status domain.RunStatus
		var ordinal int64
		err = tx.QueryRowContext(ctx, `SELECT COALESCE(binding.predecessor_run_id, ''),
			binding.ordinal, binding.session_id, run.session_id, run.mission_id,
			mission.workspace_id, run.status FROM thread_runs binding
			JOIN runs run ON run.id = binding.run_id
			JOIN missions mission ON mission.id = run.mission_id
			WHERE binding.thread_id = ? AND binding.run_id = ?`, threadID, currentID).
			Scan(&predecessor, &ordinal, &storedSession, &runSession, &storedMission, &storedWorkspace, &status)
		if errors.Is(err, sql.ErrNoRows) {
			return notFound()
		}
		if err != nil {
			return webevidence.Source{}, webevidence.Snapshot{}, err
		}
		if storedSession != runSession || storedMission != missionID || storedWorkspace != workspaceID ||
			(previousOrdinal != 0 && ordinal != previousOrdinal-1) ||
			(depth > 0 && status != domain.RunCompleted && status != domain.RunFailed && status != domain.RunCancelled) {
			return notFound()
		}
		if currentID == sourceRunID {
			source, sourceFound, sourceErr := getWebSource(ctx, tx, sourceRunID, sourceID)
			if sourceErr != nil {
				return webevidence.Source{}, webevidence.Snapshot{}, sourceErr
			}
			snapshot, snapshotFound, snapshotErr := getWebSnapshot(ctx, tx, sourceRunID, snapshotID)
			if snapshotErr != nil {
				return webevidence.Source{}, webevidence.Snapshot{}, snapshotErr
			}
			if !sourceFound || !snapshotFound || source.ID != sourceID || snapshot.ID != snapshotID ||
				source.RunID != sourceRunID || snapshot.RunID != sourceRunID ||
				source.MissionID != missionID || snapshot.MissionID != missionID ||
				source.WorkspaceID != workspaceID || snapshot.SourceID != source.ID {
				return notFound()
			}
			return source, snapshot, nil
		}
		if predecessor == "" || predecessor == currentID || ordinal <= 1 {
			return notFound()
		}
		var receipts, matchingReceipts int
		err = tx.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(CASE
			WHEN json_extract(payload_json, '$.predecessor_run_id') = ?
				AND json_extract(payload_json, '$.successor_run_id') = ? THEN 1 ELSE 0 END), 0)
			FROM thread_events WHERE thread_id = ? AND run_id = ?
				AND type = 'thread.run_successor_created' AND source = 'thread_continuation'`,
			predecessor, currentID, threadID, currentID).Scan(&receipts, &matchingReceipts)
		if err != nil {
			return webevidence.Source{}, webevidence.Snapshot{}, err
		}
		if receipts != 1 || matchingReceipts != 1 {
			return notFound()
		}
		currentID, previousOrdinal = predecessor, ordinal
	}
	return notFound()
}
