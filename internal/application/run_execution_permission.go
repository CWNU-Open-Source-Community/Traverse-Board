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

type RunExecutionPermissionStore interface {
	GetRun(ctx context.Context, id string) (domain.Run, error)
	GetRunExecutionPermission(ctx context.Context,
		runID string) (domain.RunExecutionPermissionSnapshot, error)
	GetRunExecutionPermissionSnapshot(ctx context.Context,
		id string) (domain.RunExecutionPermissionSnapshot, error)
	GetRunExecutionPermissionOperation(ctx context.Context,
		keyDigest string) (domain.RunExecutionPermissionOperation, bool, error)
	TransitionRunExecutionPermission(ctx context.Context,
		snapshot domain.RunExecutionPermissionSnapshot,
		operation domain.RunExecutionPermissionOperation,
		event events.Event) (domain.RunExecutionPermissionSnapshot, bool, error)
}

type RunExecutionPermissionService struct {
	store        RunExecutionPermissionStore
	capabilities domain.ExecutionPermissionRuntimeCapabilities
}

type ChangeRunExecutionPermissionRequest struct {
	RunID                   string
	Mode                    string
	OperationKey            string
	RequestedBy             string
	Reason                  string
	ConfirmUserApproval     bool
	ConfirmDangerFullAccess bool
	ConfirmDebugAccess      bool
}

type ChangeRunExecutionPermissionResult struct {
	Permission domain.RunExecutionPermissionSnapshot
	Replayed   bool
}

func NewRunExecutionPermissionService(store RunExecutionPermissionStore,
	capabilities domain.ExecutionPermissionRuntimeCapabilities,
) *RunExecutionPermissionService {
	return &RunExecutionPermissionService{store: store, capabilities: capabilities}
}

func (s *RunExecutionPermissionService) RuntimeCapabilities() (
	domain.ExecutionPermissionRuntimeCapabilities, error,
) {
	if s == nil {
		return domain.ExecutionPermissionRuntimeCapabilities{},
			apperror.New(apperror.CodeFailedPrecondition,
				"Run execution permission service is required")
	}
	if err := s.capabilities.Validate(); err != nil {
		return domain.ExecutionPermissionRuntimeCapabilities{},
			apperror.Wrap(apperror.CodeFailedPrecondition,
				"Run execution permission runtime capabilities are invalid", err)
	}
	return s.capabilities, nil
}

func (s *RunExecutionPermissionService) Current(ctx context.Context,
	runID string,
) (domain.RunExecutionPermissionSnapshot, error) {
	if s == nil || s.store == nil {
		return domain.RunExecutionPermissionSnapshot{}, apperror.New(
			apperror.CodeFailedPrecondition, "Run execution permission store is required")
	}
	runID = strings.TrimSpace(runID)
	if !domain.ValidAgentID(runID) || strings.ContainsRune(runID, 0) {
		return domain.RunExecutionPermissionSnapshot{}, apperror.New(
			apperror.CodeInvalidArgument, "Run execution permission Run id is invalid")
	}
	permission, err := s.store.GetRunExecutionPermission(ctx, runID)
	return permission, apperror.Normalize(err)
}

