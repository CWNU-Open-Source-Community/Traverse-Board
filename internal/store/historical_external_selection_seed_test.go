package store

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/skills"
)

// This one-shot fixture seed is restricted to the real v177 prefix used by the
// v70 projection test's existing inverse chain. Current databases use the live
// writer. Keep the historical selection, operation and event in one transaction
// with the original foreign keys, triggers and constraints enabled.
type historicalExternalSelectionSeedStore struct {
	*SQLiteStore
}

func (s historicalExternalSelectionSeedStore) CreateExternalSkillSelection(ctx context.Context,
	selection skills.ExternalSelection, operation skills.ExternalSelectionOperation,
	event events.Event,
) (skills.ExternalSelection, bool, error) {
	version, err := s.SchemaVersion(ctx)
	if err != nil {
		return skills.ExternalSelection{}, false, err
	}
	if version != 177 {
		return skills.ExternalSelection{}, false, fmt.Errorf("historical external selection seed rejects schema %d", version)
	}
	if selection.ProtocolVersion != skills.ExternalSelectionProtocolVersion {
		return skills.ExternalSelection{}, false, fmt.Errorf("historical external selection seed requires v1")
	}
	for _, item := range selection.Items {
		if item.Plugin != nil {
			return skills.ExternalSelection{}, false, fmt.Errorf("historical external selection seed rejects Plugin bindings")
		}
	}
	selection = skills.CloneExternalSelection(selection)
	if err := validateExternalSkillSelectionMutation(selection, operation, event); err != nil {
		return skills.ExternalSelection{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return skills.ExternalSelection{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := acquireSkillSelectionWriteLockTx(ctx, tx, selection.RunID); err != nil {
		return skills.ExternalSelection{}, false, err
	}
	run, mission, mode, err := requireExternalSkillSelectionBindingTx(ctx, tx, selection)
	if err != nil {
		return skills.ExternalSelection{}, false, err
	}
	if event.RunID != run.ID || event.MissionID != mission.ID ||
		mode.ID != selection.ModeSnapshotID || !event.CreatedAt.Equal(selection.CreatedAt) {
		return skills.ExternalSelection{}, false, apperror.New(apperror.CodeInvalidArgument,
			"external Skill selection event scope or timestamp does not match")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO run_external_skill_selections
		(id, run_id, mission_id, mode_snapshot_id, mode_revision, protocol_version,
		surface, profile, token_budget, token_upper_bound, item_count,
		selection_fingerprint, requested_by, operator_confirmed,
		context_delivery_authorized, tool_capability_grant, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		selection.ID, selection.RunID, selection.MissionID, selection.ModeSnapshotID,
		selection.ModeRevision, selection.ProtocolVersion, selection.Surface,
		selection.Profile, selection.TokenBudget, selection.TokenUpperBound,
		selection.ItemCount, selection.Fingerprint, selection.RequestedBy,
		boolInt(selection.OperatorConfirmed), boolInt(selection.ContextDeliveryAuthorized),
		boolInt(selection.ToolCapabilityGrant), ts(selection.CreatedAt)); err != nil {
		return skills.ExternalSelection{}, false, err
	}
	for _, item := range selection.Items {
		if _, err := tx.ExecContext(ctx, `INSERT INTO run_external_skill_selection_items
			(selection_id, ordinal, installation_id, installation_fingerprint,
			install_result_fingerprint, name, version, surface, content_sha256,
			content_bytes, token_upper_bound, archive_sha256, archive_bytes,
			package_fingerprint, object_key, trust_class, tool_dependency_count,
			specialist_eligible) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			item.SelectionID, item.Ordinal, item.InstallationID,
			item.InstallationFingerprint, item.InstallResultFingerprint, item.Name,
			item.Version, item.Surface, item.ContentSHA256, item.ContentBytes,
			item.TokenUpperBound, item.ArchiveSHA256, item.ArchiveBytes,
			item.PackageFingerprint, item.ObjectKey, item.TrustClass,
			item.ToolDependencyCount, boolInt(item.SpecialistEligible)); err != nil {
			return skills.ExternalSelection{}, false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO run_external_skill_selection_operations
		(operation_key_digest, request_fingerprint, selection_id, run_id,
		requested_by, created_at) VALUES (?, ?, ?, ?, ?, ?)`, operation.KeyDigest,
		operation.RequestFingerprint, operation.SelectionID, operation.RunID,
		operation.RequestedBy, ts(operation.CreatedAt)); err != nil {
		return skills.ExternalSelection{}, false, err
	}
	if _, err := insertRunEventTx(ctx, tx, event); err != nil {
		return skills.ExternalSelection{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return skills.ExternalSelection{}, false, err
	}
	return skills.CloneExternalSelection(selection), false, nil
}

func TestHistoricalExternalSelectionSeedRejectsCurrentSchema(t *testing.T) {
	state := openRunSeedBoundaryStore(t, 0)
	before := runSeedBoundaryRows(t, state)
	_, _, err := (historicalExternalSelectionSeedStore{state}).CreateExternalSkillSelection(
		t.Context(), skills.ExternalSelection{}, skills.ExternalSelectionOperation{}, events.Event{})
	if err == nil || !strings.Contains(err.Error(), "rejects schema") {
		t.Fatalf("historical external selection seed accepted current schema: %v", err)
	}
	if after := runSeedBoundaryRows(t, state); !reflect.DeepEqual(before, after) {
		t.Fatalf("rejected historical selection seed changed rows: before=%v after=%v", before, after)
	}
}
