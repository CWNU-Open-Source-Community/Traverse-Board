package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"cyberagent-workbench/internal/analyzer"
	"cyberagent-workbench/internal/artifact"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/idgen"
)

const embeddedAnalyzerToolName = "embedded_analyzer"

// CommitAnalyzerExecution atomically records a successful embedded guest
// receipt and its metadata-only output artifact. The input request and bearer
// token are deliberately absent from this boundary.
func (s *SQLiteStore) CommitAnalyzerExecution(ctx context.Context,
	request analyzer.AnalyzerExecutionCommitRequest,
) (analyzer.AnalyzerExecutionRecord, artifact.Descriptor, bool, error) {
	if s == nil || s.db == nil || ctx == nil {
		return analyzer.AnalyzerExecutionRecord{}, artifact.Descriptor{}, false,
			errors.New("analyzer execution store is unavailable")
	}
	if err := analyzer.ValidateAnalyzerExecutionCommitRequest(request); err != nil {
		return analyzer.AnalyzerExecutionRecord{}, artifact.Descriptor{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return analyzer.AnalyzerExecutionRecord{}, artifact.Descriptor{}, false, err
	}
	defer func() { _ = tx.Rollback() }()

	if existing, descriptor, found, err := loadAnalyzerExecutionTx(ctx, tx, request.ID); err != nil {
		return analyzer.AnalyzerExecutionRecord{}, artifact.Descriptor{}, false, err
	} else if found {
		expected, code := analyzer.BuildAnalyzerExecutionRecord(request, existing.ArtifactID,
			artifact.Hash(string(request.RawResult)), len(request.RawResult))
		if code != "" || !reflect.DeepEqual(existing, expected) {
			return analyzer.AnalyzerExecutionRecord{}, artifact.Descriptor{}, false,
				errors.New("analyzer execution ID was reused")
		}
		return existing, descriptor, true, nil
	}

	capability, found, err := loadAnalyzerExecutionCapabilityTx(ctx, tx, request.CapabilityID)
	if err != nil {
		return analyzer.AnalyzerExecutionRecord{}, artifact.Descriptor{}, false, err
	}
	if !found {
		return analyzer.AnalyzerExecutionRecord{}, artifact.Descriptor{}, false,
			errors.New("analyzer execution capability not found")
	}
	consumption, found, err := loadAnalyzerExecutionConsumptionTx(ctx, tx,
		request.CapabilityID, capability)
	if err != nil {
		return analyzer.AnalyzerExecutionRecord{}, artifact.Descriptor{}, false, err
	}
	if !found || consumption.ID != request.ConsumptionID ||
		capability.RunID != request.RunID || capability.WorkspaceID != request.WorkspaceID ||
		capability.RequestID != request.Candidate.RequestID ||
		capability.RequestSHA256 != request.Candidate.RequestSHA256 ||
		capability.CandidateSHA256 != request.Execution.CandidateSHA256 ||
		capability.ModuleSHA256 != request.Execution.ModuleSHA256 {
		return analyzer.AnalyzerExecutionRecord{}, artifact.Descriptor{}, false,
			errors.New("analyzer execution capability, consumption, or candidate binding changed")
	}
	missionID, workspaceID, terminal, err := analyzerStartRunBindingTx(ctx, tx, request.RunID)
	if err != nil {
		return analyzer.AnalyzerExecutionRecord{}, artifact.Descriptor{}, false, err
	}
	var sessionID string
	if err := tx.QueryRowContext(ctx, `SELECT session_id FROM runs WHERE id = ?`, request.RunID).
		Scan(&sessionID); err != nil {
		return analyzer.AnalyzerExecutionRecord{}, artifact.Descriptor{}, false, err
	}
	if terminal || sessionID != request.SessionID || workspaceID != request.WorkspaceID {
		return analyzer.AnalyzerExecutionRecord{}, artifact.Descriptor{}, false,
			errors.New("analyzer execution Run, Session, or Workspace binding is no longer active")
	}

	capture, err := artifact.NormalizeCaptureRequest(artifact.CaptureRequest{
		RunID: request.RunID, SessionID: request.SessionID, WorkspaceID: request.WorkspaceID,
		SourceID: request.ID, ToolName: embeddedAnalyzerToolName,
		Outputs: []artifact.Output{{Stream: artifact.StreamStdout,
			MIME: "application/json", Content: string(request.RawResult)}},
	})
	if err != nil {
		return analyzer.AnalyzerExecutionRecord{}, artifact.Descriptor{}, false, err
	}
	if capture.Outputs[0].Content != string(request.RawResult) {
		return analyzer.AnalyzerExecutionRecord{}, artifact.Descriptor{}, false,
			errors.New("metadata-only analyzer result unexpectedly required redaction")
	}
	output := capture.Outputs[0]
	descriptor := artifact.Descriptor{
		ID: idgen.New("artifact"), RunID: request.RunID, SessionID: request.SessionID,
		WorkspaceID: request.WorkspaceID, SourceID: request.ID, ToolName: embeddedAnalyzerToolName,
		Stream: output.Stream, Kind: artifact.KindToolOutput, MIME: output.MIME,
		Encoding: artifact.EncodingUTF8, SHA256: artifact.Hash(output.Content),
		SizeBytes: int64(len([]byte(output.Content))), Redacted: output.Redacted,
		CreatedAt: request.CreatedAt.UTC(),
	}
	if err := descriptor.Validate(); err != nil {
		return analyzer.AnalyzerExecutionRecord{}, artifact.Descriptor{}, false, err
	}
	record, code := analyzer.BuildAnalyzerExecutionRecord(request, descriptor.ID,
		descriptor.SHA256, int(descriptor.SizeBytes))
	if code != "" {
		return analyzer.AnalyzerExecutionRecord{}, artifact.Descriptor{}, false,
			fmt.Errorf("build analyzer execution record: %s", code)
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return analyzer.AnalyzerExecutionRecord{}, artifact.Descriptor{}, false, err
	}

	if _, err := tx.ExecContext(ctx, `INSERT INTO run_artifacts
		(id, run_id, session_id, workspace_id, source_id, tool_name, stream, kind, mime, encoding,
		 sha256, size_bytes, content, redacted, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, descriptor.ID,
		descriptor.RunID, descriptor.SessionID, descriptor.WorkspaceID, descriptor.SourceID,
		descriptor.ToolName, descriptor.Stream, descriptor.Kind, descriptor.MIME,
		descriptor.Encoding, descriptor.SHA256, descriptor.SizeBytes, output.Content,
		descriptor.Redacted, ts(descriptor.CreatedAt)); err != nil {
		return analyzer.AnalyzerExecutionRecord{}, artifact.Descriptor{}, false, err
	}
	completedEvent, err := events.New(record.RunID, missionID,
		events.AnalyzerExecutionCompletedEvent, "analyzer_execution", record.ID,
		map[string]any{"execution_fingerprint": record.Fingerprint,
			"artifact_id": record.ArtifactID, "result_sha256": record.ResultSHA256,
			"result_bytes": record.ResultBytes, "redacted": true})
	if err != nil {
		return analyzer.AnalyzerExecutionRecord{}, artifact.Descriptor{}, false, err
	}
	completedEvent.CreatedAt = record.CreatedAt
	completedEvent, err = insertRunEventTx(ctx, tx, completedEvent)
	if err != nil {
		return analyzer.AnalyzerExecutionRecord{}, artifact.Descriptor{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO analyzer_executions
		(id, run_id, session_id, workspace_id, capability_id, consumption_id, requested_by, request_id,
		 request_sha256, candidate_sha256, module_sha256, execution_fingerprint,
		 result_sha256, result_bytes, artifact_id, fingerprint, event_sequence, payload_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, record.ID,
		record.RunID, record.SessionID, record.WorkspaceID, record.CapabilityID,
		record.ConsumptionID, record.RequestedBy, record.RequestID, record.RequestSHA256, record.CandidateSHA256,
		record.ModuleSHA256, record.Execution.Fingerprint, record.ResultSHA256,
		record.ResultBytes, record.ArtifactID, record.Fingerprint, completedEvent.Sequence,
		string(payload), ts(record.CreatedAt)); err != nil {
		return analyzer.AnalyzerExecutionRecord{}, artifact.Descriptor{}, false,
			fmt.Errorf("insert analyzer execution: %w", err)
	}
	artifactEvent, err := events.New(record.RunID, missionID, events.ArtifactCreatedEvent,
		"artifact_store", descriptor.ID, map[string]any{
			"artifact_id": descriptor.ID, "source_id": descriptor.SourceID,
			"session_id": descriptor.SessionID, "workspace_id": descriptor.WorkspaceID,
			"tool_name": descriptor.ToolName, "stream": descriptor.Stream,
			"kind": descriptor.Kind, "mime": descriptor.MIME, "encoding": descriptor.Encoding,
			"sha256": descriptor.SHA256, "size_bytes": descriptor.SizeBytes,
			"redacted": descriptor.Redacted,
		})
	if err != nil {
		return analyzer.AnalyzerExecutionRecord{}, artifact.Descriptor{}, false, err
	}
	artifactEvent.CreatedAt = record.CreatedAt
	if _, err := insertRunEventTx(ctx, tx, artifactEvent); err != nil {
		return analyzer.AnalyzerExecutionRecord{}, artifact.Descriptor{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return analyzer.AnalyzerExecutionRecord{}, artifact.Descriptor{}, false, err
	}
	return record, descriptor, false, nil
}

func (s *SQLiteStore) GetAnalyzerExecution(ctx context.Context, id string) (
	analyzer.AnalyzerExecutionRecord, artifact.Descriptor, bool, error,
) {
	if s == nil || s.db == nil || ctx == nil {
		return analyzer.AnalyzerExecutionRecord{}, artifact.Descriptor{}, false,
			errors.New("analyzer execution store is unavailable")
	}
	var payload, artifactID string
	err := s.db.QueryRowContext(ctx, `SELECT payload_json, artifact_id
		FROM analyzer_executions WHERE id = ?`, id).Scan(&payload, &artifactID)
	if errors.Is(err, sql.ErrNoRows) {
		return analyzer.AnalyzerExecutionRecord{}, artifact.Descriptor{}, false, nil
	}
	if err != nil {
		return analyzer.AnalyzerExecutionRecord{}, artifact.Descriptor{}, false, err
	}
	var record analyzer.AnalyzerExecutionRecord
	if err := json.Unmarshal([]byte(payload), &record); err != nil ||
		analyzer.ValidateAnalyzerExecutionRecord(record) != "" || record.ArtifactID != artifactID {
		return analyzer.AnalyzerExecutionRecord{}, artifact.Descriptor{}, false,
			errors.New("stored analyzer execution is invalid")
	}
	descriptor, err := scanRunArtifactDescriptor(s.db.QueryRowContext(ctx,
		runArtifactDescriptorSelect+` WHERE id = ?`, artifactID))
	if err != nil {
		return analyzer.AnalyzerExecutionRecord{}, artifact.Descriptor{}, false, err
	}
	return record, descriptor, true, nil
}

func loadAnalyzerExecutionTx(ctx context.Context, tx *sql.Tx, id string) (
	analyzer.AnalyzerExecutionRecord, artifact.Descriptor, bool, error,
) {
	var payload, artifactID string
	err := tx.QueryRowContext(ctx, `SELECT payload_json, artifact_id FROM analyzer_executions
		WHERE id = ?`, id).Scan(&payload, &artifactID)
	if errors.Is(err, sql.ErrNoRows) {
		return analyzer.AnalyzerExecutionRecord{}, artifact.Descriptor{}, false, nil
	}
	if err != nil {
		return analyzer.AnalyzerExecutionRecord{}, artifact.Descriptor{}, false, err
	}
	var record analyzer.AnalyzerExecutionRecord
	if err := json.Unmarshal([]byte(payload), &record); err != nil ||
		analyzer.ValidateAnalyzerExecutionRecord(record) != "" || record.ArtifactID != artifactID {
		return analyzer.AnalyzerExecutionRecord{}, artifact.Descriptor{}, false,
			errors.New("stored analyzer execution is invalid")
	}
	descriptor, err := scanRunArtifactDescriptor(tx.QueryRowContext(ctx,
		runArtifactDescriptorSelect+` WHERE id = ?`, artifactID))
	if err != nil {
		return analyzer.AnalyzerExecutionRecord{}, artifact.Descriptor{}, false, err
	}
	return record, descriptor, true, nil
}
