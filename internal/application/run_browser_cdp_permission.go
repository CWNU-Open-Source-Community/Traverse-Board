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

type RunBrowserCDPPermissionStore interface {
	GetRun(ctx context.Context, id string) (domain.Run, error)
	GetRunExecutionPermission(ctx context.Context,
		runID string) (domain.RunExecutionPermissionSnapshot, error)
	GetRunBrowserCDPPermission(ctx context.Context,
		runID string) (domain.RunBrowserCDPPermissionSnapshot, error)
	GetRunBrowserCDPPermissionSnapshot(ctx context.Context,
		id string) (domain.RunBrowserCDPPermissionSnapshot, error)
	GetRunBrowserCDPPermissionOperation(ctx context.Context,
		keyDigest string) (domain.RunBrowserCDPPermissionOperation, bool, error)
	TransitionRunBrowserCDPPermission(ctx context.Context,
		snapshot domain.RunBrowserCDPPermissionSnapshot,
		operation domain.RunBrowserCDPPermissionOperation,
		event events.Event) (domain.RunBrowserCDPPermissionSnapshot, bool, error)
}

type RunBrowserCDPPermissionService struct {
	store        RunBrowserCDPPermissionStore
	capabilities domain.BrowserCDPPermissionRuntimeCapabilities
}

type ChangeRunBrowserCDPPermissionRequest struct {
	RunID               string
	Mode                string
	OperationKey        string
	RequestedBy         string
	Reason              string
	ConfirmFullCDPDebug bool
}

type ChangeRunBrowserCDPPermissionResult struct {
	Permission domain.RunBrowserCDPPermissionSnapshot
	Replayed   bool
}

func NewRunBrowserCDPPermissionService(store RunBrowserCDPPermissionStore,
	capabilities domain.BrowserCDPPermissionRuntimeCapabilities,
) *RunBrowserCDPPermissionService {
	return &RunBrowserCDPPermissionService{store: store, capabilities: capabilities}
}

func (s *RunBrowserCDPPermissionService) RuntimeCapabilities() (
	domain.BrowserCDPPermissionRuntimeCapabilities, error,
) {
	if s == nil {
		return domain.BrowserCDPPermissionRuntimeCapabilities{}, apperror.New(
			apperror.CodeFailedPrecondition,
			"Run browser CDP permission service is required")
	}
	if err := s.capabilities.Validate(); err != nil {
		return domain.BrowserCDPPermissionRuntimeCapabilities{}, apperror.Wrap(
			apperror.CodeFailedPrecondition,
			"Run browser CDP runtime capabilities are invalid", err)
	}
	return s.capabilities, nil
}

func (s *RunBrowserCDPPermissionService) Current(ctx context.Context,
	runID string,
) (domain.RunBrowserCDPPermissionSnapshot, error) {
	if s == nil || s.store == nil {
		return domain.RunBrowserCDPPermissionSnapshot{}, apperror.New(
			apperror.CodeFailedPrecondition,
			"Run browser CDP permission store is required")
	}
	runID = strings.TrimSpace(runID)
	if !domain.ValidAgentID(runID) || strings.ContainsRune(runID, 0) {
		return domain.RunBrowserCDPPermissionSnapshot{}, apperror.New(
			apperror.CodeInvalidArgument,
			"Run browser CDP permission Run id is invalid")
	}
	permission, err := s.store.GetRunBrowserCDPPermission(ctx, runID)
	return permission, apperror.Normalize(err)
}

