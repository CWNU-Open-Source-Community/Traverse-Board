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
)

const contextMemorySelect = `SELECT id, protocol_version, scope, scope_id, title,
	content, content_sha256, status, source_kind, source_ref, references_json,
	retention_until, redacted, created_by, updated_by, version, created_at, updated_at
	FROM context_memories `

func (s *SQLiteStore) CreateContextMemory(ctx context.Context,
	memory contextmgr.Memory,
) error {
	if err := memory.ValidateAt(time.Now().UTC()); err != nil {
		return err
	}
	references, err := json.Marshal(memory.References)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO context_memories
		(id, protocol_version, scope, scope_id, title, content, content_sha256, status,
		source_kind, source_ref, references_json, retention_until, redacted,
		created_by, updated_by, version, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		memory.ID, memory.ProtocolVersion, memory.Scope, memory.ScopeID, memory.Title,
		memory.Content, memory.ContentSHA256, memory.Status, memory.SourceKind,
		memory.SourceRef, string(references), nullableTS(memory.RetentionUntil),
		boolInt(memory.Redacted), memory.CreatedBy, memory.UpdatedBy, memory.Version,
		ts(memory.CreatedAt), ts(memory.UpdatedAt))
	return err
}

func (s *SQLiteStore) GetContextMemory(ctx context.Context,
	id string,
) (contextmgr.Memory, error) {
	return scanContextMemory(s.db.QueryRowContext(ctx,
		contextMemorySelect+`WHERE id = ?`, strings.TrimSpace(id)))
}

func (s *SQLiteStore) ListContextMemories(ctx context.Context,
	filter contextmgr.MemoryFilter, at time.Time,
) ([]contextmgr.Memory, error) {
	if err := filter.Validate(); err != nil {
		return nil, err
	}
	if at.IsZero() {
		at = time.Now().UTC()
	} else {
		at = at.UTC()
	}
	query := contextMemorySelect + `WHERE scope = ? AND scope_id = ?`
	args := []any{filter.Scope, filter.ScopeID}
	if !filter.IncludeDisabled {
		query += ` AND status = 'active'`
	}
	if !filter.IncludeExpired {
		query += ` AND (retention_until IS NULL OR julianday(retention_until) > julianday(?))`
		args = append(args, ts(at))
	}
	query += ` ORDER BY updated_at DESC, id DESC LIMIT ?`
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]contextmgr.Memory, 0, limit)
	for rows.Next() {
		memory, err := scanContextMemory(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, memory)
	}
	return values, rows.Err()
}

func (s *SQLiteStore) UpdateContextMemory(ctx context.Context,
	memory contextmgr.Memory, expectedVersion int64,
) error {
	if expectedVersion <= 0 || memory.Version != expectedVersion+1 {
		return errors.New("long-term memory update version is invalid")
	}
	if err := memory.ValidateAt(time.Now().UTC()); err != nil {
		return err
	}
	references, err := json.Marshal(memory.References)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE context_memories SET
		title = ?, content = ?, content_sha256 = ?, status = ?, source_ref = ?,
		references_json = ?, retention_until = ?, redacted = ?, updated_by = ?,
		version = ?, updated_at = ? WHERE id = ? AND version = ?`,
		memory.Title, memory.Content, memory.ContentSHA256, memory.Status,
		memory.SourceRef, string(references), nullableTS(memory.RetentionUntil),
		boolInt(memory.Redacted), memory.UpdatedBy, memory.Version, ts(memory.UpdatedAt),
		memory.ID, expectedVersion)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return errors.New("long-term memory changed concurrently or was not found")
	}
	return nil
}

func (s *SQLiteStore) DeleteContextMemory(ctx context.Context,
	id string, expectedVersion int64,
) (bool, error) {
	id = strings.TrimSpace(id)
	if id == "" || expectedVersion <= 0 {
		return false, errors.New("long-term memory delete identity and version are required")
	}
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM context_memories WHERE id = ? AND version = ?`, id, expectedVersion)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows == 1, nil
}

func scanContextMemory(row scanner) (contextmgr.Memory, error) {
	var memory contextmgr.Memory
	var scope, status, referencesJSON string
	var retention sql.NullString
	var redacted int
	var created, updated string
	if err := row.Scan(&memory.ID, &memory.ProtocolVersion, &scope, &memory.ScopeID,
		&memory.Title, &memory.Content, &memory.ContentSHA256, &status,
		&memory.SourceKind, &memory.SourceRef, &referencesJSON, &retention, &redacted,
		&memory.CreatedBy, &memory.UpdatedBy, &memory.Version, &created, &updated); err != nil {
		return contextmgr.Memory{}, err
	}
	memory.Scope = contextmgr.MemoryScope(scope)
	memory.Status = contextmgr.MemoryStatus(status)
	memory.RetentionUntil = parseNullableTS(retention)
	memory.Redacted = redacted != 0
	memory.CreatedAt = parseTS(created)
	memory.UpdatedAt = parseTS(updated)
	if err := json.Unmarshal([]byte(referencesJSON), &memory.References); err != nil {
		return contextmgr.Memory{}, fmt.Errorf("decode long-term memory references: %w", err)
	}
	if memory.References == nil {
		memory.References = []string{}
	}
	if err := memory.ValidateAt(time.Now().UTC()); err != nil {
		return contextmgr.Memory{}, err
	}
	return memory, nil
}
