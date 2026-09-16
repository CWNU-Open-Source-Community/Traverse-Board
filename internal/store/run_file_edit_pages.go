package store

import (
	"context"
	"strings"

	"cyberagent-workbench/internal/fileedit"
)

// Filter by durable Run ownership before pagination. A continued Run has its
// own Session on the same physical Drydock, whose creator identity stays intact.
func (s *SQLiteStore) ListRunFileEditPreviewsPage(ctx context.Context,
	runID string, offset, limit int,
) ([]fileedit.Preview, error) {
	if err := validateStoreReadPage(offset, limit); err != nil {
		return nil, err
	}
	ownerTable := "drydock_workspaces"
	if present, err := hasRunFileDrydockBindings(ctx, s.db); err != nil {
		return nil, err
	} else if present {
		ownerTable = "run_file_drydock_bindings"
	}
	rows, err := s.db.QueryContext(ctx, `SELECT e.id,e.session_id,e.workspace_id,e.path,
		e.operation_kind,e.destination_path,e.status,e.diff_text,e.original_hash,e.proposed_hash,
		e.destination_original_hash,e.destination_proposed_hash,e.reason,e.secrets_redacted,
		e.created_at,e.updated_at FROM file_edits e
		JOIN runs r ON r.session_id=e.session_id JOIN missions m ON m.id=r.mission_id
		JOIN sessions se ON se.id=r.session_id
		WHERE r.id=? AND se.workspace_id=m.workspace_id AND
		(e.workspace_id=m.workspace_id OR EXISTS (SELECT 1 FROM `+ownerTable+` d
			WHERE d.run_id=r.id AND d.mission_id=m.id AND d.session_id=se.id
			AND d.source_workspace_id=m.workspace_id AND d.workspace_id=e.workspace_id))
		ORDER BY e.updated_at DESC,e.id DESC LIMIT ? OFFSET ?`, strings.TrimSpace(runID), limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]fileedit.Preview, 0, limit)
	for rows.Next() {
		value, err := scanFileEditPreview(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}
