package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/fileedit"
)

func (s *SQLiteStore) GetFileEditReviewSnapshot(ctx context.Context, runID, editID string) (fileedit.Edit, bool, error) {
	var payload string
	err := s.db.QueryRowContext(ctx, `SELECT json_extract(payload_json,'$.review_snapshot') FROM run_events
		WHERE run_id=? AND subject_id=? AND source='fileedit_store' AND type IN (?,?)
		AND json_type(payload_json,'$.review_snapshot')='object' ORDER BY sequence LIMIT 1`, runID, editID, events.FileEditApprovedEvent, events.FileEditDeniedEvent).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return fileedit.Edit{}, false, nil
	}
	if err != nil {
		return fileedit.Edit{}, false, err
	}
	var snapshot fileedit.Edit
	if err := json.Unmarshal([]byte(payload), &snapshot); err != nil {
		return snapshot, false, err
	}
	current, err := s.GetFileEdit(ctx, editID)
	if err != nil {
		return snapshot, false, err
	}
	if snapshot.ID != current.ID || snapshot.SessionID != current.SessionID || snapshot.WorkspaceID != current.WorkspaceID || snapshot.Path != current.Path || snapshot.Operation != current.Operation || snapshot.DestinationPath != current.DestinationPath || snapshot.OriginalHash != current.OriginalHash || snapshot.ProposedHash != current.ProposedHash || snapshot.DestinationOriginalHash != current.DestinationOriginalHash || snapshot.DestinationProposedHash != current.DestinationProposedHash || snapshot.SecretsRedacted != current.SecretsRedacted || !snapshot.CreatedAt.Equal(current.CreatedAt) || (snapshot.Status != fileedit.StatusApproved && snapshot.Status != fileedit.StatusDenied) {
		return snapshot, false, apperror.New(apperror.CodeConflict, "Stored file review no longer matches its immutable proposal")
	}
	snapshot.OriginalText, snapshot.ProposedText, snapshot.Diff = current.OriginalText, current.ProposedText, current.Diff
	return snapshot, true, nil
}