func (s *RunExecutionPermissionService) Change(ctx context.Context,
	request ChangeRunExecutionPermissionRequest,
) (ChangeRunExecutionPermissionResult, error) {
	if s == nil || s.store == nil {
		return ChangeRunExecutionPermissionResult{}, apperror.New(
			apperror.CodeFailedPrecondition, "Run execution permission store is required")
	}
	if err := s.capabilities.Validate(); err != nil {
		return ChangeRunExecutionPermissionResult{}, apperror.Wrap(
			apperror.CodeFailedPrecondition,
			"Run execution permission runtime capabilities are invalid", err)
	}
	normalized, target, confirmed, err :=
		normalizeChangeRunExecutionPermissionRequest(request)
	if err != nil {
		return ChangeRunExecutionPermissionResult{}, apperror.Wrap(
			apperror.CodeInvalidArgument, err.Error(), err)
	}
	if !s.capabilities.Allows(target) {
		return ChangeRunExecutionPermissionResult{}, apperror.New(
			apperror.CodePolicyDenied,
			fmt.Sprintf(
				"Run execution permission %s is unavailable because this process lacks gate %s",
				target, requiredExecutionPermissionGate(target)))
	}
	keyDigest := runmutation.Fingerprint("run_execution_permission_operation.v1",
		normalized.RunID, normalized.OperationKey)
	requestFingerprint := runmutation.Fingerprint(
		"run_execution_permission_change_request.v1", normalized.RunID,
		string(target), fmt.Sprintf("%t", confirmed),
		normalized.RequestedBy, normalized.Reason)
	if replay, found, err := s.loadReplay(ctx, keyDigest, requestFingerprint,
		normalized.RunID, normalized.RequestedBy, target); err != nil {
		return ChangeRunExecutionPermissionResult{}, err
	} else if found {
		return replay, nil
	}
	run, err := s.store.GetRun(ctx, normalized.RunID)
	if err != nil {
		return ChangeRunExecutionPermissionResult{}, apperror.Normalize(err)
	}
	if !domain.CanChangeRunExecutionPermission(run.Status) {
		return ChangeRunExecutionPermissionResult{}, apperror.New(
			apperror.CodeFailedPrecondition,
			"Run execution permission can only change while the Run is created or paused")
	}
	current, err := s.store.GetRunExecutionPermission(ctx, run.ID)
	if err != nil {
		return ChangeRunExecutionPermissionResult{}, apperror.Normalize(err)
	}
	if current.Mode == target {
		return ChangeRunExecutionPermissionResult{}, apperror.New(
			apperror.CodeFailedPrecondition,
			"Run already uses the requested execution permission mode")
	}
	now := time.Now().UTC()
	if now.Before(current.CreatedAt) {
		now = current.CreatedAt
	}
	next, err := current.Next(idgen.New("run-exec-permission"), target, confirmed,
		normalized.RequestedBy, normalized.Reason, now)
	if err != nil {
		return ChangeRunExecutionPermissionResult{}, apperror.Wrap(
			apperror.CodeInvalidArgument, "Run execution permission transition is invalid", err)
	}
	operation := domain.RunExecutionPermissionOperation{
		KeyDigest: keyDigest, RequestFingerprint: requestFingerprint,
		SnapshotID: next.ID, RunID: next.RunID, RequestedBy: next.RequestedBy,
		CreatedAt: next.CreatedAt,
	}
	event, err := events.New(next.RunID, next.MissionID,
		events.RunExecutionPermissionSelectedEvent, "run_execution_permission", next.ID,
		map[string]any{
			"protocol": next.ProtocolVersion, "revision": next.Revision,
			"from": current.Mode, "to": next.Mode,
			"approval_policy": next.ApprovalPolicy, "command_scope": next.CommandScope,
			"filesystem_scope": next.FilesystemScope, "network_scope": next.NetworkScope,
			"persistent_terminal":  next.PersistentTerminal,
			"background_process":   next.BackgroundProcess,
			"agent_terminal_input": next.AgentTerminalInput,
			"risk_tier":            next.RiskTier, "required_gate": next.RequiredGate,
			"policy_version": next.PolicyVersion, "requested_by": next.RequestedBy,
			"reason": next.Reason, "process_enabled": false,
			"execution_authorized": false, "capability_grant": false,
		})
	if err != nil {
		return ChangeRunExecutionPermissionResult{}, err
	}
	event.CreatedAt = next.CreatedAt
	stored, replayed, err := s.store.TransitionRunExecutionPermission(
		ctx, next, operation, event)
	return ChangeRunExecutionPermissionResult{
		Permission: stored, Replayed: replayed,
	}, apperror.Normalize(err)
}

func (s *RunExecutionPermissionService) loadReplay(ctx context.Context,
	keyDigest string, requestFingerprint string, runID string, requestedBy string,
	target domain.RunExecutionPermissionMode,
) (ChangeRunExecutionPermissionResult, bool, error) {
	existing, found, err := s.store.GetRunExecutionPermissionOperation(ctx, keyDigest)
	if err != nil {
		return ChangeRunExecutionPermissionResult{}, false, apperror.Normalize(err)
	}
	if !found {
		return ChangeRunExecutionPermissionResult{}, false, nil
	}
	if existing.RequestFingerprint != requestFingerprint || existing.RunID != runID ||
		existing.RequestedBy != requestedBy {
		return ChangeRunExecutionPermissionResult{}, true, apperror.New(
			apperror.CodeConflict,
			"Run execution permission operation key was already used for different intent")
	}
	stored, err := s.store.GetRunExecutionPermissionSnapshot(ctx, existing.SnapshotID)
	if err != nil {
		return ChangeRunExecutionPermissionResult{}, true, apperror.Normalize(err)
	}
	if stored.ID != existing.SnapshotID || stored.RunID != existing.RunID ||
		stored.RequestedBy != existing.RequestedBy ||
		!stored.CreatedAt.Equal(existing.CreatedAt) || stored.Mode != target ||
		stored.ProcessEnabled || stored.ExecutionAuthorized || stored.CapabilityGrant {
		return ChangeRunExecutionPermissionResult{}, true, apperror.New(
			apperror.CodeInternal,
			"stored Run execution permission operation binding is invalid")
	}
	return ChangeRunExecutionPermissionResult{
		Permission: stored, Replayed: true,
	}, true, nil
}

