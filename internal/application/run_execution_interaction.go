package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/redact"
	"cyberagent-workbench/internal/runmutation"
)

type RunExecutionInteractionStore interface {
	GetRun(ctx context.Context, id string) (domain.Run, error)
	GetRunMode(ctx context.Context, runID string) (domain.RunModeSnapshot, error)
	GetRunExecutionProfile(ctx context.Context,
		runID string) (domain.RunExecutionProfileSnapshot, error)
	GetRunExecutionInteraction(ctx context.Context,
		runID string) (domain.RunExecutionInteractionSnapshot, error)
	GetRunExecutionInteractionSnapshot(ctx context.Context,
		id string) (domain.RunExecutionInteractionSnapshot, error)
	GetRunExecutionInteractionOperation(ctx context.Context,
		keyDigest string) (domain.RunExecutionInteractionOperation, bool, error)
	TransitionRunExecutionInteraction(ctx context.Context,
		snapshot domain.RunExecutionInteractionSnapshot,
		operation domain.RunExecutionInteractionOperation,
		event events.Event) (domain.RunExecutionInteractionSnapshot, bool, error)
}

type RunExecutionInteractionService struct {
	store RunExecutionInteractionStore
}

type ChangeRunExecutionInteractionRequest struct {
	RunID                    string
	Mode                     string
	Trust                    string
	OperationKey             string
	RequestedBy              string
	Reason                   string
	ConfirmWorkspaceTrust    bool
	ConfirmDebugBoundary     bool
	ConfirmContainerBoundary bool
}

type ChangeRunExecutionInteractionResult struct {
	Interaction domain.RunExecutionInteractionSnapshot
	Replayed    bool
}

func NewRunExecutionInteractionService(
	store RunExecutionInteractionStore,
) *RunExecutionInteractionService {
	return &RunExecutionInteractionService{store: store}
}

func (s *RunExecutionInteractionService) Current(ctx context.Context,
	runID string,
) (domain.RunExecutionInteractionSnapshot, error) {
	if s == nil || s.store == nil {
		return domain.RunExecutionInteractionSnapshot{}, apperror.New(
			apperror.CodeFailedPrecondition,
			"Run execution interaction store is required")
	}
	runID = strings.TrimSpace(runID)
	if !domain.ValidAgentID(runID) || strings.ContainsRune(runID, 0) {
		return domain.RunExecutionInteractionSnapshot{}, apperror.New(
			apperror.CodeInvalidArgument,
			"Run execution interaction Run id is invalid")
	}
	interaction, err := s.store.GetRunExecutionInteraction(ctx, runID)
	return interaction, apperror.Normalize(err)
}