func (s *RunBrowserCDPPermissionService) Change(ctx context.Context,
	request ChangeRunBrowserCDPPermissionRequest,
) (ChangeRunBrowserCDPPermissionResult, error) {
	if s == nil || s.store == nil {
		return ChangeRunBrowserCDPPermissionResult{}, apperror.New(
			apperror.CodeFailedPrecondition,
			"Run browser CDP permission store is required")
	}
	if err := s.capabilities.Validate(); err != nil {
		return ChangeRunBrowserCDPPermissionResult{}, apperror.Wrap(
			apperror.CodeFailedPrecondition,
			"Run browser CDP runtime capabilities are invalid", err)
	}
	normalized, target, confirmed, err :=
		normalizeChangeRunBrowserCDPPermissionRequest(request)
	if err != nil {
		return ChangeRunBrowserCDPPermissionResult{}, apperror.Wrap(
			apperror.CodeInvalidArgument, err.Error(), err)
	}
	if !s.capabilities.Allows(target) {
		return ChangeRunBrowserCDPPermissionResult{}, apperror.New(
			apperror.CodePolicyDenied,
			fmt.Sprintf(
				"Run browser CDP permission %s is unavailable because this process lacks gate %s",
				target, requiredBrowserCDPPermissionGate(target)))
	}
	keyDigest := runmutation.Fingerprint("run_browser_cdp_permission_operation.v1",
		normalized.RunID, normalized.OperationKey)
	requestFingerprint := runmutation.Fingerprint(
		"run_browser_cdp_permission_change_request.v1", normalized.RunID,
		string(target), fmt.Sprintf("%t", confirmed),
		normalized.RequestedBy, normalized.Reason)
	if replay, found, err := s.loadReplay(ctx, keyDigest, requestFingerprint,
		normalized.RunID, normalized.RequestedBy, target); err != nil {
		return ChangeRunBrowserCDPPermissionResult{}, err
	} else if found {
		return replay, nil
	}
	run, err := s.store.GetRun(ctx, normalized.RunID)
	if err != nil {
		return ChangeRunBrowserCDPPermissionResult{}, apperror.Normalize(err)
	}
	if !domain.CanChangeRunBrowserCDPPermission(run.Status) {
		return ChangeRunBrowserCDPPermissionResult{}, apperror.New(
			apperror.CodeFailedPrecondition,
			"Run browser CDP permission can only change while the Run is created or paused")
	}
	executionPermission, err := s.store.GetRunExecutionPermission(ctx, run.ID)
	if err != nil {
		return ChangeRunBrowserCDPPermissionResult{}, apperror.Normalize(err)
	}
	if target == domain.RunBrowserCDPPermissionFullDebug &&
		executionPermission.Mode != domain.RunExecutionPermissionDebug {
		return ChangeRunBrowserCDPPermissionResult{}, apperror.New(
			apperror.CodePolicyDenied,
			"full CDP debug requires the current Run execution permission to be debug")
	}
	current, err := s.store.GetRunBrowserCDPPermission(ctx, run.ID)
	if err != nil {
		return ChangeRunBrowserCDPPermissionResult{}, apperror.Normalize(err)
	}
	if current.Mode == target {
		return ChangeRunBrowserCDPPermissionResult{}, apperror.New(
			apperror.CodeFailedPrecondition,
			"Run already uses the requested browser CDP permission mode")
	}
	now := time.Now().UTC()
	if now.Before(current.CreatedAt) {
		now = current.CreatedAt
	}
	next, err := current.Next(idgen.New("run-browser-cdp-permission"), target,
		confirmed, normalized.RequestedBy, normalized.Reason, now)
	if err != nil {
		return ChangeRunBrowserCDPPermissionResult{}, apperror.Wrap(
			apperror.CodeInvalidArgument,
			"Run browser CDP permission transition is invalid", err)
	}
	operation := domain.RunBrowserCDPPermissionOperation{
		KeyDigest: keyDigest, RequestFingerprint: requestFingerprint,
		SnapshotID: next.ID, RunID: next.RunID, RequestedBy: next.RequestedBy,
		CreatedAt: next.CreatedAt,
	}
	event, err := events.New(next.RunID, next.MissionID,
		events.RunBrowserCDPPermissionSelectedEvent, "run_browser_cdp_permission",
		next.ID, map[string]any{
			"protocol": next.ProtocolVersion, "revision": next.Revision,
			"from": current.Mode, "to": next.Mode,
			"navigate_allowed":         next.NavigateAllowed,
			"dom_snapshot_allowed":     next.DOMSnapshotAllowed,
			"screenshot_allowed":       next.ScreenshotAllowed,
			"request_capture_allowed":  next.RequestCaptureAllowed,
			"request_mutation_allowed": next.RequestMutationAllowed,
			"request_replay_allowed":   next.RequestReplayAllowed,
			"cookie_access_allowed":    next.CookieAccessAllowed,
			"arbitrary_method_allowed": next.ArbitraryMethodAllowed,
			"risk_tier":                next.RiskTier, "required_gate": next.RequiredGate,
			"policy_version": next.PolicyVersion, "requested_by": next.RequestedBy,
			"reason": next.Reason, "transport_enabled": false,
			"browser_start_authorized": false, "runtime_authorized": false,
			"capability_grant": false,
		})
	if err != nil {
		return ChangeRunBrowserCDPPermissionResult{}, err
	}
	event.CreatedAt = next.CreatedAt
	stored, replayed, err := s.store.TransitionRunBrowserCDPPermission(
		ctx, next, operation, event)
	return ChangeRunBrowserCDPPermissionResult{
		Permission: stored, Replayed: replayed,
	}, apperror.Normalize(err)
}

