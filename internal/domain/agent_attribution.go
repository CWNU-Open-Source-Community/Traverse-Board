package domain

import (
	"errors"
	"strings"
)

// AgentAttributionSource describes how an execution fact was bound to its
// actor or Run authority anchor. Recorded identities were supplied at the execution boundary;
// supervisor_root identities were proven from the active root Supervisor in
// the same transaction; legacy values were reconstructed by a forward
// migration and remain explicitly distinguishable from newly recorded facts.
type AgentAttributionSource string

const (
	AgentAttributionRecorded       AgentAttributionSource = "recorded"
	AgentAttributionSupervisorRoot AgentAttributionSource = "supervisor_root"
	// AgentAttributionOperatorRoot records an operator-started Run operation.
	// AgentID is the real root authority anchor, not a claim that a Supervisor
	// attempt requested the work, so AgentAttemptID must remain empty.
	AgentAttributionOperatorRoot  AgentAttributionSource = "operator_root"
	AgentAttributionLegacyRoot    AgentAttributionSource = "legacy_root"
	AgentAttributionLegacyUnknown AgentAttributionSource = "legacy_unknown"
)

func (s AgentAttributionSource) Valid() bool {
	switch s {
	case AgentAttributionRecorded, AgentAttributionSupervisorRoot,
		AgentAttributionOperatorRoot,
		AgentAttributionLegacyRoot, AgentAttributionLegacyUnknown:
		return true
	default:
		return false
	}
}

// AgentAttribution is a durable execution attribution. AgentAttemptID is
// required when a model Agent performed the action. An operator_root value
// identifies operator-started work authorized by the real root Agent anchor
// without claiming a model attempt. A legacy root can lack the attempt because
// old Command Runtime rows did not retain the root Supervisor identity.
type AgentAttribution struct {
	AgentID        string
	AgentAttemptID string
	Source         AgentAttributionSource
}

func (a AgentAttribution) Validate() error {
	if !a.Source.Valid() || strings.TrimSpace(a.AgentID) != a.AgentID ||
		strings.TrimSpace(a.AgentAttemptID) != a.AgentAttemptID ||
		len([]rune(a.AgentID)) > MaxSupervisorToolIdentityRunes ||
		len([]rune(a.AgentAttemptID)) > MaxSupervisorToolIdentityRunes {
		return errors.New("Agent attribution is invalid")
	}
	if a.Source == AgentAttributionLegacyUnknown {
		if a.AgentID != "" || a.AgentAttemptID != "" {
			return errors.New("unknown Agent attribution cannot carry an identity")
		}
		return nil
	}
	if !ValidAgentID(a.AgentID) {
		return errors.New("Agent attribution requires a valid Agent identity")
	}
	if (a.Source == AgentAttributionRecorded ||
		a.Source == AgentAttributionSupervisorRoot) &&
		!ValidAgentID(a.AgentAttemptID) {
		return errors.New("recorded Agent attribution requires an attempt identity")
	}
	if a.Source == AgentAttributionOperatorRoot && a.AgentAttemptID != "" {
		return errors.New("operator root attribution cannot claim an Agent attempt")
	}
	if a.AgentAttemptID != "" && !ValidAgentID(a.AgentAttemptID) {
		return errors.New("Agent attribution attempt identity is invalid")
	}
	return nil
}
