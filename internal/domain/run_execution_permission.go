package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	RunExecutionPermissionProtocolVersion = "run_execution_permission.v1"
	RunExecutionPermissionPolicyVersion   = "execution_permission_policy.v1"
	MaxRunExecutionPermissionReasonRunes  = 1024
)

// RunExecutionPermissionMode is orthogonal to RunExecutionInteractionMode.
// The permission mode answers what may be authorized; the interaction mode
// answers how an authorized operation is presented and transported.
type RunExecutionPermissionMode string

const (
	RunExecutionPermissionConservative RunExecutionPermissionMode = "conservative"
	RunExecutionPermissionApproval     RunExecutionPermissionMode = "approval"
	RunExecutionPermissionFullAccess   RunExecutionPermissionMode = "full_access"
	RunExecutionPermissionDebug        RunExecutionPermissionMode = "debug"
)

type ExecutionPermissionApprovalPolicy string

const (
	ExecutionPermissionApprovalFixedTemplates ExecutionPermissionApprovalPolicy = "fixed_templates"
	ExecutionPermissionApprovalPerCommand     ExecutionPermissionApprovalPolicy = "per_command"
	ExecutionPermissionApprovalNone           ExecutionPermissionApprovalPolicy = "none"
)

type ExecutionPermissionCommandScope string

const (
	ExecutionPermissionCommandFixedTemplates      ExecutionPermissionCommandScope = "fixed_templates"
	ExecutionPermissionCommandArbitraryStateless  ExecutionPermissionCommandScope = "arbitrary_stateless"
	ExecutionPermissionCommandArbitraryPersistent ExecutionPermissionCommandScope = "arbitrary_persistent"
)

type ExecutionPermissionFilesystemScope string

const (
	ExecutionPermissionFilesystemWorkspaceGuarded ExecutionPermissionFilesystemScope = "workspace_guarded"
	ExecutionPermissionFilesystemHostFull         ExecutionPermissionFilesystemScope = "host_full"
)

type ExecutionPermissionNetworkScope string

const (
	ExecutionPermissionNetworkDisabled ExecutionPermissionNetworkScope = "disabled"
	ExecutionPermissionNetworkHost     ExecutionPermissionNetworkScope = "host"
)

type ExecutionPermissionGate string

const (
	ExecutionPermissionGateConservative       ExecutionPermissionGate = "conservative_control"
	ExecutionPermissionGateOperatorApproval   ExecutionPermissionGate = "operator_approval"
	ExecutionPermissionGateDangerFullAccess   ExecutionPermissionGate = "danger_full_access"
	ExecutionPermissionGateDebugMaximumAccess ExecutionPermissionGate = "debug_maximum_access"
)

type runExecutionPermissionDefinition struct {
	ApprovalPolicy     ExecutionPermissionApprovalPolicy
	CommandScope       ExecutionPermissionCommandScope
	FilesystemScope    ExecutionPermissionFilesystemScope
	NetworkScope       ExecutionPermissionNetworkScope
	PersistentTerminal bool
	BackgroundProcess  bool
	AgentTerminalInput bool
	RiskTier           ExecutionRiskTier
	RequiredGate       ExecutionPermissionGate
	OperatorConfirmed  bool
}

var runExecutionPermissionDefinitions = map[RunExecutionPermissionMode]runExecutionPermissionDefinition{
	RunExecutionPermissionConservative: {
		ApprovalPolicy:  ExecutionPermissionApprovalFixedTemplates,
		CommandScope:    ExecutionPermissionCommandFixedTemplates,
		FilesystemScope: ExecutionPermissionFilesystemWorkspaceGuarded,
		NetworkScope:    ExecutionPermissionNetworkDisabled,
		RiskTier:        ExecutionRiskMinimal,
		RequiredGate:    ExecutionPermissionGateConservative,
	},
	RunExecutionPermissionApproval: {
		ApprovalPolicy:    ExecutionPermissionApprovalPerCommand,
		CommandScope:      ExecutionPermissionCommandArbitraryStateless,
		FilesystemScope:   ExecutionPermissionFilesystemHostFull,
		NetworkScope:      ExecutionPermissionNetworkHost,
		RiskTier:          ExecutionRiskElevated,
		RequiredGate:      ExecutionPermissionGateOperatorApproval,
		OperatorConfirmed: true,
	},
	RunExecutionPermissionFullAccess: {
		ApprovalPolicy:    ExecutionPermissionApprovalNone,
		CommandScope:      ExecutionPermissionCommandArbitraryStateless,
		FilesystemScope:   ExecutionPermissionFilesystemHostFull,
		NetworkScope:      ExecutionPermissionNetworkHost,
		RiskTier:          ExecutionRiskHigh,
		RequiredGate:      ExecutionPermissionGateDangerFullAccess,
		OperatorConfirmed: true,
	},
	RunExecutionPermissionDebug: {
		ApprovalPolicy:     ExecutionPermissionApprovalNone,
		CommandScope:       ExecutionPermissionCommandArbitraryPersistent,
		FilesystemScope:    ExecutionPermissionFilesystemHostFull,
		NetworkScope:       ExecutionPermissionNetworkHost,
		PersistentTerminal: true,
		BackgroundProcess:  true,
		AgentTerminalInput: true,
		RiskTier:           ExecutionRiskHigh,
		RequiredGate:       ExecutionPermissionGateDebugMaximumAccess,
		OperatorConfirmed:  true,
	},
}

