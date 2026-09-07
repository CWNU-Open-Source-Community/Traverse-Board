package application

import (
	"context"
	"errors"
	"sort"
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
	"cyberagent-workbench/internal/webevidence"
)

const RunNetworkAuthorityControlProtocolVersion = "run_network_authority_control.v1"

type RunNetworkAuthorityStore interface {
	GetRun(context.Context, string) (domain.Run, error)
	GetRunMode(context.Context, string) (domain.RunModeSnapshot, error)
	GetRunModeSnapshot(context.Context, string) (domain.RunModeSnapshot, error)
	GetRunNetworkAuthorityOperation(context.Context, string) (
		domain.RunNetworkAuthorityOperation, bool, error)
	TransitionRunNetworkAuthority(context.Context, domain.RunModeSnapshot,
		domain.RunNetworkAuthorityOperation, events.Event) (
		domain.RunModeSnapshot, bool, error)
}

type RunNetworkAuthorityService struct {
	store            RunNetworkAuthorityStore
	runtimeAuthority *domain.ExecutionPermissionRuntimeAuthority
}

type ExpandRunNetworkAuthorityRequest struct {
	Version              string
	RunID                string
	ExpectedModeRevision int64
	AddAllowedTargets    []string
	OperationKey         string
	RequestedBy          string
	Reason               string
}

type ExpandRunNetworkAuthorityResult struct {
	Mode         domain.RunModeSnapshot
	AddedTargets []string
	Replayed     bool
}

func NewRunNetworkAuthorityService(store RunNetworkAuthorityStore) *RunNetworkAuthorityService {
	return &RunNetworkAuthorityService{store: store}
}

func (s *RunNetworkAuthorityService) WithRuntimeAuthority(
	authority *domain.ExecutionPermissionRuntimeAuthority,
) *RunNetworkAuthorityService {
	if s != nil {
		s.runtimeAuthority = authority
	}
	return s
}

func (s *RunNetworkAuthorityService) Expand(ctx context.Context,
	request ExpandRunNetworkAuthorityRequest,
) (ExpandRunNetworkAuthorityResult, error) {
	if s == nil || s.store == nil {
		return ExpandRunNetworkAuthorityResult{}, apperror.New(
			apperror.CodeFailedPrecondition, "Run network authority store is required")
	}
	if s.runtimeAuthority == nil {
		return ExpandRunNetworkAuthorityResult{}, apperror.New(
			apperror.CodeFailedPrecondition,
			"Run network authority requires the process-local tool fence authority")
	}
	normalized, addedTargets, err := normalizeExpandRunNetworkAuthorityRequest(request)
	if err != nil {
		return ExpandRunNetworkAuthorityResult{}, apperror.Wrap(
			apperror.CodeInvalidArgument, err.Error(), err)
	}
	keyDigest := runmutation.RunNetworkAuthorityOperationDigest(
		normalized.RunID, normalized.OperationKey)
	requestFingerprint := runmutation.RunNetworkAuthorityRequestFingerprint(
		normalized.RunID, normalized.ExpectedModeRevision, addedTargets,
		normalized.RequestedBy, normalized.Reason)
	if replay, found, replayErr := s.loadReplay(ctx, keyDigest, requestFingerprint,
		normalized, addedTargets); replayErr != nil {
		return ExpandRunNetworkAuthorityResult{}, replayErr
	} else if found {
		return replay, nil
	}

	run, err := s.store.GetRun(ctx, normalized.RunID)
	if err != nil {
		return ExpandRunNetworkAuthorityResult{}, apperror.Normalize(err)
	}
	if !domain.CanExpandRunNetworkAuthority(run.Status) {
		return ExpandRunNetworkAuthorityResult{}, apperror.New(apperror.CodeConflict,
			"Run network authority can only expand while the Run is created or paused; current status is "+
				string(run.Status))
	}
	current, err := s.store.GetRunMode(ctx, run.ID)
	if err != nil {
		return ExpandRunNetworkAuthorityResult{}, apperror.Normalize(err)
	}
	if current.Revision != normalized.ExpectedModeRevision {
		return ExpandRunNetworkAuthorityResult{}, apperror.New(apperror.CodeConflict,
			"Run mode revision changed before network authority expansion")
	}
	currentTargets, err := canonicalCurrentRunNetworkTargets(current.Scope)
	if err != nil {
		return ExpandRunNetworkAuthorityResult{}, apperror.Wrap(apperror.CodeConflict,
			"current Run network authority is not an exact-host scope", err)
	}
	union, genuinelyAdded, err := unionExactTargets(currentTargets, addedTargets)
	if err != nil {
		return ExpandRunNetworkAuthorityResult{}, err
	}
	if s.runtimeAuthority != nil {
		// Fence before persistence so no tool minted against the old scope can
		// overlap the authority transition. A failed durable transition may
		// invalidate work unnecessarily, but it can never widen stale authority.
		if _, err := s.runtimeAuthority.RotateRunAuthorizationFence(run.ID); err != nil {
			return ExpandRunNetworkAuthorityResult{}, apperror.Wrap(
				apperror.CodeFailedPrecondition,
				"Run network authority could not fence its previous tool generation", err)
		}
	}

	now := time.Now().UTC()
	if now.Before(current.CreatedAt) {
		now = current.CreatedAt
	}
	next, err := current.NextNetworkAuthority(idgen.New("run-network-authority"), union,
		normalized.RequestedBy, normalized.Reason, now)
	if err != nil {
		return ExpandRunNetworkAuthorityResult{}, apperror.Wrap(
			apperror.CodeInvalidArgument, "Run network authority transition is invalid", err)
	}
	operation := domain.RunNetworkAuthorityOperation{
		KeyDigest: keyDigest, RequestFingerprint: requestFingerprint,
		SnapshotID: next.ID, RunID: next.RunID,
		ExpectedModeRevision: normalized.ExpectedModeRevision,
		RequestedBy:          next.RequestedBy, CreatedAt: next.CreatedAt,
	}
	event, err := events.New(next.RunID, next.MissionID,
		events.RunNetworkAuthorityExpandedEvent, "run_network_authority", next.ID,
		map[string]any{
			"protocol":               domain.RunNetworkAuthorityProtocolVersion,
			"revision":               next.Revision,
			"expected_mode_revision": normalized.ExpectedModeRevision,
			"from_network_mode":      current.Scope.NetworkMode,
			"to_network_mode":        next.Scope.NetworkMode,
			"previous_target_count":  len(currentTargets),
			"added_targets":          genuinelyAdded,
			"added_target_count":     len(genuinelyAdded),
			"allowed_target_count":   len(next.Scope.AllowedTargets),
			"requested_by":           next.RequestedBy, "reason": next.Reason,
			"capability_grant": true,
		})
	if err != nil {
		return ExpandRunNetworkAuthorityResult{}, err
	}
	event.CreatedAt = next.CreatedAt
	stored, replayed, err := s.store.TransitionRunNetworkAuthority(ctx, next,
		operation, event)
	if err != nil {
		return ExpandRunNetworkAuthorityResult{}, apperror.Normalize(err)
	}
	if !replayed && s.runtimeAuthority != nil {
		// Fence again after commit to close the race where a child capability
		// was issued after the pre-transition fence but before SQLite committed.
		if _, err := s.runtimeAuthority.RotateRunAuthorizationFence(stored.RunID); err != nil {
			return ExpandRunNetworkAuthorityResult{}, apperror.Wrap(apperror.CodeInternal,
				"Run network authority changed but its runtime fence could not rotate", err)
		}
	}
	return ExpandRunNetworkAuthorityResult{Mode: stored,
		AddedTargets: append([]string(nil), genuinelyAdded...), Replayed: replayed}, nil
}