func (s *RunExecutionInteractionService) Change(ctx context.Context,
	request ChangeRunExecutionInteractionRequest,
) (ChangeRunExecutionInteractionResult, error) {
	if s == nil || s.store == nil {
		return ChangeRunExecutionInteractionResult{}, apperror.New(
			apperror.CodeFailedPrecondition,
			"Run execution interaction store is required")
	}
	normalized, targetMode, trust, confirmed, err :=
		normalizeChangeRunExecutionInteractionRequest(request)
	if err != nil {
		return ChangeRunExecutionInteractionResult{}, apperror.Wrap(
			apperror.CodeInvalidArgument, err.Error(), err)
	}
	keyDigest := runmutation.Fingerprint(
		"run_execution_interaction_operation.v1",
		normalized.RunID, normalized.OperationKey)
	if replay, found, err := s.loadReplay(ctx, keyDigest, normalized,
		targetMode, trust, confirmed); err != nil {
		return ChangeRunExecutionInteractionResult{}, err
	} else if found {
		return replay, nil
	}
	run, err := s.store.GetRun(ctx, normalized.RunID)
	if err != nil {
		return ChangeRunExecutionInteractionResult{}, apperror.Normalize(err)
	}
	if !domain.CanChangeRunExecutionInteraction(run.Status) {
		return ChangeRunExecutionInteractionResult{}, apperror.New(
			apperror.CodeFailedPrecondition,
			"Run execution interaction can only change while the Run is created or paused")
	}
	runMode, err := s.store.GetRunMode(ctx, run.ID)
	if err != nil {
		return ChangeRunExecutionInteractionResult{}, apperror.Normalize(err)
	}
	profile, err := s.store.GetRunExecutionProfile(ctx, run.ID)
	if err != nil {
		return ChangeRunExecutionInteractionResult{}, apperror.Normalize(err)
	}
	requestFingerprint := runmutation.Fingerprint(
		"run_execution_interaction_change_request.v1", normalized.RunID,
		string(targetMode), string(runMode.Surface), string(profile.Profile),
		fmt.Sprintf("%d", profile.Revision), string(trust), fmt.Sprintf("%t", confirmed),
		normalized.RequestedBy, normalized.Reason)
	current, err := s.store.GetRunExecutionInteraction(ctx, run.ID)
	if err != nil {
		return ChangeRunExecutionInteractionResult{}, apperror.Normalize(err)
	}
	now := time.Now().UTC()
	if now.Before(current.CreatedAt) {
		now = current.CreatedAt
	}
	next, err := current.Next(idgen.New("run-exec-interaction"), targetMode,
		runMode, profile, trust, confirmed, normalized.RequestedBy,
		normalized.Reason, now)
	if err != nil {
		return ChangeRunExecutionInteractionResult{}, apperror.Wrap(
			apperror.CodeInvalidArgument,
			"Run execution interaction transition is invalid", err)
	}
	operation := domain.RunExecutionInteractionOperation{
		KeyDigest: keyDigest, RequestFingerprint: requestFingerprint,
		SnapshotID: next.ID, RunID: next.RunID, RequestedBy: next.RequestedBy,
		CreatedAt: next.CreatedAt,
	}
	event, err := events.New(next.RunID, next.MissionID,
		events.RunExecutionInteractionSelectedEvent, "run_execution_interaction",
		next.ID, map[string]any{
			"protocol": next.ProtocolVersion, "revision": next.Revision,
			"from": current.Mode, "to": next.Mode, "surface": next.Surface,
			"execution_profile":          next.ExecutionProfile,
			"execution_profile_revision": next.ExecutionProfileRevision,
			"workspace_trust":            next.WorkspaceTrust, "command_form": next.CommandForm,
			"persistent_terminal":  next.PersistentTerminal,
			"user_input_available": next.UserInputAvailable,
			"agent_input_default":  false, "network_scope": next.NetworkScope,
			"required_gate": next.RequiredGate, "policy_version": next.PolicyVersion,
			"operator_confirmed": next.OperatorConfirmed,
			"requested_by":       next.RequestedBy, "reason": next.Reason,
			"process_enabled": false, "execution_authorized": false,
			"capability_grant": false,
		})
	if err != nil {
		return ChangeRunExecutionInteractionResult{}, err
	}
	event.CreatedAt = next.CreatedAt
	stored, replayed, err := s.store.TransitionRunExecutionInteraction(ctx,
		next, operation, event)
	return ChangeRunExecutionInteractionResult{
		Interaction: stored, Replayed: replayed,
	}, apperror.Normalize(err)
}

func (s *RunExecutionInteractionService) loadReplay(ctx context.Context,
	keyDigest string, request ChangeRunExecutionInteractionRequest,
	target domain.RunExecutionInteractionMode, trust domain.WorkspaceTrustLevel,
	confirmed bool,
) (ChangeRunExecutionInteractionResult, bool, error) {
	existing, found, err := s.store.GetRunExecutionInteractionOperation(ctx, keyDigest)
	if err != nil {
		return ChangeRunExecutionInteractionResult{}, false, apperror.Normalize(err)
	}
	if !found {
		return ChangeRunExecutionInteractionResult{}, false, nil
	}
	if existing.RunID != request.RunID ||
		existing.RequestedBy != request.RequestedBy {
		return ChangeRunExecutionInteractionResult{}, true, apperror.New(
			apperror.CodeConflict,
			"Run execution interaction operation key was already used for different intent")
	}
	stored, err := s.store.GetRunExecutionInteractionSnapshot(ctx, existing.SnapshotID)
	if err != nil {
		return ChangeRunExecutionInteractionResult{}, true, apperror.Normalize(err)
	}
	if stored.ID != existing.SnapshotID || stored.RunID != existing.RunID ||
		stored.RequestedBy != existing.RequestedBy ||
		!stored.CreatedAt.Equal(existing.CreatedAt) || stored.Mode != target ||
		stored.WorkspaceTrust != trust ||
		stored.OperatorConfirmed != confirmed ||
		stored.Reason != request.Reason ||
		existing.RequestFingerprint != executionInteractionRequestFingerprint(stored) ||
		stored.AgentInputDefault || stored.ProcessEnabled ||
		stored.ExecutionAuthorized || stored.CapabilityGrant {
		return ChangeRunExecutionInteractionResult{}, true, apperror.New(
			apperror.CodeConflict,
			"Run execution interaction operation key was already used for different intent")
	}
	return ChangeRunExecutionInteractionResult{
		Interaction: stored, Replayed: true,
	}, true, nil
}

func executionInteractionRequestFingerprint(
	snapshot domain.RunExecutionInteractionSnapshot,
) string {
	return runmutation.Fingerprint("run_execution_interaction_change_request.v1",
		snapshot.RunID, string(snapshot.Mode), string(snapshot.Surface),
		string(snapshot.ExecutionProfile),
		fmt.Sprintf("%d", snapshot.ExecutionProfileRevision),
		string(snapshot.WorkspaceTrust), fmt.Sprintf("%t", snapshot.OperatorConfirmed),
		snapshot.RequestedBy, snapshot.Reason)
}