func ParseRunExecutionPermissionMode(value string) (RunExecutionPermissionMode, error) {
	mode := RunExecutionPermissionMode(strings.ToLower(strings.TrimSpace(value)))
	if _, ok := runExecutionPermissionDefinitions[mode]; !ok {
		return "", fmt.Errorf("unsupported Run execution permission mode %q", value)
	}
	return mode, nil
}

func (m RunExecutionPermissionMode) Valid() bool {
	parsed, err := ParseRunExecutionPermissionMode(string(m))
	return err == nil && parsed == m
}

// ExecutionPermissionRuntimeCapabilities are process-local startup grants.
// They are deliberately never persisted in a Run snapshot.
type ExecutionPermissionRuntimeCapabilities struct {
	OperatorApprovalEnabled   bool
	DangerFullAccessEnabled   bool
	DebugMaximumAccessEnabled bool
}

func (c ExecutionPermissionRuntimeCapabilities) Validate() error {
	if c.DebugMaximumAccessEnabled && !c.DangerFullAccessEnabled {
		return errors.New("debug maximum access requires danger full access")
	}
	if c.DangerFullAccessEnabled && !c.OperatorApprovalEnabled {
		return errors.New("danger full access requires permission control")
	}
	return nil
}

func (c ExecutionPermissionRuntimeCapabilities) Allows(
	mode RunExecutionPermissionMode,
) bool {
	if c.Validate() != nil {
		return false
	}
	switch mode {
	case RunExecutionPermissionConservative:
		return true
	case RunExecutionPermissionApproval:
		return c.OperatorApprovalEnabled
	case RunExecutionPermissionFullAccess:
		return c.DangerFullAccessEnabled
	case RunExecutionPermissionDebug:
		return c.DebugMaximumAccessEnabled
	default:
		return false
	}
}

// RunExecutionPermissionSnapshot records the operator-selected policy ceiling.
// ProcessEnabled, ExecutionAuthorized, and CapabilityGrant must remain false:
// actual authority is recomputed from process-local startup capabilities for
// every operation.
type RunExecutionPermissionSnapshot struct {
	ID                  string
	RunID               string
	MissionID           string
	Revision            int64
	ProtocolVersion     string
	Mode                RunExecutionPermissionMode
	ApprovalPolicy      ExecutionPermissionApprovalPolicy
	CommandScope        ExecutionPermissionCommandScope
	FilesystemScope     ExecutionPermissionFilesystemScope
	NetworkScope        ExecutionPermissionNetworkScope
	PersistentTerminal  bool
	BackgroundProcess   bool
	AgentTerminalInput  bool
	RiskTier            ExecutionRiskTier
	RequiredGate        ExecutionPermissionGate
	PolicyVersion       string
	OperatorConfirmed   bool
	ProcessEnabled      bool
	ExecutionAuthorized bool
	CapabilityGrant     bool
	RequestedBy         string
	Reason              string
	CreatedAt           time.Time
}

func NewInitialRunExecutionPermissionSnapshot(id string, run Run, mission Mission,
	requestedBy string, at time.Time,
) (RunExecutionPermissionSnapshot, error) {
	if run.MissionID != mission.ID {
		return RunExecutionPermissionSnapshot{}, errors.New(
			"Run execution permission Run and Mission identities do not match")
	}
	snapshot := newRunExecutionPermissionSnapshot(id, run.ID, mission.ID, 1,
		RunExecutionPermissionConservative, false, requestedBy,
		"initial conservative execution permission", at)
	if err := snapshot.Validate(); err != nil {
		return RunExecutionPermissionSnapshot{}, err
	}
	return snapshot, nil
}

func newRunExecutionPermissionSnapshot(id string, runID string, missionID string,
	revision int64, mode RunExecutionPermissionMode, confirmed bool,
	requestedBy string, reason string, at time.Time,
) RunExecutionPermissionSnapshot {
	definition := runExecutionPermissionDefinitions[mode]
	return RunExecutionPermissionSnapshot{
		ID: strings.TrimSpace(id), RunID: runID, MissionID: missionID, Revision: revision,
		ProtocolVersion: RunExecutionPermissionProtocolVersion, Mode: mode,
		ApprovalPolicy: definition.ApprovalPolicy, CommandScope: definition.CommandScope,
		FilesystemScope: definition.FilesystemScope, NetworkScope: definition.NetworkScope,
		PersistentTerminal: definition.PersistentTerminal,
		BackgroundProcess:  definition.BackgroundProcess,
		AgentTerminalInput: definition.AgentTerminalInput, RiskTier: definition.RiskTier,
		RequiredGate: definition.RequiredGate, PolicyVersion: RunExecutionPermissionPolicyVersion,
		OperatorConfirmed: confirmed, RequestedBy: strings.TrimSpace(requestedBy),
		Reason: strings.TrimSpace(reason), CreatedAt: at.UTC(),
	}
}