func (s *RunNetworkAuthorityService) loadReplay(ctx context.Context,
	keyDigest string, requestFingerprint string,
	request ExpandRunNetworkAuthorityRequest, addedTargets []string,
) (ExpandRunNetworkAuthorityResult, bool, error) {
	existing, found, err := s.store.GetRunNetworkAuthorityOperation(ctx, keyDigest)
	if err != nil {
		return ExpandRunNetworkAuthorityResult{}, false, apperror.Normalize(err)
	}
	if !found {
		return ExpandRunNetworkAuthorityResult{}, false, nil
	}
	if existing.RequestFingerprint != requestFingerprint ||
		existing.RunID != request.RunID ||
		existing.ExpectedModeRevision != request.ExpectedModeRevision ||
		existing.RequestedBy != request.RequestedBy {
		return ExpandRunNetworkAuthorityResult{}, true, apperror.New(
			apperror.CodeConflict,
			"Run network authority operation key was already used for different intent")
	}
	stored, err := s.store.GetRunModeSnapshot(ctx, existing.SnapshotID)
	if err != nil {
		return ExpandRunNetworkAuthorityResult{}, true, apperror.Normalize(err)
	}
	if stored.ID != existing.SnapshotID || stored.RunID != existing.RunID ||
		stored.Revision != existing.ExpectedModeRevision+1 ||
		stored.RequestedBy != existing.RequestedBy ||
		!stored.CreatedAt.Equal(existing.CreatedAt) || stored.Scope.NetworkMode != "allowlist" {
		return ExpandRunNetworkAuthorityResult{}, true, apperror.New(
			apperror.CodeInternal, "stored Run network authority operation binding is invalid")
	}
	for _, target := range addedTargets {
		if !containsExactTarget(stored.Scope.AllowedTargets, target) {
			return ExpandRunNetworkAuthorityResult{}, true, apperror.New(
				apperror.CodeInternal, "stored Run network authority omitted an authorized target")
		}
	}
	return ExpandRunNetworkAuthorityResult{Mode: stored,
		AddedTargets: append([]string(nil), addedTargets...), Replayed: true}, true, nil
}

