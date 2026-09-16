package store

import (
	"context"
	"database/sql"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/drydock"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/runmutation"
)

// continueThreadStandardCodeTx carries configuration, never an execution grant.
// The caller has atomically materialized the Thread's current permission and a
// the Thread's validated working directory. Old approvals and runtime leases stay behind.
func continueThreadStandardCodeTx(ctx context.Context, tx *sql.Tx, predecessor domain.Run,
	candidate domain.Run, mode domain.RunModeSnapshot, oldPreset domain.StandardCodePresetOperation,
	newDrydock drydock.Workspace, at time.Time,
) error {
	if err := oldPreset.Validate(); err != nil {
		return err
	}
	if !predecessor.Terminal() || candidate.Status != domain.RunCreated ||
		oldPreset.Status != domain.StandardCodePresetConfigured || oldPreset.RunID != predecessor.ID ||
		oldPreset.MissionID != candidate.MissionID || mode.RunID != candidate.ID ||
		mode.Surface != domain.ExecutionSurfaceCode || newDrydock.ID != oldPreset.DrydockID ||
		newDrydock.SourceWorkspaceID != oldPreset.WorkspaceID {
		return apperror.New(apperror.CodeConflict, "Thread Standard Code continuation binding changed")
	}
	var exact int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_file_drydock_bindings binding
		JOIN drydock_workspaces workspace ON workspace.id=binding.drydock_id
		JOIN drydock_workspace_trust trust_record ON trust_record.id=workspace.trust_id
		WHERE binding.run_id=? AND binding.session_id=? AND binding.drydock_id=?
		AND binding.mission_id=? AND binding.source_workspace_id=?
		AND workspace.generation=? AND workspace.last_checkpoint_id=?
		AND workspace.state IN ('ready','delivered') AND trust_record.run_id=workspace.run_id
		AND trust_record.workspace_id=workspace.source_workspace_id AND trust_record.grants_process_authority=0`,
		candidate.ID, candidate.SessionID, newDrydock.ID, candidate.MissionID, oldPreset.WorkspaceID,
		newDrydock.Generation, newDrydock.LastCheckpointID).Scan(&exact); err != nil {
		return err
	}
	if exact != 1 {
		return apperror.New(apperror.CodeConflict, "Thread coding directory binding changed before configuration")
	}
	oldProfile, err := getCurrentRunExecutionProfileSnapshot(ctx, tx, predecessor.ID)
	if err != nil {
		return err
	}
	oldInteraction, err := getCurrentRunExecutionInteractionSnapshot(ctx, tx, predecessor.ID)
	if err != nil {
		return err
	}
	if oldProfile.Profile != oldPreset.SelectedBackend.ExecutionProfile() ||
		oldInteraction.Mode != domain.RunExecutionInteractionControlled ||
		oldInteraction.ExecutionProfileRevision != oldProfile.Revision {
		return apperror.New(apperror.CodeFailedPrecondition, "The previous coding configuration changed; review its current execution settings")
	}
	profile, err := getCurrentRunExecutionProfileSnapshot(ctx, tx, candidate.ID)
	if err != nil {
		return err
	}
	profile, err = profile.Next(idgen.New("exec-profile"), oldProfile.Profile,
		"thread_continuation", "Continue the selected coding backend; runtime authority is revalidated", at)
	if err != nil {
		return err
	}
	if err := insertRunExecutionProfileSnapshotTx(ctx, tx, profile); err != nil {
		return err
	}
	interaction, err := getCurrentRunExecutionInteractionSnapshot(ctx, tx, candidate.ID)
	if err != nil {
		return err
	}
	interaction, err = interaction.Next(idgen.New("exec-interaction"), oldInteraction.Mode,
		mode, profile, domain.WorkspaceTrustTrusted, true, "thread_continuation",
		"Continue controlled coding in the newly validated working directory", at)
	if err != nil {
		return err
	}
	if err := insertRunExecutionInteractionSnapshotTx(ctx, tx, interaction); err != nil {
		return err
	}
	permission, err := getCurrentRunExecutionPermissionSnapshot(ctx, tx, candidate.ID)
	if err != nil {
		return err
	}
	cdp, err := getCurrentRunBrowserCDPPermissionSnapshot(ctx, tx, candidate.ID)
	if err != nil {
		return err
	}
	operation := domain.StandardCodePresetOperation{
		ProtocolVersion:    domain.StandardCodePresetProtocolVersion,
		KeyDigest:          runmutation.Fingerprint("thread_standard_code_continuation_key", predecessor.ID, candidate.ID, oldPreset.KeyDigest),
		RequestFingerprint: runmutation.Fingerprint("thread_standard_code_continuation", predecessor.ID, candidate.ID, oldPreset.KeyDigest, newDrydock.ID, permission.ID),
		RequestedRunID:     candidate.ID, RunID: candidate.ID, MissionID: candidate.MissionID,
		WorkspaceID: oldPreset.WorkspaceID, Action: domain.StandardCodePresetConfigure,
		BackendIntent: oldPreset.BackendIntent, SelectedBackend: oldPreset.SelectedBackend,
		SelectionReason: oldPreset.SelectionReason, Status: domain.StandardCodePresetPreparing,
		RequestedBy: "thread_continuation", CreatedAt: at, UpdatedAt: at,
	}
	operation, err = insertStandardCodePresetIntentTx(ctx, tx, operation)
	if err != nil {
		return err
	}
	for _, e := range []struct {
		kind    string
		id      string
		payload map[string]any
	}{
		{events.RunExecutionProfileSelectedEvent, profile.ID, map[string]any{"to": profile.Profile, "revision": profile.Revision}},
		{events.RunExecutionInteractionSelectedEvent, interaction.ID, map[string]any{"to": interaction.Mode, "revision": interaction.Revision}},
	} {
		e.payload["source"] = "thread_continuation"
		e.payload["predecessor_run_id"] = predecessor.ID
		e.payload["capability_grant"] = false
		if err := appendStandardCodeSnapshotEventTx(ctx, tx, candidate.ID, candidate.MissionID, e.kind, e.id, at, e.payload); err != nil {
			return err
		}
	}
	event, err := events.New(candidate.ID, candidate.MissionID, events.StandardCodePresetConfiguredEvent,
		"standard_code_preset", candidate.ID, map[string]any{
			"source": "thread_continuation", "predecessor_run_id": predecessor.ID,
			"predecessor_preset_digest": oldPreset.KeyDigest, "drydock_id": newDrydock.ID,
			"phase": mode.Phase, "permission": permission.Mode, "permission_snapshot_id": permission.ID,
			"browser_cdp_snapshot_id": cdp.ID, "capability_grant": false,
		})
	if err != nil {
		return err
	}
	event.CreatedAt = at.UTC()
	inserted, err := insertRunEventTx(ctx, tx, event)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE standard_code_preset_operations SET status='configured',
		drydock_id=?,drydock_generation=?,drydock_checkpoint_id=?,mode_snapshot_id=?,
		profile_snapshot_id=?,interaction_snapshot_id=?,permission_snapshot_id=?,browser_cdp_snapshot_id=?,
		event_sequence_end=?,updated_at=? WHERE operation_key_digest=? AND status='preparing'`,
		newDrydock.ID, newDrydock.Generation, newDrydock.LastCheckpointID, mode.ID, profile.ID,
		interaction.ID, permission.ID, cdp.ID, inserted.Sequence, ts(at), operation.KeyDigest)
	return err
}
