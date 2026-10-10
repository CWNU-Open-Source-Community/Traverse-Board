package store

import (
	"context"
	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/hooks"
	"cyberagent-workbench/internal/plugins"
	"database/sql"
)

// Reads receipts only: no hook evaluation, payloads, annotations or scripts.
func (s *SQLiteStore) ListHookAudits(ctx context.Context, runID, workspaceID string, limit int) ([]hooks.AuditRecord, error) {
	if limit < 1 || limit > 200 {
		return nil, apperror.New(apperror.CodeInvalidArgument, "hook audit limit is invalid")
	}
	query := `SELECT id, plugin_id, hook_id, event, run_id, workspace_id, tool_name, outcome, created_at, plugin_fingerprint, declared_action, rejected FROM plugin_hook_audits`
	args := []any{}
	if runID != "" {
		query += ` WHERE run_id = ? AND workspace_id = ?`
		args = append(args, runID, workspaceID)
	} else if workspaceID != "" {
		query += ` WHERE workspace_id = ?`
		args = append(args, workspaceID)
	}
	query += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]hooks.AuditRecord, 0)
	for rows.Next() {
		var value hooks.AuditRecord
		var createdAt string
		var rejected sql.NullBool
		if err := rows.Scan(&value.ID, &value.PluginID, &value.HookID, &value.Event, &value.RunID, &value.WorkspaceID, &value.ToolName, &value.Outcome, &createdAt, &value.PluginFingerprint, &value.Action, &rejected); err != nil {
			return nil, err
		}
		value.CreatedAt = parseTS(createdAt)
		if rejected.Valid {
			value.Rejected = &rejected.Bool
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *SQLiteStore) ListPluginVersions(ctx context.Context, packageID, protocol, surface string, limit int) ([]plugins.Installation, int, error) {
	if limit < 1 || limit > 1000 {
		return nil, 0, apperror.New(apperror.CodeInvalidArgument, "plugin version limit is invalid")
	}
	const predicate = ` FROM plugin_installations WHERE plugin_id=? AND protocol_version=? AND COALESCE(json_extract(source_json,'$.surface'),'')=?`
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*)`+predicate, packageID, protocol, surface).Scan(&count); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+pluginInstallationColumns+predicate+` ORDER BY created_at DESC,id DESC LIMIT ?`, packageID, protocol, surface, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	values := []plugins.Installation{}
	for rows.Next() {
		value, err := scanPluginInstallation(rows)
		if err != nil {
			return nil, 0, err
		}
		values = append(values, value)
	}
	return values, count, rows.Err()
}

func (s *SQLiteStore) ListPluginPublisherInstallations(ctx context.Context, fingerprint string, limit int) ([]plugins.Installation, int, error) {
	if limit < 1 || limit > 1000 {
		return nil, 0, apperror.New(apperror.CodeInvalidArgument, "publisher installation limit is invalid")
	}
	const predicate = ` FROM plugin_installations WHERE publisher_fingerprint=? AND state NOT IN ('revoked','rolled_back')`
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*)`+predicate, fingerprint).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+pluginInstallationColumns+predicate+` ORDER BY created_at DESC,id DESC LIMIT ?`, fingerprint, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	values := []plugins.Installation{}
	for rows.Next() {
		value, err := scanPluginInstallation(rows)
		if err != nil {
			return nil, 0, err
		}
		values = append(values, value)
	}
	return values, total, rows.Err()
}
