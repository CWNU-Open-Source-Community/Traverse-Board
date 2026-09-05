package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const RunNetworkAuthorityProtocolVersion = "run_network_authority.v1"

// RunNetworkAuthorityOperation is the append-only idempotency binding for one
// explicit exact-host expansion. The expected revision is part of the intent,
// so replay cannot silently apply the same key to a later mode snapshot.
type RunNetworkAuthorityOperation struct {
	KeyDigest            string
	RequestFingerprint   string
	SnapshotID           string
	RunID                string
	ExpectedModeRevision int64
	RequestedBy          string
	CreatedAt            time.Time
}

func (o RunNetworkAuthorityOperation) Validate() error {
	if !validLowerHexDigest(o.KeyDigest) || !validLowerHexDigest(o.RequestFingerprint) {
		return errors.New("Run network authority operation digests must be lowercase SHA-256")
	}
	for label, value := range map[string]string{
		"snapshot id": o.SnapshotID, "Run id": o.RunID, "requester": o.RequestedBy,
	} {
		if !ValidAgentID(value) || strings.ContainsRune(value, 0) {
			return fmt.Errorf("Run network authority operation %s is invalid", label)
		}
	}
	if o.ExpectedModeRevision <= 0 {
		return errors.New("Run network authority expected mode revision must be positive")
	}
	if o.CreatedAt.IsZero() {
		return errors.New("Run network authority operation creation time is required")
	}
	return nil
}

// NextNetworkAuthority creates the sole scope-changing Run mode transition.
// allowedTargets is the complete canonical post-transition set, not a
// replacement supplied by an untrusted caller. It must be a strict superset of
// the current exact allowlist and can never become public or wildcard authority.
func (s RunModeSnapshot) NextNetworkAuthority(id string, allowedTargets []string,
	requestedBy string, reason string, at time.Time,
) (RunModeSnapshot, error) {
	if err := s.Validate(); err != nil {
		return RunModeSnapshot{}, err
	}
	if s.Scope.NetworkMode == "disabled" && len(s.Scope.AllowedTargets) != 0 {
		return RunModeSnapshot{}, errors.New("disabled Run network authority retained targets")
	}
	if s.Scope.NetworkMode != "disabled" && s.Scope.NetworkMode != "allowlist" {
		return RunModeSnapshot{}, errors.New("Run network authority is not expandable")
	}
	if len(allowedTargets) == 0 || len(allowedTargets) > 256 {
		return RunModeSnapshot{}, errors.New("Run network authority requires a bounded exact allowlist")
	}
	for index, target := range allowedTargets {
		if target == "" || strings.TrimSpace(target) != target ||
			(index > 0 && allowedTargets[index-1] >= target) {
			return RunModeSnapshot{}, errors.New("Run network targets must be canonical, sorted, and unique")
		}
	}
	current := make(map[string]struct{}, len(s.Scope.AllowedTargets))
	for _, target := range s.Scope.AllowedTargets {
		current[target] = struct{}{}
	}
	added := 0
	for _, target := range allowedTargets {
		if _, exists := current[target]; !exists {
			added++
		}
	}
	if added == 0 || len(allowedTargets) != len(current)+added {
		return RunModeSnapshot{}, errors.New("Run network authority must add a strict target superset")
	}
	for target := range current {
		found := false
		for _, candidate := range allowedTargets {
			if candidate == target {
				found = true
				break
			}
		}
		if !found {
			return RunModeSnapshot{}, errors.New("Run network authority cannot remove an existing target")
		}
	}

	next := s
	next.ID = strings.TrimSpace(id)
	next.Revision++
	next.Scope = CloneScope(s.Scope)
	next.Scope.NetworkMode = "allowlist"
	next.Scope.AllowedTargets = append([]string(nil), allowedTargets...)
	next.RequestedBy = strings.TrimSpace(requestedBy)
	next.Reason = strings.TrimSpace(reason)
	next.CreatedAt = at.UTC()
	if err := next.Validate(); err != nil {
		return RunModeSnapshot{}, err
	}
	if next.CreatedAt.Before(s.CreatedAt) {
		return RunModeSnapshot{}, errors.New("Run network authority transition time cannot move backwards")
	}
	if !next.SameStaticPolicy(s) || next.Phase != s.Phase {
		return RunModeSnapshot{}, errors.New("Run network authority transition changed immutable mode policy")
	}
	return next, nil
}

func CanExpandRunNetworkAuthority(status RunStatus) bool {
	return status == RunCreated || status == RunPaused
}
