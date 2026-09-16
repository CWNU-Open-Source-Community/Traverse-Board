package store

import (
	"context"
	"errors"

	"cyberagent-workbench/internal/session"
)

// CreateWorkspaceIfAbsent registers a new identity without changing any existing
// workspace. Conflicts are retryable by the import manager; the historical
// SaveWorkspace name-based root upsert is deliberately not used for imports.
func (s *SQLiteStore) CreateWorkspaceIfAbsent(ctx context.Context,
	record session.WorkspaceRecord) (bool, error) {
	if record.ID == "" || record.Name == "" || record.RootPath == "" || record.CreatedAt.IsZero() {
		return false, errors.New("complete workspace registration is required")
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO workspaces (id, name, root_path, created_at)
		VALUES (?, ?, ?, ?) ON CONFLICT DO NOTHING`,
		record.ID, record.Name, record.RootPath, ts(record.CreatedAt))
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}
