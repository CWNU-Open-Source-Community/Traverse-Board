package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"cyberagent-workbench/internal/analyzer"
	"cyberagent-workbench/internal/events"
)

func (s *SQLiteStore) RegisterAnalyzerExecutionCapability(ctx context.Context,
	capability analyzer.AnalyzerExecutionCapability,
) (analyzer.AnalyzerExecutionCapability, bool, error) {
	if s == nil || s.db == nil || ctx == nil {
		return analyzer.AnalyzerExecutionCapability{}, false,
			errors.New("analyzer execution capability store is unavailable")
	}
	if code := analyzer.ValidateAnalyzerExecutionCapability(capability); code != "" {
		return analyzer.AnalyzerExecutionCapability{}, false,
			fmt.Errorf("invalid analyzer execution capability: %s", code)
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return analyzer.AnalyzerExecutionCapability{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if existing, found, err := loadAnalyzerExecutionCapabilityTx(ctx, tx,
		capability.ID); err != nil {
		return analyzer.AnalyzerExecutionCapability{}, false, err
	} else if found {
		if !analyzer.AnalyzerExecutionCapabilityEqual(existing, capability) {
			return analyzer.AnalyzerExecutionCapability{}, false,
				errors.New("analyzer execution capability ID was reused")
		}
		return existing, true, nil
	}
	var existingID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM analyzer_execution_capabilities
		WHERE bearer_token_sha256 = ?`, capability.BearerTokenSHA256).Scan(&existingID)
	if err == nil {
		return analyzer.AnalyzerExecutionCapability{}, false,
			fmt.Errorf("analyzer execution bearer was already bound to %s", existingID)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return analyzer.AnalyzerExecutionCapability{}, false, err
	}
	missionID, workspaceID, terminal, err := analyzerStartRunBindingTx(ctx, tx,
		capability.RunID)
	if err != nil {
		return analyzer.AnalyzerExecutionCapability{}, false, err
	}
	if terminal || workspaceID != capability.WorkspaceID {
		return analyzer.AnalyzerExecutionCapability{}, false,
			errors.New("analyzer execution capability run/workspace binding is invalid")
	}
	event, err := events.New(capability.RunID, missionID,
		events.AnalyzerExecutionCapabilityIssuedEvent, "analyzer_execution", capability.ID,
		map[string]any{"capability_fingerprint": capability.Fingerprint,
			"module_sha256": capability.ModuleSHA256, "redacted": true})
	if err != nil {
		return analyzer.AnalyzerExecutionCapability{}, false, err
	}
	event.CreatedAt = capability.IssuedAt
	event, err = insertRunEventTx(ctx, tx, event)
	if err != nil {
		return analyzer.AnalyzerExecutionCapability{}, false, err
	}
	payload, code := analyzer.EncodeAnalyzerExecutionCapability(capability)
	if code != "" {
		return analyzer.AnalyzerExecutionCapability{}, false,
			fmt.Errorf("encode analyzer execution capability: %s", code)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO analyzer_execution_capabilities
		(id, run_id, mission_id, workspace_id, request_id, request_sha256,
		 candidate_sha256, module_sha256, bearer_token_sha256, fingerprint,
		 event_sequence, payload_json, issued_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		capability.ID, capability.RunID, missionID, capability.WorkspaceID,
		capability.RequestID, capability.RequestSHA256, capability.CandidateSHA256,
		capability.ModuleSHA256, capability.BearerTokenSHA256, capability.Fingerprint,
		event.Sequence, string(payload), ts(capability.IssuedAt), ts(capability.ExpiresAt))
	if err != nil {
		return analyzer.AnalyzerExecutionCapability{}, false,
			fmt.Errorf("insert analyzer execution capability: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return analyzer.AnalyzerExecutionCapability{}, false, err
	}
	return capability, false, nil
}

func (s *SQLiteStore) ConsumeAnalyzerExecutionCapability(ctx context.Context,
	capabilityID, consumptionID string, bearerToken []byte, candidate analyzer.InvocationCandidate,
	at time.Time,
) (analyzer.AnalyzerExecutionConsumption, error) {
	if s == nil || s.db == nil || ctx == nil {
		return analyzer.AnalyzerExecutionConsumption{},
			errors.New("analyzer execution capability store is unavailable")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return analyzer.AnalyzerExecutionConsumption{}, err
	}
	defer func() { _ = tx.Rollback() }()
	capability, found, err := loadAnalyzerExecutionCapabilityTx(ctx, tx, capabilityID)
	if err != nil {
		return analyzer.AnalyzerExecutionConsumption{}, err
	}
	if !found {
		return analyzer.AnalyzerExecutionConsumption{},
			errors.New("analyzer execution capability not found")
	}
	if _, found, err := loadAnalyzerExecutionConsumptionTx(ctx, tx,
		capabilityID, capability); err != nil {
		return analyzer.AnalyzerExecutionConsumption{}, err
	} else if found {
		return analyzer.AnalyzerExecutionConsumption{},
			errors.New("analyzer execution capability was already consumed")
	}
	consumption, code := analyzer.BuildAnalyzerExecutionConsumption(consumptionID,
		capability, bearerToken, candidate, at)
	if code != "" {
		return analyzer.AnalyzerExecutionConsumption{},
			fmt.Errorf("consume analyzer execution capability: %s", code)
	}
	missionID, workspaceID, terminal, err := analyzerStartRunBindingTx(ctx, tx,
		capability.RunID)
	if err != nil {
		return analyzer.AnalyzerExecutionConsumption{}, err
	}
	if terminal || workspaceID != capability.WorkspaceID {
		return analyzer.AnalyzerExecutionConsumption{},
			errors.New("analyzer execution capability run is no longer active")
	}
	event, err := events.New(capability.RunID, missionID,
		events.AnalyzerExecutionCapabilityConsumedEvent, "analyzer_execution", consumption.ID,
		map[string]any{"consumption_fingerprint": consumption.Fingerprint,
			"capability_id": capability.ID, "redacted": true})
	if err != nil {
		return analyzer.AnalyzerExecutionConsumption{}, err
	}
	event.CreatedAt = consumption.ConsumedAt
	event, err = insertRunEventTx(ctx, tx, event)
	if err != nil {
		return analyzer.AnalyzerExecutionConsumption{}, err
	}
	payload, code := analyzer.EncodeAnalyzerExecutionConsumption(consumption, capability)
	if code != "" {
		return analyzer.AnalyzerExecutionConsumption{},
			fmt.Errorf("encode analyzer execution consumption: %s", code)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO analyzer_execution_consumptions
		(id, capability_id, run_id, workspace_id, request_id, fingerprint,
		 event_sequence, payload_json, consumed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, consumption.ID, capability.ID,
		capability.RunID, capability.WorkspaceID, capability.RequestID,
		consumption.Fingerprint, event.Sequence, string(payload), ts(consumption.ConsumedAt))
	if err != nil {
		return analyzer.AnalyzerExecutionConsumption{},
			fmt.Errorf("insert analyzer execution consumption: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return analyzer.AnalyzerExecutionConsumption{}, err
	}
	return consumption, nil
}

func (s *SQLiteStore) LoadAnalyzerExecutionCapability(ctx context.Context, id string) (
	analyzer.AnalyzerExecutionCapability, bool, error,
) {
	if s == nil || s.db == nil || ctx == nil {
		return analyzer.AnalyzerExecutionCapability{}, false,
			errors.New("analyzer execution capability store is unavailable")
	}
	var payload string
	err := s.db.QueryRowContext(ctx, `SELECT payload_json FROM analyzer_execution_capabilities
		WHERE id = ?`, id).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return analyzer.AnalyzerExecutionCapability{}, false, nil
	}
	if err != nil {
		return analyzer.AnalyzerExecutionCapability{}, false, err
	}
	value, code := analyzer.DecodeAnalyzerExecutionCapability([]byte(payload))
	if code != "" {
		return analyzer.AnalyzerExecutionCapability{}, false,
			fmt.Errorf("decode analyzer execution capability: %s", code)
	}
	return value, true, nil
}

func loadAnalyzerExecutionCapabilityTx(ctx context.Context, tx *sql.Tx, id string) (
	analyzer.AnalyzerExecutionCapability, bool, error,
) {
	var payload string
	err := tx.QueryRowContext(ctx, `SELECT payload_json FROM analyzer_execution_capabilities
		WHERE id = ?`, id).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return analyzer.AnalyzerExecutionCapability{}, false, nil
	}
	if err != nil {
		return analyzer.AnalyzerExecutionCapability{}, false, err
	}
	value, code := analyzer.DecodeAnalyzerExecutionCapability([]byte(payload))
	if code != "" {
		return analyzer.AnalyzerExecutionCapability{}, false,
			fmt.Errorf("decode analyzer execution capability: %s", code)
	}
	return value, true, nil
}

func loadAnalyzerExecutionConsumptionTx(ctx context.Context, tx *sql.Tx, capabilityID string,
	capability analyzer.AnalyzerExecutionCapability,
) (analyzer.AnalyzerExecutionConsumption, bool, error) {
	var payload string
	err := tx.QueryRowContext(ctx, `SELECT payload_json FROM analyzer_execution_consumptions
		WHERE capability_id = ?`, capabilityID).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return analyzer.AnalyzerExecutionConsumption{}, false, nil
	}
	if err != nil {
		return analyzer.AnalyzerExecutionConsumption{}, false, err
	}
	value, code := analyzer.DecodeAnalyzerExecutionConsumption([]byte(payload), capability)
	if code != "" {
		return analyzer.AnalyzerExecutionConsumption{}, false,
			fmt.Errorf("decode analyzer execution consumption: %s", code)
	}
	return value, true, nil
}