func normalizeExpandRunNetworkAuthorityRequest(request ExpandRunNetworkAuthorityRequest) (
	ExpandRunNetworkAuthorityRequest, []string, error,
) {
	originalKey := request.OperationKey
	request.Version = strings.TrimSpace(request.Version)
	request.RunID = strings.TrimSpace(request.RunID)
	request.RequestedBy = strings.TrimSpace(redact.String(request.RequestedBy))
	request.Reason = strings.TrimSpace(redact.String(request.Reason))
	if request.Version != RunNetworkAuthorityControlProtocolVersion {
		return ExpandRunNetworkAuthorityRequest{}, nil, errors.New(
			"unsupported Run network authority control version")
	}
	if request.RequestedBy == "" {
		request.RequestedBy = "cli_operator"
	}
	if request.Reason == "" {
		request.Reason = "operator added exact HTTPS targets"
	}
	if !domain.ValidAgentID(request.RunID) || strings.ContainsRune(request.RunID, 0) ||
		!domain.ValidAgentID(request.RequestedBy) || strings.ContainsRune(request.RequestedBy, 0) {
		return ExpandRunNetworkAuthorityRequest{}, nil, errors.New(
			"bounded Run and operator identities are required")
	}
	if request.ExpectedModeRevision <= 0 {
		return ExpandRunNetworkAuthorityRequest{}, nil, errors.New(
			"expected mode revision must be positive")
	}
	if !utf8.ValidString(request.Reason) || strings.ContainsRune(request.Reason, 0) ||
		utf8.RuneCountInString(request.Reason) > domain.MaxRunModeReasonRunes {
		return ExpandRunNetworkAuthorityRequest{}, nil, errors.New(
			"Run network authority reason is invalid or too long")
	}
	if request.OperationKey != strings.TrimSpace(originalKey) ||
		!utf8.ValidString(request.OperationKey) {
		return ExpandRunNetworkAuthorityRequest{}, nil, errors.New(
			"Run network authority operation key must be normalized UTF-8")
	}
	if _, err := domain.NormalizeAgentOperationKey(request.OperationKey); err != nil {
		return ExpandRunNetworkAuthorityRequest{}, nil, err
	}
	for _, current := range request.OperationKey {
		if unicode.IsControl(current) || unicode.IsSpace(current) {
			return ExpandRunNetworkAuthorityRequest{}, nil, errors.New(
				"Run network authority operation key cannot contain whitespace or control characters")
		}
	}
	targets, err := webevidence.NormalizeExactAuthorityTargets(request.AddAllowedTargets)
	if err != nil {
		return ExpandRunNetworkAuthorityRequest{}, nil, err
	}
	request.AddAllowedTargets = append([]string(nil), targets...)
	return request, targets, nil
}

func canonicalCurrentRunNetworkTargets(scope domain.Scope) ([]string, error) {
	switch scope.NetworkMode {
	case "disabled":
		if len(scope.AllowedTargets) != 0 {
			return nil, errors.New("disabled scope retained targets")
		}
		return nil, nil
	case "allowlist":
		targets, err := webevidence.NormalizeExactAuthorityTargets(scope.AllowedTargets)
		if err != nil {
			return nil, err
		}
		if len(targets) != len(scope.AllowedTargets) {
			return nil, errors.New("current allowlist is not canonical and unique")
		}
		for index := range targets {
			if targets[index] != scope.AllowedTargets[index] {
				return nil, errors.New("current allowlist is not canonical and sorted")
			}
		}
		return targets, nil
	default:
		return nil, errors.New("current network mode is unsupported")
	}
}

// successorRunNetworkScope carries an exact-host network preference into the
// next Run. Historical broad or otherwise non-canonical scopes are deliberately
// reset instead of being copied into a newly created Run.
func successorRunNetworkScope(scope domain.Scope) (domain.Scope, bool) {
	if _, err := canonicalCurrentRunNetworkTargets(scope); err == nil {
		return domain.CloneScope(scope), false
	}
	reset := domain.CloneScope(scope)
	reset.NetworkMode = "disabled"
	reset.AllowedTargets = nil
	return reset, true
}

func unionExactTargets(current []string, requested []string) ([]string, []string, error) {
	seen := make(map[string]struct{}, len(current)+len(requested))
	union := append([]string(nil), current...)
	for _, target := range current {
		seen[target] = struct{}{}
	}
	added := make([]string, 0, len(requested))
	for _, target := range requested {
		if _, exists := seen[target]; exists {
			return nil, nil, apperror.New(apperror.CodeFailedPrecondition,
				"a requested HTTPS target is already allowed")
		}
		seen[target] = struct{}{}
		union = append(union, target)
		added = append(added, target)
	}
	if len(union) > 256 {
		return nil, nil, apperror.New(apperror.CodeResourceExhausted,
			"Run network authority cannot exceed 256 exact HTTPS targets")
	}
	sort.Strings(union)
	sort.Strings(added)
	return union, added, nil
}

func containsExactTarget(targets []string, expected string) bool {
	index := sort.SearchStrings(targets, expected)
	return index < len(targets) && targets[index] == expected
}