func normalizeChangeRunExecutionInteractionRequest(
	request ChangeRunExecutionInteractionRequest,
) (ChangeRunExecutionInteractionRequest, domain.RunExecutionInteractionMode,
	domain.WorkspaceTrustLevel, bool, error,
) {
	originalKey := request.OperationKey
	request.RunID = strings.TrimSpace(request.RunID)
	request.RequestedBy = strings.TrimSpace(redact.String(request.RequestedBy))
	request.Reason = strings.TrimSpace(redact.String(request.Reason))
	if request.RequestedBy == "" {
		request.RequestedBy = "cli_operator"
	}
	if request.Reason == "" {
		request.Reason = "operator selected execution interaction boundary"
	}
	if !domain.ValidAgentID(request.RunID) || strings.ContainsRune(request.RunID, 0) ||
		!domain.ValidAgentID(request.RequestedBy) ||
		strings.ContainsRune(request.RequestedBy, 0) {
		return ChangeRunExecutionInteractionRequest{}, "", "", false,
			errors.New("bounded Run and operator identities are required")
	}
	switch strings.ToLower(request.RequestedBy) {
	case "agent", "llm", "model", "repository", "repo", "skill":
		return ChangeRunExecutionInteractionRequest{}, "", "", false,
			errors.New("models, agents, Skills, and repository content cannot select execution interaction modes")
	}
	if !utf8.ValidString(request.Reason) || strings.ContainsRune(request.Reason, 0) ||
		utf8.RuneCountInString(request.Reason) >
			domain.MaxRunExecutionInteractionReasonRunes {
		return ChangeRunExecutionInteractionRequest{}, "", "", false,
			errors.New("Run execution interaction reason is invalid or too long")
	}
	if request.OperationKey != strings.TrimSpace(originalKey) ||
		!utf8.ValidString(request.OperationKey) {
		return ChangeRunExecutionInteractionRequest{}, "", "", false,
			errors.New("Run execution interaction operation key must be normalized UTF-8")
	}
	if _, err := domain.NormalizeAgentOperationKey(request.OperationKey); err != nil {
		return ChangeRunExecutionInteractionRequest{}, "", "", false, err
	}
	for _, current := range request.OperationKey {
		if unicode.IsControl(current) || unicode.IsSpace(current) {
			return ChangeRunExecutionInteractionRequest{}, "", "", false,
				errors.New("Run execution interaction operation key cannot contain whitespace or control characters")
		}
	}
	mode, err := domain.ParseRunExecutionInteractionMode(request.Mode)
	if err != nil {
		return ChangeRunExecutionInteractionRequest{}, "", "", false, err
	}
	if request.Trust == "" && mode == domain.RunExecutionInteractionPreview {
		request.Trust = string(domain.WorkspaceTrustUntrusted)
	}
	trust, err := domain.ParseWorkspaceTrustLevel(request.Trust)
	if err != nil {
		return ChangeRunExecutionInteractionRequest{}, "", "", false, err
	}
	confirmed := false
	switch mode {
	case domain.RunExecutionInteractionPreview:
		if trust != domain.WorkspaceTrustUntrusted ||
			request.ConfirmWorkspaceTrust || request.ConfirmDebugBoundary ||
			request.ConfirmContainerBoundary {
			return ChangeRunExecutionInteractionRequest{}, "", "", false,
				errors.New("preview mode must reset to an untrusted, unconfirmed boundary")
		}
	case domain.RunExecutionInteractionControlled:
		if trust != domain.WorkspaceTrustTrusted ||
			!request.ConfirmWorkspaceTrust || request.ConfirmDebugBoundary ||
			request.ConfirmContainerBoundary {
			return ChangeRunExecutionInteractionRequest{}, "", "", false,
				errors.New("controlled mode requires explicit Workspace trust confirmation")
		}
		confirmed = true
	case domain.RunExecutionInteractionDebug:
		if trust != domain.WorkspaceTrustTrusted ||
			!request.ConfirmWorkspaceTrust || !request.ConfirmDebugBoundary ||
			request.ConfirmContainerBoundary {
			return ChangeRunExecutionInteractionRequest{}, "", "", false,
				errors.New("debug mode requires explicit Workspace trust and debug-boundary confirmation")
		}
		confirmed = true
	case domain.RunExecutionInteractionCyber:
		if trust != domain.WorkspaceTrustTrusted ||
			!request.ConfirmWorkspaceTrust || request.ConfirmDebugBoundary ||
			!request.ConfirmContainerBoundary {
			return ChangeRunExecutionInteractionRequest{}, "", "", false,
				errors.New("cyber mode requires explicit Workspace trust and container-boundary confirmation")
		}
		confirmed = true
	}
	request.Mode = string(mode)
	request.Trust = string(trust)
	return request, mode, trust, confirmed, nil
}