func (s RunExecutionPermissionSnapshot) Validate() error {
	for label, value := range map[string]string{
		"snapshot id": s.ID, "Run id": s.RunID, "Mission id": s.MissionID,
		"requester": s.RequestedBy, "policy version": s.PolicyVersion,
	} {
		if !ValidAgentID(value) || strings.ContainsRune(value, 0) {
			return fmt.Errorf(
				"Run execution permission %s must be normalized and bounded UTF-8", label)
		}
	}
	if s.Revision <= 0 {
		return errors.New("Run execution permission revision must be positive")
	}
	if s.ProtocolVersion != RunExecutionPermissionProtocolVersion {
		return fmt.Errorf("unsupported Run execution permission protocol %q", s.ProtocolVersion)
	}
	definition, ok := runExecutionPermissionDefinitions[s.Mode]
	if !ok {
		return fmt.Errorf("invalid Run execution permission mode %q", s.Mode)
	}
	if s.ApprovalPolicy != definition.ApprovalPolicy ||
		s.CommandScope != definition.CommandScope ||
		s.FilesystemScope != definition.FilesystemScope ||
		s.NetworkScope != definition.NetworkScope ||
		s.PersistentTerminal != definition.PersistentTerminal ||
		s.BackgroundProcess != definition.BackgroundProcess ||
		s.AgentTerminalInput != definition.AgentTerminalInput ||
		s.RiskTier != definition.RiskTier ||
		s.RequiredGate != definition.RequiredGate ||
		s.OperatorConfirmed != definition.OperatorConfirmed {
		return errors.New("Run execution permission controls do not match the selected mode")
	}
	if s.PolicyVersion != RunExecutionPermissionPolicyVersion {
		return fmt.Errorf("unsupported Run execution permission policy %q", s.PolicyVersion)
	}
	if s.ProcessEnabled || s.ExecutionAuthorized || s.CapabilityGrant {
		return errors.New("Run execution permission selection cannot grant runtime authority")
	}
	if !utf8.ValidString(s.Reason) || strings.TrimSpace(s.Reason) != s.Reason ||
		s.Reason == "" ||
		utf8.RuneCountInString(s.Reason) > MaxRunExecutionPermissionReasonRunes ||
		strings.ContainsRune(s.Reason, 0) {
		return fmt.Errorf(
			"Run execution permission reason must contain between 1 and %d normalized UTF-8 characters",
			MaxRunExecutionPermissionReasonRunes)
	}
	if s.CreatedAt.IsZero() {
		return errors.New("Run execution permission creation time is required")
	}
	return nil
}

func (s RunExecutionPermissionSnapshot) Next(id string,
	mode RunExecutionPermissionMode, confirmed bool, requestedBy string,
	reason string, at time.Time,
) (RunExecutionPermissionSnapshot, error) {
	if err := s.Validate(); err != nil {
		return RunExecutionPermissionSnapshot{}, err
	}
	if !mode.Valid() {
		return RunExecutionPermissionSnapshot{}, fmt.Errorf(
			"invalid Run execution permission mode %q", mode)
	}
	if mode == s.Mode {
		return RunExecutionPermissionSnapshot{}, errors.New(
			"Run execution permission transition must change mode")
	}
	next := newRunExecutionPermissionSnapshot(id, s.RunID, s.MissionID,
		s.Revision+1, mode, confirmed, requestedBy, reason, at)
	if err := next.Validate(); err != nil {
		return RunExecutionPermissionSnapshot{}, err
	}
	if next.CreatedAt.Before(s.CreatedAt) {
		return RunExecutionPermissionSnapshot{}, errors.New(
			"Run execution permission transition time cannot move backwards")
	}
	return next, nil
}

func CanChangeRunExecutionPermission(status RunStatus) bool {
	return status == RunCreated || status == RunPaused
}

type RunExecutionPermissionOperation struct {
	KeyDigest          string
	RequestFingerprint string
	SnapshotID         string
	RunID              string
	RequestedBy        string
	CreatedAt          time.Time
}

func (o RunExecutionPermissionOperation) Validate() error {
	if !validLowerHexDigest(o.KeyDigest) || !validLowerHexDigest(o.RequestFingerprint) {
		return errors.New(
			"Run execution permission operation digests must be lowercase SHA-256")
	}
	for label, value := range map[string]string{
		"snapshot id": o.SnapshotID, "Run id": o.RunID, "requester": o.RequestedBy,
	} {
		if !ValidAgentID(value) || strings.ContainsRune(value, 0) {
			return fmt.Errorf(
				"Run execution permission operation %s must be normalized and bounded UTF-8",
				label)
		}
	}
	if o.CreatedAt.IsZero() {
		return errors.New("Run execution permission operation creation time is required")
	}
	return nil
}
