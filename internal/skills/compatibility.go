package skills

import (
	"errors"
	"fmt"
	"slices"

	"cyberagent-workbench/internal/domain"
)

// InvocationSource identifies who is asking the control plane to activate a
// Skill. It is activation provenance only and never grants tools or authority.
type InvocationSource string

const (
	InvocationSourceUser  InvocationSource = "user"
	InvocationSourceModel InvocationSource = "model"
)

func (s InvocationSource) Valid() bool {
	return s == InvocationSourceUser || s == InvocationSourceModel
}

// ExecutionContext is the complete runtime compatibility tuple used when a
// selected Skill body is considered for delivery to an Agent.
type ExecutionContext struct {
	Surface domain.ExecutionSurface
	Phase   domain.ExecutionPhase
	Profile domain.Profile
	Role    domain.AgentRole
}

func (c ExecutionContext) Validate() error {
	if !c.Surface.Valid() || !c.Phase.Valid() || !domain.ValidAgentRole(c.Role) {
		return errors.New("skill execution surface, phase, or role is invalid")
	}
	profile, err := domain.ParseProfile(string(c.Profile))
	if err != nil || profile != c.Profile {
		return fmt.Errorf("invalid skill execution profile %q", c.Profile)
	}
	return nil
}

// HasModeMetadata distinguishes current manifests from legacy skill.v1
// manifests. Legacy packages remain valid and receive the exact conservative
// compatibility behavior that existed before explicit mode metadata.
func (m Manifest) HasModeMetadata() bool {
	return len(m.Surfaces) > 0 || len(m.Phases) > 0 || len(m.Roles) > 0
}

// SupportsContext reports whether the Skill body may be delivered in the
// supplied runtime context. It does not evaluate invocation provenance.
func (m Manifest) SupportsContext(context ExecutionContext) bool {
	if context.Validate() != nil || !containsProfile(m.Profiles, context.Profile) {
		return false
	}
	if !m.HasModeMetadata() {
		return legacySupportsContext(m, context)
	}
	return slices.Contains(m.Surfaces, context.Surface) &&
		slices.Contains(m.Phases, context.Phase) &&
		slices.Contains(m.Roles, context.Role)
}

// AllowsInvocation evaluates who may activate a Skill. ExplicitOnly means a
// user/operator must name and pin it; a model can never satisfy that boundary.
func (m Manifest) AllowsInvocation(source InvocationSource, explicit bool) bool {
	if !source.Valid() {
		return false
	}
	if !m.HasModeMetadata() {
		return source == InvocationSourceUser && explicit
	}
	if m.ExplicitOnly && (source != InvocationSourceUser || !explicit) {
		return false
	}
	switch source {
	case InvocationSourceUser:
		return m.UserInvocable
	case InvocationSourceModel:
		return m.ModelInvocable && !m.ExplicitOnly
	default:
		return false
	}
}

// InvocationPolicy returns the effective policy, including conservative
// defaults for legacy manifests that predate explicit mode metadata.
func (m Manifest) InvocationPolicy() (userInvocable, modelInvocable, explicitOnly bool) {
	if !m.HasModeMetadata() {
		return true, false, true
	}
	return m.UserInvocable, m.ModelInvocable, m.ExplicitOnly
}

func legacySupportsContext(m Manifest, context ExecutionContext) bool {
	if context.Role == domain.AgentRoleRoot {
		return true
	}
	if context.Role != domain.AgentRoleSpecialist {
		return false
	}
	if context.Surface == domain.ExecutionSurfaceCyber {
		return context.Profile == domain.ProfileScript && m.Name == "script"
	}
	return m.Name == string(context.Profile)
}

func validateModeMetadata(m Manifest) error {
	present := []bool{len(m.Surfaces) > 0, len(m.Phases) > 0, len(m.Roles) > 0}
	if !present[0] && !present[1] && !present[2] {
		if m.UserInvocable || m.ModelInvocable || m.ExplicitOnly {
			return errors.New("skill invocation policy requires surfaces, phases, and roles")
		}
		return nil
	}
	if !present[0] || !present[1] || !present[2] {
		return errors.New("skill surfaces, phases, and roles must be declared together")
	}
	if err := validateSurfaces(m.Surfaces); err != nil {
		return err
	}
	if err := validatePhases(m.Phases); err != nil {
		return err
	}
	if err := validateRoles(m.Roles); err != nil {
		return err
	}
	if !m.UserInvocable && !m.ModelInvocable {
		return errors.New("skill must be user_invocable, model_invocable, or both")
	}
	if m.ExplicitOnly && (!m.UserInvocable || m.ModelInvocable) {
		return errors.New("explicit_only skill must be user_invocable and not model_invocable")
	}
	return nil
}

func validateSurfaces(values []domain.ExecutionSurface) error {
	if len(values) == 0 || len(values) > MaxSurfaces {
		return fmt.Errorf("skill surfaces must contain between 1 and %d entries", MaxSurfaces)
	}
	want := []domain.ExecutionSurface{domain.ExecutionSurfaceCode, domain.ExecutionSurfaceCyber}
	return validateOrderedEnum("surfaces", values, want)
}

func validatePhases(values []domain.ExecutionPhase) error {
	if len(values) == 0 || len(values) > MaxPhases {
		return fmt.Errorf("skill phases must contain between 1 and %d entries", MaxPhases)
	}
	want := []domain.ExecutionPhase{domain.ExecutionPhasePlan, domain.ExecutionPhaseDeliver}
	return validateOrderedEnum("phases", values, want)
}

func validateRoles(values []domain.AgentRole) error {
	if len(values) == 0 || len(values) > MaxRoles {
		return fmt.Errorf("skill roles must contain between 1 and %d entries", MaxRoles)
	}
	want := []domain.AgentRole{domain.AgentRoleRoot, domain.AgentRoleSpecialist}
	return validateOrderedEnum("roles", values, want)
}

func validateOrderedEnum[T comparable](label string, values, order []T) error {
	previous := -1
	for _, value := range values {
		index := slices.Index(order, value)
		if index < 0 {
			return fmt.Errorf("invalid skill %s value %v", label, value)
		}
		if index <= previous {
			return fmt.Errorf("skill %s must be unique and canonically ordered", label)
		}
		previous = index
	}
	return nil
}
