package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/gitmutation"
	"cyberagent-workbench/internal/runmutation"
)

const threadGitPreparedEvent = "git.mutation_prepared"

type threadGitPreparation struct {
	RequestFingerprint string          `json:"request_fingerprint"`
	Prepared           json.RawMessage `json:"prepared"`
}

func threadGitPreparationEventID(id string) string {
	return "evt-git-prepared-" + runmutation.Fingerprint("thread-git-preparation", id)
}

// RecordThreadGitPreparation saves metadata for read-only recovery after the
// durable native intent was approved and claimed, but before publication. It
// uses the existing immutable Run event ledger and never completes the intent.
func (s *SQLiteStore) RecordThreadGitPreparation(ctx context.Context, id, fingerprint, raw string, lease domain.RunExecutionLease) error {
	if len(raw) == 0 || len(raw) > 8192 || !json.Valid([]byte(raw)) {
		return errors.New("Git preparation metadata is invalid")
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil || metadata == nil {
		return errors.New("Git preparation must be an object")
	}
	prepared, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	row, found, err := getGitMutationRecord(ctx, tx, id)
	if err != nil {
		return err
	}
	if !found || row.RequestFingerprint != fingerprint || row.Operation != gitmutation.Commit || row.StartedAt == nil || row.CompletedAt != nil || row.RunID != lease.RunID {
		return errors.New("Git preparation requires the exact claimed commit intent")
	}
	current, found, err := getRunExecutionLeaseTx(ctx, tx, row.RunID)
	if err != nil {
		return err
	}
	if !found || !sameRunExecutionLease(current, lease) || !current.ActiveAt(time.Now().UTC()) {
		return errors.New("Git preparation lease changed")
	}
	proof, err := getApprovalTx(ctx, tx, "", id)
	if err != nil {
		return err
	}
	var intent struct {
		Version   string `json:"version"`
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal([]byte(row.SpecJSON), &intent) != nil || intent.Version != "thread_git.v1" ||
		proof.RunID != row.RunID || proof.SessionID != intent.SessionID || proof.WorkspaceID != row.WorkspaceID ||
		proof.Status != approval.StatusApproved || proof.ToolName != "thread.git" || proof.ActionClass != "git_write" ||
		proof.Mode != "per_call" || proof.GrantID != "" || proof.RequestFingerprint != fingerprint {
		return errors.New("Git preparation approval changed")
	}
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT payload_json FROM run_events WHERE event_id=?`, threadGitPreparationEventID(id)).Scan(&existing)
	if err == nil {
		var stored threadGitPreparation
		if json.Unmarshal([]byte(existing), &stored) != nil || stored.RequestFingerprint != fingerprint || string(stored.Prepared) != string(prepared) {
			return errors.New("Git preparation receipt is immutable")
		}
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var missionID string
	if err := tx.QueryRowContext(ctx, `SELECT mission_id FROM runs WHERE id=?`, row.RunID).Scan(&missionID); err != nil {
		return err
	}
	event, err := events.New(row.RunID, missionID, threadGitPreparedEvent, "thread_git", id,
		threadGitPreparation{RequestFingerprint: fingerprint, Prepared: prepared})
	if err != nil {
		return err
	}
	event.EventID = threadGitPreparationEventID(id)
	saved, err := insertRunEventTx(ctx, tx, event)
	if err != nil {
		return err
	}
	var retained threadGitPreparation
	if json.Unmarshal([]byte(saved.PayloadJSON), &retained) != nil || retained.RequestFingerprint != fingerprint || string(retained.Prepared) != string(prepared) {
		return errors.New("Git preparation metadata could not be retained exactly")
	}
	return tx.Commit()
}

func (s *SQLiteStore) GetThreadGitPreparation(ctx context.Context, id, fingerprint string) (string, bool, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT e.payload_json FROM run_events e
		JOIN git_mutation_operations g ON g.id=e.subject_id AND g.run_id=e.run_id
		WHERE e.event_id=? AND e.type=? AND e.source='thread_git' AND g.id=? AND g.request_fingerprint=?`,
		threadGitPreparationEventID(id), threadGitPreparedEvent, id, fingerprint).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	var stored threadGitPreparation
	if json.Unmarshal([]byte(raw), &stored) != nil || stored.RequestFingerprint != fingerprint || len(stored.Prepared) == 0 {
		return "", false, errors.New("stored Git preparation differs from its original intent")
	}
	return string(stored.Prepared), true, nil
}