func normalizeChangeRunExecutionPermissionRequest(
	request ChangeRunExecutionPermissionRequest,
) (ChangeRunExecutionPermissionRequest, domain.RunExecutionPermissionMode, bool, error) {
	originalKey := request.OperationKey
	request.RunID = strings.TrimSpace(request.RunID)
	request.RequestedBy = strings.TrimSpace(redact.String(request.RequestedBy))
	request.Reason = strings.TrimSpace(redact.String(request.Reason))
	if request.RequestedBy == "" {
		request.RequestedBy = "cli_operator"
	}
	if request.Reason == "" {
		request.Reason = "operator selected execution permission mode"
	}
	if !domain.ValidAgentID(request.RunID) || strings.ContainsRune(request.RunID, 0) ||
		!domain.ValidAgentID(request.RequestedBy) ||
		strings.ContainsRune(request.RequestedBy, 0) {
		return ChangeRunExecutionPermissionRequest{}, "", false,
			errors.New("bounded Run and operator identities are required")
	}
	switch strings.ToLower(request.RequestedBy) {
	case "agent", "llm", "model", "repository", "repo", "skill":
		return ChangeRunExecutionPermissionRequest{}, "", false,
			errors.New(
				"models, agents, Skills, and repository content cannot select execution permission modes")
	}
	if !utf8.ValidString(request.Reason) || strings.ContainsRune(request.Reason, 0) ||
		utf8.RuneCountInString(request.Reason) >
			domain.MaxRunExecutionPermissionReasonRunes {
		return ChangeRunExecutionPermissionRequest{}, "", false,
			errors.New("Run execution permission reason is invalid or too long")
	}
	if request.OperationKey != strings.TrimSpace(originalKey) ||
		!utf8.ValidString(request.OperationKey) {
		return ChangeRunExecutionPermissionRequest{}, "", false,
			errors.New("Run execution permission operation key must be normalized UTF-8")
	}
	if _, err := domain.NormalizeAgentOperationKey(request.OperationKey); err != nil {
		return ChangeRunExecutionPermissionRequest{}, "", false, err
	}
	for _, current := range request.OperationKey {
		if unicode.IsControl(current) || unicode.IsSpace(current) {
			return ChangeRunExecutionPermissionRequest{}, "", false,
				errors.New(
					"Run execution permission operation key cannot contain whitespace or control characters")
		}
	}
	mode, err := domain.ParseRunExecutionPermissionMode(request.Mode)
	if err != nil {
		return ChangeRunExecutionPermissionRequest{}, "", false, err
	}
	confirmed := false
	switch mode {
	case domain.RunExecutionPermissionConservative:
		if request.ConfirmUserApproval || request.ConfirmDangerFullAccess ||
			request.ConfirmDebugAccess {
			return ChangeRunExecutionPermissionRequest{}, "", false,
				errors.New("conservative mode must reset to an unconfirmed boundary")
		}
	case domain.RunExecutionPermissionApproval:
		if !request.ConfirmUserApproval || request.ConfirmDangerFullAccess ||
			request.ConfirmDebugAccess {
			return ChangeRunExecutionPermissionRequest{}, "", false,
				errors.New("approval mode requires its exact user-approval confirmation")
		}
		confirmed = true
	case domain.RunExecutionPermissionFullAccess:
		if request.ConfirmUserApproval || !request.ConfirmDangerFullAccess ||
			request.ConfirmDebugAccess {
			return ChangeRunExecutionPermissionRequest{}, "", false,
				errors.New("full-access mode requires its exact danger-full-access confirmation")
		}
		confirmed = true
	case domain.RunExecutionPermissionDebug:
		if request.ConfirmUserApproval || request.ConfirmDangerFullAccess ||
			!request.ConfirmDebugAccess {
			return ChangeRunExecutionPermissionRequest{}, "", false,
				errors.New("debug mode requires its exact maximum-access confirmation")
		}
		confirmed = true
	}
	request.Mode = string(mode)
	return request, mode, confirmed, nil
}

func requiredExecutionPermissionGate(
	mode domain.RunExecutionPermissionMode,
) domain.ExecutionPermissionGate {
	switch mode {
	case domain.RunExecutionPermissionConservative:
		return domain.ExecutionPermissionGateConservative
	case domain.RunExecutionPermissionApproval:
		return domain.ExecutionPermissionGateOperatorApproval
	case domain.RunExecutionPermissionFullAccess:
		return domain.ExecutionPermissionGateDangerFullAccess
	case domain.RunExecutionPermissionDebug:
		return domain.ExecutionPermissionGateDebugMaximumAccess
	default:
		return ""
	}
}
