package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/skills"
)

const rootSkillContextPreparationSelect = `SELECT id, run_id, mission_id,
	root_agent_id, supervisor_attempt_id, turn_number, selection_id,
	protocol_version, profile, selection_fingerprint, context_fingerprint,
	item_count, token_budget, token_upper_bound, redaction_count, prepared_at
	FROM root_skill_context_preparations`

const rootSkillContextCommitSelect = `SELECT preparation_id, run_id,
	supervisor_attempt_id, model_attempt, committed_at
	FROM root_skill_context_commits`

const rootModeSkillContextPreparationSelect = `SELECT id, run_id, mission_id,
	root_agent_id, supervisor_attempt_id, turn_number, selection_id,
	protocol_version, profile, mode_snapshot_id, mode_revision, surface, phase,
	selection_fingerprint, context_fingerprint, selection_item_count, item_count,
	token_budget, token_upper_bound, redaction_count, prepared_at
	FROM root_mode_skill_context_preparations`

const rootModeSkillContextCommitSelect = `SELECT preparation_id, run_id,
	supervisor_attempt_id, model_attempt, committed_at
	FROM root_mode_skill_context_commits`

func (s *SQLiteStore) PrepareRootSkillContext(ctx context.Context,
	checkpoint domain.SupervisorCheckpoint, request skills.RootContextPreparationRequest,
) (skills.RootContextPreparation, error) {
	if err := checkpoint.Validate(); err != nil {
		return skills.RootContextPreparation{}, err
	}
	if checkpoint.Phase != domain.SupervisorTurnStarted {
		return skills.RootContextPreparation{}, apperror.New(apperror.CodeFailedPrecondition,
			"only a started Supervisor turn can prepare root Skill context")
	}
	if err := request.Validate(); err != nil {
		return skills.RootContextPreparation{}, apperror.Wrap(apperror.CodeInvalidArgument,
			"root Skill context preparation is invalid", err)
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return skills.RootContextPreparation{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := acquireSkillContextWriteLockTx(ctx, tx, checkpoint.RunID); err != nil {
		return skills.RootContextPreparation{}, err
	}
	run, current, err := requireActiveSupervisorAttemptTx(ctx, tx, checkpoint)
	if err != nil {
		return skills.RootContextPreparation{}, err
	}
	root, found, err := getRootAgentTx(ctx, tx, run.ID)
	if err != nil {
		return skills.RootContextPreparation{}, err
	}
	if !found || root.Role != domain.AgentRoleRoot || root.Status != domain.AgentRunning ||
		root.ActiveAttemptID != current.AttemptID {
		return skills.RootContextPreparation{}, apperror.New(apperror.CodeConflict,
			"root Agent is not bound to the preparing Supervisor attempt")
	}
	if request.RunID != run.ID || request.MissionID != run.MissionID ||
		request.RootAgentID != root.ID || request.SupervisorAttemptID != current.AttemptID ||
		request.Turn != current.NextTurn {
		return skills.RootContextPreparation{}, apperror.New(apperror.CodeInvalidArgument,
			"root Skill context scope does not match the active Supervisor turn")
	}
	selection, found, err := getSkillSelectionByRun(ctx, tx, run.ID)
	if err != nil {
		return skills.RootContextPreparation{}, err
	}
	if !found {
		return skills.RootContextPreparation{}, apperror.New(apperror.CodeFailedPrecondition,
			"root Skill context requires a persisted Run selection")
	}
	if request.ModeBound() {
		return prepareRootModeSkillContextTx(ctx, tx, run, selection, request)
	}
	if err := validateRootSkillContextSelection(selection, request); err != nil {
		return skills.RootContextPreparation{}, err
	}

	existing, found, err := getRootSkillContextPreparationByAttemptTx(ctx, tx,
		request.RunID, request.SupervisorAttemptID)
	if err != nil {
		return skills.RootContextPreparation{}, err
	}
	if found {
		if !sameRootSkillContextRequest(existing.RootContextPreparationRequest, request) {
			return skills.RootContextPreparation{}, apperror.New(apperror.CodeConflict,
				"prepared root Skill context does not match the reconstructed context")
		}
		existing.Recovered = true
		if err := tx.Commit(); err != nil {
			return skills.RootContextPreparation{}, err
		}
		return existing, nil
	}

	preparation := skills.RootContextPreparation{
		ID: idgen.New("skillctx"), RootContextPreparationRequest: request,
		PreparedAt: time.Now().UTC(),
	}
	if err := preparation.Validate(); err != nil {
		return skills.RootContextPreparation{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO root_skill_context_preparations
		(id, run_id, mission_id, root_agent_id, supervisor_attempt_id, turn_number,
		selection_id, protocol_version, profile, selection_fingerprint,
		context_fingerprint, item_count, token_budget, token_upper_bound,
		redaction_count, prepared_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		preparation.ID, request.RunID, request.MissionID, request.RootAgentID,
		request.SupervisorAttemptID, request.Turn, request.SelectionID,
		request.ProtocolVersion, request.Profile, request.SelectionFingerprint,
		request.ContextFingerprint, request.ItemCount, request.TokenBudget,
		request.TokenUpperBound, request.RedactionCount, ts(preparation.PreparedAt)); err != nil {
		return skills.RootContextPreparation{}, err
	}
	if err := appendSupervisorEventTx(ctx, tx, run, events.SkillContextPreparedEvent,
		"skills", preparation.ID, rootSkillContextEventPayload(preparation, 0)); err != nil {
		return skills.RootContextPreparation{}, err
	}
	if err := tx.Commit(); err != nil {
		return skills.RootContextPreparation{}, err
	}
	return preparation, nil
}

func commitRootSkillContextTx(ctx context.Context, tx *sql.Tx, run domain.Run,
	checkpoint domain.SupervisorCheckpoint, modelAttempt int,
) error {
	selection, selected, err := getSkillSelectionByRun(ctx, tx, run.ID)
	if err != nil {
		return err
	}
	preparation, prepared, err := getRootSkillContextPreparationByAttemptTx(ctx, tx,
		run.ID, checkpoint.AttemptID)
	if err != nil {
		return err
	}
	modePreparation, modePrepared, err := getRootModeSkillContextPreparationByAttemptTx(ctx,
		tx, run.ID, checkpoint.AttemptID)
	if err != nil {
		return err
	}
	if prepared && modePrepared {
		return apperror.New(apperror.CodeFailedPrecondition,
			"root Skill context has conflicting legacy and mode-bound preparations")
	}
	if !selected {
		if prepared || modePrepared {
			return apperror.New(apperror.CodeFailedPrecondition,
				"root Skill context exists without a persisted selection")
		}
		return nil
	}
	if modePrepared {
		return commitRootModeSkillContextTx(ctx, tx, run, checkpoint, selection,
			modePreparation, modelAttempt)
	}
	if !prepared {
		return apperror.New(apperror.CodeFailedPrecondition,
			"persisted Skill selection was not prepared for the active root turn")
	}
	if err := validateRootSkillContextSelection(selection,
		preparation.RootContextPreparationRequest); err != nil {
		return err
	}
	root, found, err := getRootAgentTx(ctx, tx, run.ID)
	if err != nil {
		return err
	}
	if !found || root.ID != preparation.RootAgentID || root.Role != domain.AgentRoleRoot ||
		root.Status != domain.AgentRunning || root.ActiveAttemptID != checkpoint.AttemptID ||
		preparation.Turn != checkpoint.NextTurn {
		return apperror.New(apperror.CodeConflict,
			"prepared root Skill context is not bound to the active root Agent")
	}
	existing, found, err := getRootSkillContextCommitTx(ctx, tx, preparation.ID)
	if err != nil {
		return err
	}
	if found {
		if existing.RunID != run.ID || existing.SupervisorAttemptID != checkpoint.AttemptID ||
			existing.ModelAttempt <= 0 || existing.ModelAttempt > modelAttempt {
			return apperror.New(apperror.CodeConflict,
				"root Skill context commit does not match the model attempt")
		}
		return nil
	}
	commit := skills.RootContextCommit{
		PreparationID: preparation.ID, RunID: run.ID,
		SupervisorAttemptID: checkpoint.AttemptID, ModelAttempt: modelAttempt,
		CommittedAt: time.Now().UTC(),
	}
	if err := commit.Validate(); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO root_skill_context_commits
		(preparation_id, run_id, supervisor_attempt_id, model_attempt, committed_at)
		VALUES (?, ?, ?, ?, ?)`, commit.PreparationID, commit.RunID,
		commit.SupervisorAttemptID, commit.ModelAttempt, ts(commit.CommittedAt)); err != nil {
		return err
	}
	if err := appendSupervisorEventTx(ctx, tx, run, events.SkillContextCommittedEvent,
		"skills", preparation.ID, rootSkillContextEventPayload(preparation,
			commit.ModelAttempt)); err != nil {
		return err
	}
	return nil
}

func rootSkillContextEventPayload(preparation skills.RootContextPreparation,
	modelAttempt int,
) map[string]any {
	payload := map[string]any{
		"protocol": preparation.ProtocolVersion, "agent_id": preparation.RootAgentID,
		"turn": preparation.Turn, "item_count": preparation.ItemCount,
		"token_budget":      preparation.TokenBudget,
		"token_upper_bound": preparation.TokenUpperBound,
		"redaction_count":   preparation.RedactionCount,
		"root_only":         true, "tool_capability_grant": false,
	}
	if modelAttempt > 0 {
		payload["model_attempt"] = modelAttempt
	}
	if preparation.ModeBound() {
		payload["mode_revision"] = preparation.ModeRevision
		payload["surface"] = preparation.Surface
		payload["phase"] = preparation.Phase
		payload["selection_item_count"] = preparation.SelectionItemCount
	}
	return payload
}

func validateRootSkillContextSelection(selection skills.Selection,
	request skills.RootContextPreparationRequest,
) error {
	if err := selection.Validate(); err != nil {
		return apperror.Wrap(apperror.CodeFailedPrecondition,
			"durable Skill selection is invalid", err)
	}
	if request.ModeBound() || selection.ID != request.SelectionID || selection.RunID != request.RunID ||
		selection.MissionID != request.MissionID || selection.Profile != request.Profile ||
		selection.Fingerprint != request.SelectionFingerprint ||
		selection.ItemCount != request.ItemCount || selection.TokenBudget != request.TokenBudget ||
		request.TokenUpperBound > selection.TokenUpperBound {
		return apperror.New(apperror.CodeFailedPrecondition,
			"root Skill context does not match its persisted selection")
	}
	return nil
}

func prepareRootModeSkillContextTx(ctx context.Context, tx *sql.Tx, run domain.Run,
	selection skills.Selection, request skills.RootContextPreparationRequest,
) (skills.RootContextPreparation, error) {
	if err := validateRootModeSkillContextSelection(ctx, tx, selection, request); err != nil {
		return skills.RootContextPreparation{}, err
	}
	existing, found, err := getRootModeSkillContextPreparationByAttemptTx(ctx, tx,
		request.RunID, request.SupervisorAttemptID)
	if err != nil {
		return skills.RootContextPreparation{}, err
	}
	if found {
		if !sameRootSkillContextRequest(existing.RootContextPreparationRequest, request) {
			return skills.RootContextPreparation{}, apperror.New(apperror.CodeConflict,
				"prepared mode-bound root Skill context does not match the reconstructed context")
		}
		existing.Recovered = true
		if err := tx.Commit(); err != nil {
			return skills.RootContextPreparation{}, err
		}
		return existing, nil
	}
	legacy, found, err := getRootSkillContextPreparationByAttemptTx(ctx, tx,
		request.RunID, request.SupervisorAttemptID)
	if err != nil {
		return skills.RootContextPreparation{}, err
	}
	if found {
		legacyRequest := request
		legacyRequest.ModeSnapshotID = ""
		legacyRequest.ModeRevision = 0
		legacyRequest.Surface = ""
		legacyRequest.Phase = ""
		legacyRequest.SelectionItemCount = 0
		if !sameRootSkillContextRequest(legacy.RootContextPreparationRequest, legacyRequest) {
			return skills.RootContextPreparation{}, apperror.New(apperror.CodeConflict,
				"legacy root Skill context does not match the reconstructed mode context")
		}
		legacy.Recovered = true
		if err := tx.Commit(); err != nil {
			return skills.RootContextPreparation{}, err
		}
		return legacy, nil
	}
	preparation := skills.RootContextPreparation{
		ID: idgen.New("skillctx"), RootContextPreparationRequest: request,
		PreparedAt: time.Now().UTC(),
	}
	if err := preparation.Validate(); err != nil {
		return skills.RootContextPreparation{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO root_mode_skill_context_preparations
		(id, run_id, mission_id, root_agent_id, supervisor_attempt_id, turn_number,
		selection_id, protocol_version, profile, mode_snapshot_id, mode_revision,
		surface, phase, selection_fingerprint, context_fingerprint,
		selection_item_count, item_count, token_budget, token_upper_bound,
		redaction_count, prepared_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		preparation.ID, request.RunID, request.MissionID, request.RootAgentID,
		request.SupervisorAttemptID, request.Turn, request.SelectionID,
		request.ProtocolVersion, request.Profile, request.ModeSnapshotID,
		request.ModeRevision, request.Surface, request.Phase,
		request.SelectionFingerprint, request.ContextFingerprint,
		request.SelectionItemCount, request.ItemCount, request.TokenBudget,
		request.TokenUpperBound, request.RedactionCount, ts(preparation.PreparedAt)); err != nil {
		return skills.RootContextPreparation{}, err
	}
	if err := appendSupervisorEventTx(ctx, tx, run, events.SkillContextPreparedEvent,
		"skills", preparation.ID, rootSkillContextEventPayload(preparation, 0)); err != nil {
		return skills.RootContextPreparation{}, err
	}
	if err := tx.Commit(); err != nil {
		return skills.RootContextPreparation{}, err
	}
	return preparation, nil
}

func validateRootModeSkillContextSelection(ctx context.Context, tx *sql.Tx,
	selection skills.Selection, request skills.RootContextPreparationRequest,
) error {
	if err := selection.Validate(); err != nil {
		return apperror.Wrap(apperror.CodeFailedPrecondition,
			"durable Skill selection is invalid", err)
	}
	mode, err := getCurrentRunModeSnapshot(ctx, tx, selection.RunID)
	if err != nil {
		return err
	}
	if selection.ID != request.SelectionID || selection.RunID != request.RunID ||
		selection.MissionID != request.MissionID || selection.Profile != request.Profile ||
		selection.Fingerprint != request.SelectionFingerprint ||
		selection.ItemCount != request.SelectionItemCount ||
		selection.TokenBudget != request.TokenBudget ||
		request.TokenUpperBound > selection.TokenUpperBound ||
		mode.ID != request.ModeSnapshotID || mode.Revision != request.ModeRevision ||
		mode.Surface != request.Surface || mode.Phase != request.Phase ||
		mode.Profile != request.Profile {
		return apperror.New(apperror.CodeFailedPrecondition,
			"mode-bound root Skill context does not match its selection or Run mode")
	}
	registry, err := skills.BuiltinRegistry()
	if err != nil {
		return apperror.Wrap(apperror.CodeFailedPrecondition,
			"embedded Skill Registry is unavailable", err)
	}
	assembly, err := registry.AssembleContextFor(selection, skills.ExecutionContext{
		Surface: mode.Surface, Phase: mode.Phase, Profile: mode.Profile,
		Role: domain.AgentRoleRoot,
	})
	if err != nil {
		return apperror.Wrap(apperror.CodeFailedPrecondition,
			"mode-bound root Skill context cannot be reconstructed", err)
	}
	expected, err := assembly.PreparationForMode(mode, request.RootAgentID,
		request.SupervisorAttemptID, request.Turn)
	if err != nil {
		return apperror.Wrap(apperror.CodeFailedPrecondition,
			"mode-bound root Skill context request cannot be reconstructed", err)
	}
	if !sameRootSkillContextRequest(expected, request) {
		return apperror.New(apperror.CodeFailedPrecondition,
			"mode-bound root Skill context differs from the embedded Registry")
	}
	return nil
}

func commitRootModeSkillContextTx(ctx context.Context, tx *sql.Tx, run domain.Run,
	checkpoint domain.SupervisorCheckpoint, selection skills.Selection,
	preparation skills.RootContextPreparation, modelAttempt int,
) error {
	if err := validateRootModeSkillContextSelection(ctx, tx, selection,
		preparation.RootContextPreparationRequest); err != nil {
		return err
	}
	root, found, err := getRootAgentTx(ctx, tx, run.ID)
	if err != nil {
		return err
	}
	if !found || root.ID != preparation.RootAgentID || root.Role != domain.AgentRoleRoot ||
		root.Status != domain.AgentRunning || root.ActiveAttemptID != checkpoint.AttemptID ||
		preparation.Turn != checkpoint.NextTurn {
		return apperror.New(apperror.CodeConflict,
			"prepared mode-bound root Skill context is not bound to the active root Agent")
	}
	existing, found, err := getRootModeSkillContextCommitTx(ctx, tx, preparation.ID)
	if err != nil {
		return err
	}
	if found {
		if existing.RunID != run.ID || existing.SupervisorAttemptID != checkpoint.AttemptID ||
			existing.ModelAttempt <= 0 || existing.ModelAttempt > modelAttempt {
			return apperror.New(apperror.CodeConflict,
				"mode-bound root Skill context commit does not match the model attempt")
		}
		return nil
	}
	commit := skills.RootContextCommit{
		PreparationID: preparation.ID, RunID: run.ID,
		SupervisorAttemptID: checkpoint.AttemptID, ModelAttempt: modelAttempt,
		CommittedAt: time.Now().UTC(),
	}
	if err := commit.Validate(); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO root_mode_skill_context_commits
		(preparation_id, run_id, supervisor_attempt_id, model_attempt, committed_at)
		VALUES (?, ?, ?, ?, ?)`, commit.PreparationID, commit.RunID,
		commit.SupervisorAttemptID, commit.ModelAttempt, ts(commit.CommittedAt)); err != nil {
		return err
	}
	return appendSupervisorEventTx(ctx, tx, run, events.SkillContextCommittedEvent,
		"skills", preparation.ID, rootSkillContextEventPayload(preparation,
			commit.ModelAttempt))
}

func acquireSkillContextWriteLockTx(ctx context.Context, tx *sql.Tx, runID string) error {
	result, err := tx.ExecContext(ctx, `UPDATE runs SET updated_at = updated_at WHERE id = ?`, runID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return apperror.New(apperror.CodeNotFound, "root Skill context Run was not found")
	}
	return nil
}

func getRootSkillContextPreparationByAttemptTx(ctx context.Context, tx *sql.Tx,
	runID string, attemptID string,
) (skills.RootContextPreparation, bool, error) {
	item, err := scanRootSkillContextPreparation(tx.QueryRowContext(ctx,
		rootSkillContextPreparationSelect+` WHERE run_id = ? AND supervisor_attempt_id = ?`,
		runID, attemptID))
	if errors.Is(err, sql.ErrNoRows) {
		return skills.RootContextPreparation{}, false, nil
	}
	return item, err == nil, err
}

func getRootSkillContextCommitTx(ctx context.Context, tx *sql.Tx,
	preparationID string,
) (skills.RootContextCommit, bool, error) {
	item, err := scanRootSkillContextCommit(tx.QueryRowContext(ctx,
		rootSkillContextCommitSelect+` WHERE preparation_id = ?`, preparationID))
	if errors.Is(err, sql.ErrNoRows) {
		return skills.RootContextCommit{}, false, nil
	}
	return item, err == nil, err
}

func getRootModeSkillContextPreparationByAttemptTx(ctx context.Context, tx *sql.Tx,
	runID string, attemptID string,
) (skills.RootContextPreparation, bool, error) {
	item, err := scanRootModeSkillContextPreparation(tx.QueryRowContext(ctx,
		rootModeSkillContextPreparationSelect+` WHERE run_id = ? AND supervisor_attempt_id = ?`,
		runID, attemptID))
	if errors.Is(err, sql.ErrNoRows) {
		return skills.RootContextPreparation{}, false, nil
	}
	return item, err == nil, err
}

func getRootModeSkillContextCommitTx(ctx context.Context, tx *sql.Tx,
	preparationID string,
) (skills.RootContextCommit, bool, error) {
	item, err := scanRootSkillContextCommit(tx.QueryRowContext(ctx,
		rootModeSkillContextCommitSelect+` WHERE preparation_id = ?`, preparationID))
	if errors.Is(err, sql.ErrNoRows) {
		return skills.RootContextCommit{}, false, nil
	}
	return item, err == nil, err
}

func scanRootSkillContextPreparation(row scanner) (skills.RootContextPreparation, error) {
	var preparation skills.RootContextPreparation
	var profile string
	var preparedAt string
	request := &preparation.RootContextPreparationRequest
	if err := row.Scan(&preparation.ID, &request.RunID, &request.MissionID,
		&request.RootAgentID, &request.SupervisorAttemptID, &request.Turn,
		&request.SelectionID, &request.ProtocolVersion, &profile,
		&request.SelectionFingerprint, &request.ContextFingerprint,
		&request.ItemCount, &request.TokenBudget, &request.TokenUpperBound,
		&request.RedactionCount, &preparedAt); err != nil {
		return skills.RootContextPreparation{}, err
	}
	request.Profile = domain.Profile(profile)
	preparation.PreparedAt = parseTS(preparedAt)
	return preparation, preparation.Validate()
}

func scanRootModeSkillContextPreparation(row scanner) (skills.RootContextPreparation, error) {
	var preparation skills.RootContextPreparation
	var profile, surface, phase string
	var preparedAt string
	request := &preparation.RootContextPreparationRequest
	if err := row.Scan(&preparation.ID, &request.RunID, &request.MissionID,
		&request.RootAgentID, &request.SupervisorAttemptID, &request.Turn,
		&request.SelectionID, &request.ProtocolVersion, &profile,
		&request.ModeSnapshotID, &request.ModeRevision, &surface, &phase,
		&request.SelectionFingerprint, &request.ContextFingerprint,
		&request.SelectionItemCount, &request.ItemCount, &request.TokenBudget,
		&request.TokenUpperBound, &request.RedactionCount, &preparedAt); err != nil {
		return skills.RootContextPreparation{}, err
	}
	request.Profile = domain.Profile(profile)
	request.Surface = domain.ExecutionSurface(surface)
	request.Phase = domain.ExecutionPhase(phase)
	preparation.PreparedAt = parseTS(preparedAt)
	return preparation, preparation.Validate()
}

func scanRootSkillContextCommit(row scanner) (skills.RootContextCommit, error) {
	var commit skills.RootContextCommit
	var committedAt string
	if err := row.Scan(&commit.PreparationID, &commit.RunID,
		&commit.SupervisorAttemptID, &commit.ModelAttempt, &committedAt); err != nil {
		return skills.RootContextCommit{}, err
	}
	commit.CommittedAt = parseTS(committedAt)
	return commit, commit.Validate()
}

func sameRootSkillContextRequest(left skills.RootContextPreparationRequest,
	right skills.RootContextPreparationRequest,
) bool {
	return left == right
}
