package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/webevidence"
)

func (s *SQLiteStore) GetWebEvidenceOperation(ctx context.Context, runID,
	keyDigest string,
) (webevidence.Operation, bool, error) {
	return getWebEvidenceOperation(ctx, s.db, strings.TrimSpace(runID),
		strings.TrimSpace(keyDigest))
}

func getWebEvidenceOperation(ctx context.Context, queryer skillPackageQueryer,
	runID, keyDigest string,
) (webevidence.Operation, bool, error) {
	var operation webevidence.Operation
	var response, createdAt string
	err := queryer.QueryRowContext(ctx, `SELECT protocol_version, key_digest,
		request_fingerprint, run_id, tool_name, response_json, created_at
		FROM web_evidence_operations WHERE key_digest = ? AND run_id = ?`,
		keyDigest, runID).Scan(&operation.ProtocolVersion, &operation.KeyDigest,
		&operation.RequestFingerprint, &operation.RunID, &operation.ToolName,
		&response, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return webevidence.Operation{}, false, nil
	}
	if err != nil {
		return webevidence.Operation{}, false, err
	}
	operation.Response = json.RawMessage(response)
	operation.CreatedAt = parseTS(createdAt)
	if err := operation.Validate(); err != nil {
		return webevidence.Operation{}, false, err
	}
	return operation, true, nil
}

