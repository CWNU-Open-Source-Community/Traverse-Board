package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"cyberagent-workbench/internal/contextmgr"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/projectconfig"
)

const runInstructionSnapshotSelect = `SELECT id, run_id, revision, fingerprint,
	 snapshot_json, diff_json, confirmed_by, created_at
	 FROM run_instruction_snapshots `

func insertInitialRunInstructionSnapshotTx(ctx context.Context, tx *sql.Tx,
	run domain.Run, confirmedBy string,
) error {
	if len(run.Config.ProjectInstructions) == 0 {
		return nil
	}
	var snapshot projectconfig.InstructionSnapshot
	if err := json.Unmarshal(run.Config.ProjectInstructions, &snapshot); err != nil {
		return err
	}
	if err := snapshot.Validate(); err != nil ||
		snapshot.Fingerprint != run.Config.ProjectInstructionsFingerprint {
		return errors.New("initial Run instruction snapshot binding is invalid")
	}
	added := make([]string, len(snapshot.Sources))
	for index, source := range snapshot.Sources {
		added[index] = source.Path
	}
	record := projectconfig.RunInstructionSnapshot{
		ID: idgen.New("run-instructions"), RunID: run.ID, Revision: 1,
		Snapshot: snapshot,
		Diff: projectconfig.InstructionSnapshotDiff{ToFingerprint: snapshot.Fingerprint,
			Added: added, Removed: []string{}, Changed: []string{}, RequiresConfirmation: false},
		ConfirmedBy: strings.TrimSpace(confirmedBy), CreatedAt: run.CreatedAt,
	}
	return insertRunInstructionSnapshotTx(ctx, tx, record)
}

func (s *SQLiteStore) GetLatestRunInstructionSnapshot(ctx context.Context,
	runID string,
) (projectconfig.RunInstructionSnapshot, bool, error) {
	record, err := scanRunInstructionSnapshot(s.db.QueryRowContext(ctx,
		runInstructionSnapshotSelect+`WHERE run_id = ? ORDER BY revision DESC LIMIT 1`,
		strings.TrimSpace(runID)))
	if errors.Is(err, sql.ErrNoRows) {
		return projectconfig.RunInstructionSnapshot{}, false, nil
	}
	if err != nil {
		return projectconfig.RunInstructionSnapshot{}, false, err
	}
	return record, true, nil
}