func (s *RunBrowserCDPPermissionService) loadReplay(ctx context.Context,
	keyDigest string, requestFingerprint string, runID string, requestedBy string,
	target domain.RunBrowserCDPPermissionMode,
) (ChangeRunBrowserCDPPermissionResult, bool, error) {
	existing, found, err := s.store.GetRunBrowserCDPPermissionOperation(ctx, keyDigest)
	if err != nil {
		return ChangeRunBrowserCDPPermissionResult{}, false, apperror.Normalize(err)
	}
	if !found {
		return ChangeRunBrowserCDPPermissionResult{}, false, nil
	}
	if existing.RequestFingerprint != requestFingerprint || existing.RunID != runID ||
		existing.RequestedBy != requestedBy {
		return ChangeRunBrowserCDPPermissionResult{}, true, apperror.New(
			apperror.CodeConflict,
			"Run browser CDP permission operation key was already used for different intent")
	}
	stored, err := s.store.GetRunBrowserCDPPermissionSnapshot(ctx, existing.SnapshotID)
	if err != nil {
		return ChangeRunBrowserCDPPermissionResult{}, true, apperror.Normalize(err)
	}
	if stored.ID != existing.SnapshotID || stored.RunID != existing.RunID ||
		stored.RequestedBy != existing.RequestedBy ||
		!stored.CreatedAt.Equal(existing.CreatedAt) || stored.Mode != target ||
		stored.TransportEnabled || stored.BrowserStartAuthorized ||
		stored.RuntimeAuthorized || stored.CapabilityGrant {
		return ChangeRunBrowserCDPPermissionResult{}, true, apperror.New(
			apperror.CodeInternal,
			"stored Run browser CDP permission operation binding is invalid")
	}
	return ChangeRunBrowserCDPPermissionResult{
		Permission: stored, Replayed: true,
	}, true, nil
}

func normalizeChangeRunBrowserCDPPermissionRequest(
	request ChangeRunBrowserCDPPermissionRequest,
) (ChangeRunBrowserCDPPermissionRequest, domain.RunBrowserCDPPermissionMode, bool, error) {
	originalKey := request.OperationKey
	request.RunID = strings.TrimSpace(request.RunID)
	request.RequestedBy = strings.TrimSpace(redact.String(request.RequestedBy))
	request.Reason = strings.TrimSpace(redact.String(request.Reason))
	if request.RequestedBy == "" {
		request.RequestedBy = "cli_operator"
	}
	if request.Reason == "" {
		request.Reason = "operator selected browser CDP permission mode"
	}
	if !domain.ValidAgentID(request.RunID) || strings.ContainsRune(request.RunID, 0) ||
		!domain.ValidAgentID(request.RequestedBy) || strings.ContainsRune(request.RequestedBy, 0) {
		return ChangeRunBrowserCDPPermissionRequest{}, "", false,
			errors.New("bounded Run and operator identities are required")
	}
	switch strings.ToLower(request.RequestedBy) {
	case "agent", "llm", "model", "repository", "repo", "skill", "browser",
		"document", "tool":
		return ChangeRunBrowserCDPPermissionRequest{}, "", false,
			errors.New(
				"models, agents, tools, Skills, browsers, documents, and repository content cannot select browser CDP permission modes")
	}
	if !utf8.ValidString(request.Reason) || strings.ContainsRune(request.Reason, 0) ||
		utf8.RuneCountInString(request.Reason) >
			domain.MaxRunBrowserCDPPermissionReasonRunes {
		return ChangeRunBrowserCDPPermissionRequest{}, "", false,
			errors.New("Run browser CDP permission reason is invalid or too long")
	}
	if request.OperationKey != strings.TrimSpace(originalKey) ||
		!utf8.ValidString(request.OperationKey) {
		return ChangeRunBrowserCDPPermissionRequest{}, "", false,
			errors.New("Run browser CDP permission operation key must be normalized UTF-8")
	}
	if _, err := domain.NormalizeAgentOperationKey(request.OperationKey); err != nil {
		return ChangeRunBrowserCDPPermissionRequest{}, "", false, err
	}
	for _, current := range request.OperationKey {
		if unicode.IsControl(current) || unicode.IsSpace(current) {
			return ChangeRunBrowserCDPPermissionRequest{}, "", false,
				errors.New(
					"Run browser CDP permission operation key cannot contain whitespace or control characters")
		}
	}
	mode, err := domain.ParseRunBrowserCDPPermissionMode(request.Mode)
	if err != nil {
		return ChangeRunBrowserCDPPermissionRequest{}, "", false, err
	}
	confirmed := false
	switch mode {
	case domain.RunBrowserCDPPermissionRestricted:
		if request.ConfirmFullCDPDebug {
			return ChangeRunBrowserCDPPermissionRequest{}, "", false,
				errors.New("restricted CDP mode must reset to an unconfirmed boundary")
		}
	case domain.RunBrowserCDPPermissionFullDebug:
		if !request.ConfirmFullCDPDebug {
			return ChangeRunBrowserCDPPermissionRequest{}, "", false,
				errors.New("full CDP debug requires its exact highly-sensitive confirmation")
		}
		confirmed = true
	}
	request.Mode = string(mode)
	return request, mode, confirmed, nil
}

func requiredBrowserCDPPermissionGate(
	mode domain.RunBrowserCDPPermissionMode,
) domain.BrowserCDPPermissionGate {
	switch mode {
	case domain.RunBrowserCDPPermissionRestricted:
		return domain.BrowserCDPPermissionGateRestricted
	case domain.RunBrowserCDPPermissionFullDebug:
		return domain.BrowserCDPPermissionGateFullDebug
	default:
		return ""
	}
}