func (s *SQLiteStore) SaveWebSearch(ctx context.Context, sources []webevidence.Source,
	operation webevidence.Operation,
) (webevidence.Operation, bool, error) {
	if operation.ToolName != "web_search" || operation.Validate() != nil ||
		len(sources) > webevidence.MaxSources {
		return webevidence.Operation{}, false, apperror.New(apperror.CodeInvalidArgument,
			"web search persistence input is invalid")
	}
	for _, source := range sources {
		if source.Validate() != nil || source.RunID != operation.RunID ||
			source.State != webevidence.SourceDiscovered {
			return webevidence.Operation{}, false, apperror.New(apperror.CodeInvalidArgument,
				"web search source is invalid")
		}
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return webevidence.Operation{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if existing, found, err := getWebEvidenceOperation(ctx, tx, operation.RunID,
		operation.KeyDigest); err != nil || found {
		if err == nil && existing.RequestFingerprint != operation.RequestFingerprint {
			err = apperror.New(apperror.CodeConflict,
				"web evidence operation key was reused with different input")
		}
		return existing, found, err
	}
	for _, source := range sources {
		if err := ensureWebSource(ctx, tx, source); err != nil {
			return webevidence.Operation{}, false, err
		}
	}
	if err := insertWebOperation(ctx, tx, operation); err != nil {
		return webevidence.Operation{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return webevidence.Operation{}, false, err
	}
	return operation, false, nil
}

func (s *SQLiteStore) SaveWebFetch(ctx context.Context, source webevidence.Source,
	snapshot webevidence.Snapshot, operation webevidence.Operation,
) (webevidence.Operation, bool, error) {
	if source.Validate() != nil || snapshot.Validate() != nil || operation.Validate() != nil ||
		operation.ToolName != "web_fetch" || source.RunID != operation.RunID ||
		snapshot.RunID != operation.RunID || snapshot.SourceID != source.ID ||
		snapshot.MissionID != source.MissionID ||
		snapshot.RequestedURL != source.CanonicalURL || snapshot.Provider != source.Provider {
		return webevidence.Operation{}, false, apperror.New(apperror.CodeInvalidArgument,
			"web fetch persistence input is invalid")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return webevidence.Operation{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if existing, found, err := getWebEvidenceOperation(ctx, tx, operation.RunID,
		operation.KeyDigest); err != nil || found {
		if err == nil && existing.RequestFingerprint != operation.RequestFingerprint {
			err = apperror.New(apperror.CodeConflict,
				"web evidence operation key was reused with different input")
		}
		return existing, found, err
	}
	if err := ensureWebSource(ctx, tx, source); err != nil {
		return webevidence.Operation{}, false, err
	}
	if err := ensureWebSnapshot(ctx, tx, snapshot); err != nil {
		return webevidence.Operation{}, false, err
	}
	if err := insertWebOperation(ctx, tx, operation); err != nil {
		return webevidence.Operation{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return webevidence.Operation{}, false, err
	}
	return operation, false, nil
}

func (s *SQLiteStore) SaveWebCitation(ctx context.Context, citation webevidence.Citation,
	operation webevidence.Operation,
) (webevidence.Operation, bool, error) {
	if citation.Validate() != nil || operation.Validate() != nil ||
		operation.ToolName != "web_citation" || citation.RunID != operation.RunID {
		return webevidence.Operation{}, false, apperror.New(apperror.CodeInvalidArgument,
			"web citation persistence input is invalid")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return webevidence.Operation{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if existing, found, err := getWebEvidenceOperation(ctx, tx, operation.RunID,
		operation.KeyDigest); err != nil || found {
		if err == nil && existing.RequestFingerprint != operation.RequestFingerprint {
			err = apperror.New(apperror.CodeConflict,
				"web evidence operation key was reused with different input")
		}
		return existing, found, err
	}
	snapshot, found, err := getWebSnapshot(ctx, tx, citation.RunID, citation.SnapshotID)
	if err != nil {
		return webevidence.Operation{}, false, err
	}
	if !found || snapshot.SourceID != citation.SourceID ||
		(snapshot.State != webevidence.SourceFetched &&
			snapshot.State != webevidence.SourcePartial) ||
		citation.URL != snapshot.FinalURL || citation.Digest != snapshot.Digest ||
		!citation.FetchedAt.Equal(snapshot.FetchedAt) ||
		!citation.StaleAt.Equal(snapshot.StaleAt) ||
		citation.Partial != (snapshot.State == webevidence.SourcePartial) ||
		citation.SpanEnd > utf8.RuneCountInString(snapshot.Body) {
		return webevidence.Operation{}, false, apperror.New(apperror.CodeInvalidArgument,
			"web citation does not match its immutable fetched snapshot")
	}
	existing, found, err := getWebCitation(ctx, tx, citation.RunID, citation.ID)
	if err != nil {
		return webevidence.Operation{}, false, err
	}
	if found && existing.Fingerprint != citation.Fingerprint {
		return webevidence.Operation{}, false, apperror.New(apperror.CodeConflict,
			"web citation identity was reused")
	}
	if !found {
		raw, _ := json.Marshal(citation)
		_, err = tx.ExecContext(ctx, `INSERT INTO web_evidence_citations
			(id, protocol_version, run_id, source_id, snapshot_id, fingerprint,
			citation_json, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, citation.ID,
			citation.ProtocolVersion, citation.RunID, citation.SourceID, citation.SnapshotID,
			citation.Fingerprint, string(raw), ts(citation.CreatedAt))
		if err != nil {
			return webevidence.Operation{}, false, err
		}
	}
	if err := insertWebOperation(ctx, tx, operation); err != nil {
		return webevidence.Operation{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return webevidence.Operation{}, false, err
	}
	return operation, false, nil
}

func ensureWebSource(ctx context.Context, tx *sql.Tx, source webevidence.Source) error {
	existing, found, err := getWebSource(ctx, tx, source.RunID, source.ID)
	if err != nil {
		return err
	}
	if found {
		if existing.Fingerprint != source.Fingerprint {
			return apperror.New(apperror.CodeConflict,
				"web source identity was reused with different metadata")
		}
		return nil
	}
	raw, _ := json.Marshal(source)
	_, err = tx.ExecContext(ctx, `INSERT INTO web_evidence_sources
		(id, protocol_version, run_id, mission_id, workspace_id, canonical_url,
		fingerprint, source_json, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		source.ID, source.ProtocolVersion, source.RunID, source.MissionID,
		source.WorkspaceID, source.CanonicalURL, source.Fingerprint, string(raw),
		ts(source.DiscoveredAt))
	return err
}

func ensureWebSnapshot(ctx context.Context, tx *sql.Tx,
	snapshot webevidence.Snapshot,
) error {
	existing, found, err := getWebSnapshot(ctx, tx, snapshot.RunID, snapshot.ID)
	if err != nil {
		return err
	}
	if found {
		if existing.Fingerprint != snapshot.Fingerprint {
			return apperror.New(apperror.CodeConflict,
				"web snapshot identity was reused with different content")
		}
		return nil
	}
	raw, _ := json.Marshal(snapshot)
	_, err = tx.ExecContext(ctx, `INSERT INTO web_evidence_snapshots
		(id, protocol_version, source_id, run_id, mission_id, fingerprint, digest,
		state, final_url, fetched_at, stale_at, snapshot_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, snapshot.ID,
		snapshot.ProtocolVersion, snapshot.SourceID, snapshot.RunID, snapshot.MissionID,
		snapshot.Fingerprint, snapshot.Digest, snapshot.State, snapshot.FinalURL,
		ts(snapshot.FetchedAt), ts(snapshot.StaleAt), string(raw))
	return err
}

func insertWebOperation(ctx context.Context, tx *sql.Tx,
	operation webevidence.Operation,
) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO web_evidence_operations
		(key_digest, protocol_version, request_fingerprint, run_id, tool_name,
		response_json, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, operation.KeyDigest,
		operation.ProtocolVersion, operation.RequestFingerprint, operation.RunID,
		operation.ToolName, string(operation.Response), ts(operation.CreatedAt))
	return err
}

func (s *SQLiteStore) GetWebSource(ctx context.Context, runID,
	sourceID string,
) (webevidence.Source, error) {
	value, found, err := getWebSource(ctx, s.db, strings.TrimSpace(runID),
		strings.TrimSpace(sourceID))
	if err != nil {
		return webevidence.Source{}, err
	}
	if !found {
		return webevidence.Source{}, apperror.New(apperror.CodeNotFound,
			"web source was not found in this Run")
	}
	return value, nil
}

func getWebSource(ctx context.Context, queryer skillPackageQueryer, runID,
	sourceID string,
) (webevidence.Source, bool, error) {
	var raw string
	err := queryer.QueryRowContext(ctx, `SELECT source_json FROM web_evidence_sources
		WHERE id = ? AND run_id = ?`, sourceID, runID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return webevidence.Source{}, false, nil
	}
	if err != nil {
		return webevidence.Source{}, false, err
	}
	var value webevidence.Source
	if err := json.Unmarshal([]byte(raw), &value); err != nil || value.Validate() != nil {
		return webevidence.Source{}, false, errors.New("stored web source is invalid")
	}
	return value, true, nil
}

func (s *SQLiteStore) GetWebSnapshot(ctx context.Context, runID,
	snapshotID string,
) (webevidence.Snapshot, error) {
	value, found, err := getWebSnapshot(ctx, s.db, strings.TrimSpace(runID),
		strings.TrimSpace(snapshotID))
	if err != nil {
		return webevidence.Snapshot{}, err
	}
	if !found {
		return webevidence.Snapshot{}, apperror.New(apperror.CodeNotFound,
			"web snapshot was not found in this Run")
	}
	return value, nil
}

func getWebSnapshot(ctx context.Context, queryer skillPackageQueryer, runID,
	snapshotID string,
) (webevidence.Snapshot, bool, error) {
	var raw string
	err := queryer.QueryRowContext(ctx, `SELECT snapshot_json FROM web_evidence_snapshots
		WHERE id = ? AND run_id = ?`, snapshotID, runID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return webevidence.Snapshot{}, false, nil
	}
	if err != nil {
		return webevidence.Snapshot{}, false, err
	}
	var value webevidence.Snapshot
	if err := json.Unmarshal([]byte(raw), &value); err != nil || value.Validate() != nil {
		return webevidence.Snapshot{}, false, errors.New("stored web snapshot is invalid")
	}
	return value, true, nil
}

func getWebCitation(ctx context.Context, queryer skillPackageQueryer, runID,
	citationID string,
) (webevidence.Citation, bool, error) {
	var raw string
	err := queryer.QueryRowContext(ctx, `SELECT citation_json FROM web_evidence_citations
		WHERE id = ? AND run_id = ?`, citationID, runID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return webevidence.Citation{}, false, nil
	}
	if err != nil {
		return webevidence.Citation{}, false, err
	}
	var value webevidence.Citation
	if err := json.Unmarshal([]byte(raw), &value); err != nil || value.Validate() != nil {
		return webevidence.Citation{}, false, errors.New("stored web citation is invalid")
	}
	return value, true, nil
}

func (s *SQLiteStore) ListWebSources(ctx context.Context, runID string,
	limit int,
) ([]webevidence.Source, error) {
	return listWebJSON(ctx, s.db, "source_json", "web_evidence_sources", "created_at",
		strings.TrimSpace(runID), limit, func(raw []byte) (webevidence.Source, error) {
			var value webevidence.Source
			err := json.Unmarshal(raw, &value)
			if err == nil {
				err = value.Validate()
			}
			return value, err
		})
}

func (s *SQLiteStore) ListWebSnapshots(ctx context.Context, runID string,
	limit int,
) ([]webevidence.Snapshot, error) {
	return listWebJSON(ctx, s.db, "snapshot_json", "web_evidence_snapshots", "fetched_at",
		strings.TrimSpace(runID), limit, func(raw []byte) (webevidence.Snapshot, error) {
			var value webevidence.Snapshot
			err := json.Unmarshal(raw, &value)
			if err == nil {
				err = value.Validate()
			}
			return value, err
		})
}

func (s *SQLiteStore) ListWebCitations(ctx context.Context, runID string,
	limit int,
) ([]webevidence.Citation, error) {
	return listWebJSON(ctx, s.db, "citation_json", "web_evidence_citations", "created_at",
		strings.TrimSpace(runID), limit, func(raw []byte) (webevidence.Citation, error) {
			var value webevidence.Citation
			err := json.Unmarshal(raw, &value)
			if err == nil {
				err = value.Validate()
			}
			return value, err
		})
}

func listWebJSON[T any](ctx context.Context, queryer skillPackageQueryer, column,
	table, orderColumn, runID string, limit int, decode func([]byte) (T, error),
) ([]T, error) {
	if runID == "" || limit < 1 || limit > 500 {
		return nil, apperror.New(apperror.CodeInvalidArgument,
			"web evidence list requires a Run and limit between 1 and 500")
	}
	query := fmt.Sprintf("SELECT %s FROM %s WHERE run_id = ? ORDER BY %s DESC, id DESC LIMIT ?",
		column, table, orderColumn)
	rows, err := queryer.QueryContext(ctx, query, runID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]T, 0)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		value, err := decode([]byte(raw))
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}