func (s *SQLiteStore) ListRunInstructionSnapshots(ctx context.Context,
	runID string, limit int,
) ([]projectconfig.RunInstructionSnapshot, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, runInstructionSnapshotSelect+
		`WHERE run_id = ? ORDER BY revision DESC LIMIT ?`, strings.TrimSpace(runID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := make([]projectconfig.RunInstructionSnapshot, 0, limit)
	for rows.Next() {
		record, err := scanRunInstructionSnapshot(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *SQLiteStore) ConfirmRunInstructionSnapshot(ctx context.Context,
	runID, expectedFingerprint string, snapshot projectconfig.InstructionSnapshot,
	diff projectconfig.InstructionSnapshotDiff, confirmedBy string, at time.Time,
) (projectconfig.RunInstructionSnapshot, bool, error) {
	if err := snapshot.Validate(); err != nil {
		return projectconfig.RunInstructionSnapshot{}, false, err
	}
	if diff.FromFingerprint != expectedFingerprint || diff.ToFingerprint != snapshot.Fingerprint ||
		!diff.RequiresConfirmation || expectedFingerprint == snapshot.Fingerprint {
		return projectconfig.RunInstructionSnapshot{}, false,
			errors.New("project instruction refresh diff is invalid")
	}
	if err := contextmgr.ValidateMemoryActor(confirmedBy); err != nil {
		return projectconfig.RunInstructionSnapshot{}, false, err
	}
	if at.IsZero() {
		at = time.Now().UTC()
	} else {
		at = at.UTC()
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return projectconfig.RunInstructionSnapshot{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	run, err := scanRun(tx.QueryRowContext(ctx, `SELECT id, mission_id, session_id, status,
		config_json, budget_json, started_at, finished_at, created_at, updated_at
		FROM runs WHERE id = ?`, strings.TrimSpace(runID)))
	if err != nil {
		return projectconfig.RunInstructionSnapshot{}, false, err
	}
	if run.Terminal() {
		return projectconfig.RunInstructionSnapshot{}, false,
			errors.New("terminal Run project instructions cannot be refreshed")
	}
	if run.Config.ProjectInstructionsFingerprint != expectedFingerprint {
		return projectconfig.RunInstructionSnapshot{}, false,
			errors.New("project instruction snapshot changed concurrently")
	}
	originalConfigJSON, err := marshalRedactedJSON(run.Config)
	if err != nil {
		return projectconfig.RunInstructionSnapshot{}, false, err
	}
	var latestRevision int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(revision), 0)
		FROM run_instruction_snapshots WHERE run_id = ?`, run.ID).Scan(&latestRevision); err != nil {
		return projectconfig.RunInstructionSnapshot{}, false, err
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return projectconfig.RunInstructionSnapshot{}, false, err
	}
	run.Config.ProjectInstructions = raw
	run.Config.ProjectInstructionsFingerprint = snapshot.Fingerprint
	run.UpdatedAt = at
	if err := run.Validate(); err != nil {
		return projectconfig.RunInstructionSnapshot{}, false, err
	}
	configJSON, err := marshalRedactedJSON(run.Config)
	if err != nil {
		return projectconfig.RunInstructionSnapshot{}, false, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE runs SET config_json = ?, updated_at = ?
		WHERE id = ? AND config_json = ?`, configJSON, ts(at), run.ID, originalConfigJSON)
	if err != nil {
		return projectconfig.RunInstructionSnapshot{}, false, err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		if err != nil {
			return projectconfig.RunInstructionSnapshot{}, false, err
		}
		return projectconfig.RunInstructionSnapshot{}, false,
			errors.New("project instruction snapshot changed concurrently")
	}
	record := projectconfig.RunInstructionSnapshot{
		ID: idgen.New("run-instructions"), RunID: run.ID, Revision: latestRevision + 1,
		Snapshot: snapshot, Diff: diff, ConfirmedBy: strings.TrimSpace(confirmedBy), CreatedAt: at,
	}
	if err := insertRunInstructionSnapshotTx(ctx, tx, record); err != nil {
		return projectconfig.RunInstructionSnapshot{}, false, err
	}
	event, err := events.New(run.ID, run.MissionID,
		events.RunProjectInstructionsRefreshedEvent, "project_instruction_service", record.ID,
		map[string]any{"revision": record.Revision, "from_fingerprint": expectedFingerprint,
			"to_fingerprint": snapshot.Fingerprint, "target_path": snapshot.TargetPath,
			"confirmed_by": record.ConfirmedBy})
	if err != nil {
		return projectconfig.RunInstructionSnapshot{}, false, err
	}
	if _, err := insertRunEventTx(ctx, tx, event); err != nil {
		return projectconfig.RunInstructionSnapshot{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return projectconfig.RunInstructionSnapshot{}, false, err
	}
	return record, true, nil
}

func insertRunInstructionSnapshotTx(ctx context.Context, tx *sql.Tx,
	record projectconfig.RunInstructionSnapshot,
) error {
	if err := record.Validate(); err != nil {
		return err
	}
	snapshotJSON, err := json.Marshal(record.Snapshot)
	if err != nil {
		return err
	}
	diffJSON, err := json.Marshal(record.Diff)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO run_instruction_snapshots
		(id, run_id, revision, target_path, fingerprint, snapshot_json, diff_json,
		confirmed_by, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID, record.RunID, record.Revision, record.Snapshot.TargetPath,
		record.Snapshot.Fingerprint, string(snapshotJSON), string(diffJSON),
		record.ConfirmedBy, ts(record.CreatedAt))
	return err
}

func scanRunInstructionSnapshot(row scanner) (projectconfig.RunInstructionSnapshot, error) {
	var record projectconfig.RunInstructionSnapshot
	var fingerprint, snapshotJSON, diffJSON, created string
	if err := row.Scan(&record.ID, &record.RunID, &record.Revision, &fingerprint,
		&snapshotJSON, &diffJSON, &record.ConfirmedBy, &created); err != nil {
		return projectconfig.RunInstructionSnapshot{}, err
	}
	if err := json.Unmarshal([]byte(snapshotJSON), &record.Snapshot); err != nil {
		return projectconfig.RunInstructionSnapshot{}, fmt.Errorf("decode Run instruction snapshot: %w", err)
	}
	if err := json.Unmarshal([]byte(diffJSON), &record.Diff); err != nil {
		return projectconfig.RunInstructionSnapshot{}, fmt.Errorf("decode Run instruction diff: %w", err)
	}
	if record.Snapshot.Fingerprint != fingerprint {
		return projectconfig.RunInstructionSnapshot{}, errors.New("Run instruction row fingerprint mismatch")
	}
	record.CreatedAt = parseTS(created)
	if err := record.Validate(); err != nil {
		return projectconfig.RunInstructionSnapshot{}, err
	}
	return record, nil
}
