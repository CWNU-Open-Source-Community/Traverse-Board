package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	RunExecutionInteractionProtocolVersion = "run_execution_interaction.v1"
	RunExecutionInteractionPolicyVersion   = "execution_interaction_policy.v1"
	MaxRunExecutionInteractionReasonRunes  = 1024
)

type RunExecutionInteractionMode string

const (
	RunExecutionInteractionPreview    RunExecutionInteractionMode = "preview"
	RunExecutionInteractionControlled RunExecutionInteractionMode = "controlled"
	RunExecutionInteractionDebug      RunExecutionInteractionMode = "debug"
	RunExecutionInteractionCyber      RunExecutionInteractionMode = "cyber"
)

type WorkspaceTrustLevel string

const (
	WorkspaceTrustUntrusted WorkspaceTrustLevel = "untrusted"
	WorkspaceTrustTrusted   WorkspaceTrustLevel = "trusted"
)

type ExecutionCommandForm string

const (
	ExecutionCommandNone           ExecutionCommandForm = "none"
	ExecutionCommandStructuredArgv ExecutionCommandForm = "structured_argv"
	ExecutionCommandUserConPTY     ExecutionCommandForm = "user_conpty"
	ExecutionCommandContainerPTY   ExecutionCommandForm = "container_pty"
)

type ExecutionInteractionGate string

const (
	ExecutionInteractionGateNone              ExecutionInteractionGate = "none"
	ExecutionInteractionGateLocalOSSandbox    ExecutionInteractionGate = "local_os_sandbox_gate"
	ExecutionInteractionGateDebugAgentLease   ExecutionInteractionGate = "debug_agent_input_lease"
	ExecutionInteractionGateCyberContainerPTY ExecutionInteractionGate = "cyber_container_terminal_gate"
)

type runExecutionInteractionDefinition struct {
	Surface              ExecutionSurface
	ExecutionProfile     RunExecutionProfile
	Trust                WorkspaceTrustLevel
	CommandForm          ExecutionCommandForm
	PersistentTerminal   bool
	UserInputAvailable   bool
	RequiredGate         ExecutionInteractionGate
	OperatorConfirmation bool
}

var runExecutionInteractionDefinitions = map[RunExecutionInteractionMode]runExecutionInteractionDefinition{
	RunExecutionInteractionPreview: {
		ExecutionProfile: RunExecutionProfilePreview,
		Trust:            WorkspaceTrustUntrusted, CommandForm: ExecutionCommandNone,
		RequiredGate: ExecutionInteractionGateNone,
	},
	RunExecutionInteractionControlled: {
		Surface: ExecutionSurfaceCode, ExecutionProfile: RunExecutionProfileLocal,
		Trust: WorkspaceTrustTrusted, CommandForm: ExecutionCommandStructuredArgv,
		RequiredGate:         ExecutionInteractionGateLocalOSSandbox,
		OperatorConfirmation: true,
	},
	RunExecutionInteractionDebug: {
		Surface: ExecutionSurfaceCode, ExecutionProfile: RunExecutionProfileLocal,
		Trust: WorkspaceTrustTrusted, CommandForm: ExecutionCommandUserConPTY,
		PersistentTerminal: true, UserInputAvailable: true,
		RequiredGate:         ExecutionInteractionGateDebugAgentLease,
		OperatorConfirmation: true,
	},
	RunExecutionInteractionCyber: {
		Surface: ExecutionSurfaceCyber, ExecutionProfile: RunExecutionProfileDocker,
		Trust: WorkspaceTrustTrusted, CommandForm: ExecutionCommandContainerPTY,
		PersistentTerminal: true, UserInputAvailable: true,
		RequiredGate:         ExecutionInteractionGateCyberContainerPTY,
		OperatorConfirmation: true,
	},
}

func ParseRunExecutionInteractionMode(value string) (RunExecutionInteractionMode, error) {
	mode := RunExecutionInteractionMode(strings.ToLower(strings.TrimSpace(value)))
	if _, ok := runExecutionInteractionDefinitions[mode]; !ok {
		return "", fmt.Errorf("unsupported Run execution interaction mode %q", value)
	}
	return mode, nil
}

func (m RunExecutionInteractionMode) Valid() bool {
	parsed, err := ParseRunExecutionInteractionMode(string(m))
	return err == nil && parsed == m
}

func ParseWorkspaceTrustLevel(value string) (WorkspaceTrustLevel, error) {
	trust := WorkspaceTrustLevel(strings.ToLower(strings.TrimSpace(value)))
	switch trust {
	case WorkspaceTrustUntrusted, WorkspaceTrustTrusted:
		return trust, nil
	default:
		return "", fmt.Errorf("unsupported Workspace trust level %q", value)
	}
}

// RunExecutionInteractionSnapshot records operator intent, not process
// authority. Agent input always requires a separate process-lifetime lease.
type RunExecutionInteractionSnapshot struct {
	ID                       string
	RunID                    string
	MissionID                string
	Revision                 int64
	ProtocolVersion          string
	Mode                     RunExecutionInteractionMode
	Surface                  ExecutionSurface
	ExecutionProfile         RunExecutionProfile
	ExecutionProfileRevision int64
	WorkspaceTrust           WorkspaceTrustLevel
	CommandForm              ExecutionCommandForm
	PersistentTerminal       bool
	UserInputAvailable       bool
	AgentInputDefault        bool
	NetworkScope             ExecutionNetworkScope
	RequiredGate             ExecutionInteractionGate
	PolicyVersion            string
	OperatorConfirmed        bool
	ProcessEnabled           bool
	ExecutionAuthorized      bool
	CapabilityGrant          bool
	RequestedBy              string
	Reason                   string
	CreatedAt                time.Time
}

func NewInitialRunExecutionInteractionSnapshot(id string, run Run, mission Mission,
	mode RunModeSnapshot, profile RunExecutionProfileSnapshot, requestedBy string,
	at time.Time,
) (RunExecutionInteractionSnapshot, error) {
	if run.MissionID != mission.ID || mode.RunID != run.ID || mode.MissionID != mission.ID ||
		profile.RunID != run.ID || profile.MissionID != mission.ID {
		return RunExecutionInteractionSnapshot{}, errors.New(
			"Run execution interaction identities do not match")
	}
	snapshot := newRunExecutionInteractionSnapshot(id, run.ID, mission.ID, 1,
		RunExecutionInteractionPreview, mode.Surface, profile, WorkspaceTrustUntrusted,
		false, requestedBy, "initial preview execution interaction", at)
	if err := snapshot.Validate(); err != nil {
		return RunExecutionInteractionSnapshot{}, err
	}
	return snapshot, nil
}

func newRunExecutionInteractionSnapshot(id string, runID string, missionID string,
	revision int64, interactionMode RunExecutionInteractionMode, surface ExecutionSurface,
	profile RunExecutionProfileSnapshot, trust WorkspaceTrustLevel, confirmed bool,
	requestedBy string, reason string, at time.Time,
) RunExecutionInteractionSnapshot {
	definition := runExecutionInteractionDefinitions[interactionMode]
	executionProfile := definition.ExecutionProfile
	if interactionMode == RunExecutionInteractionPreview {
		executionProfile = profile.Profile
	}
	return RunExecutionInteractionSnapshot{
		ID: strings.TrimSpace(id), RunID: runID, MissionID: missionID, Revision: revision,
		ProtocolVersion: RunExecutionInteractionProtocolVersion, Mode: interactionMode,
		Surface: surface, ExecutionProfile: executionProfile,
		ExecutionProfileRevision: profile.Revision, WorkspaceTrust: trust,
		CommandForm: definition.CommandForm, PersistentTerminal: definition.PersistentTerminal,
		UserInputAvailable: definition.UserInputAvailable, AgentInputDefault: false,
		NetworkScope: ExecutionNetworkDisabled, RequiredGate: definition.RequiredGate,
		PolicyVersion: RunExecutionInteractionPolicyVersion, OperatorConfirmed: confirmed,
		ProcessEnabled: false, ExecutionAuthorized: false, CapabilityGrant: false,
		RequestedBy: strings.TrimSpace(requestedBy), Reason: strings.TrimSpace(reason),
		CreatedAt: at.UTC(),
	}
}

func (s RunExecutionInteractionSnapshot) Validate() error {
	for label, value := range map[string]string{
		"snapshot id": s.ID, "Run id": s.RunID, "Mission id": s.MissionID,
		"requester": s.RequestedBy, "policy version": s.PolicyVersion,
	} {
		if !ValidAgentID(value) || strings.ContainsRune(value, 0) {
			return fmt.Errorf("Run execution interaction %s must be normalized and bounded UTF-8",
				label)
		}
	}
	if s.Revision <= 0 || s.ExecutionProfileRevision <= 0 {
		return errors.New("Run execution interaction revisions must be positive")
	}
	if s.ProtocolVersion != RunExecutionInteractionProtocolVersion {
		return fmt.Errorf("unsupported Run execution interaction protocol %q", s.ProtocolVersion)
	}
	definition, ok := runExecutionInteractionDefinitions[s.Mode]
	if !ok {
		return fmt.Errorf("invalid Run execution interaction mode %q", s.Mode)
	}
	if !s.Surface.Valid() {
		return fmt.Errorf("invalid Run execution interaction surface %q", s.Surface)
	}
	expectedProfile := definition.ExecutionProfile
	if s.Mode == RunExecutionInteractionPreview {
		if !s.ExecutionProfile.Valid() {
			return fmt.Errorf("invalid preview execution profile %q", s.ExecutionProfile)
		}
		expectedProfile = s.ExecutionProfile
	}
	if s.ExecutionProfile != expectedProfile || s.WorkspaceTrust != definition.Trust ||
		s.CommandForm != definition.CommandForm ||
		s.PersistentTerminal != definition.PersistentTerminal ||
		s.UserInputAvailable != definition.UserInputAvailable ||
		s.RequiredGate != definition.RequiredGate ||
		s.OperatorConfirmed != definition.OperatorConfirmation {
		return errors.New("Run execution interaction controls do not match the selected mode")
	}
	if definition.Surface != "" && s.Surface != definition.Surface {
		return errors.New("Run execution interaction surface does not match the selected mode")
	}
	if s.AgentInputDefault || s.NetworkScope != ExecutionNetworkDisabled ||
		s.ProcessEnabled || s.ExecutionAuthorized || s.CapabilityGrant {
		return errors.New("Run execution interaction selection cannot grant runtime authority")
	}
	if s.PolicyVersion != RunExecutionInteractionPolicyVersion {
		return fmt.Errorf("unsupported Run execution interaction policy %q", s.PolicyVersion)
	}
	if !utf8.ValidString(s.Reason) || strings.TrimSpace(s.Reason) != s.Reason ||
		s.Reason == "" ||
		utf8.RuneCountInString(s.Reason) > MaxRunExecutionInteractionReasonRunes ||
		strings.ContainsRune(s.Reason, 0) {
		return fmt.Errorf(
			"Run execution interaction reason must contain between 1 and %d normalized UTF-8 characters",
			MaxRunExecutionInteractionReasonRunes)
	}
	if s.CreatedAt.IsZero() {
		return errors.New("Run execution interaction creation time is required")
	}
	return nil
}

func (s RunExecutionInteractionSnapshot) Next(id string,
	mode RunExecutionInteractionMode, runMode RunModeSnapshot,
	profile RunExecutionProfileSnapshot, trust WorkspaceTrustLevel, confirmed bool,
	requestedBy string, reason string, at time.Time,
) (RunExecutionInteractionSnapshot, error) {
	if err := s.Validate(); err != nil {
		return RunExecutionInteractionSnapshot{}, err
	}
	if runMode.RunID != s.RunID || runMode.MissionID != s.MissionID ||
		profile.RunID != s.RunID || profile.MissionID != s.MissionID {
		return RunExecutionInteractionSnapshot{}, errors.New(
			"Run execution interaction transition identities do not match")
	}
	if mode == s.Mode && trust == s.WorkspaceTrust &&
		profile.Revision == s.ExecutionProfileRevision &&
		runMode.Surface == s.Surface {
		return RunExecutionInteractionSnapshot{}, errors.New(
			"Run execution interaction transition must change effective intent")
	}
	next := newRunExecutionInteractionSnapshot(id, s.RunID, s.MissionID, s.Revision+1,
		mode, runMode.Surface, profile, trust, confirmed, requestedBy, reason, at)
	if err := next.Validate(); err != nil {
		return RunExecutionInteractionSnapshot{}, err
	}
	if next.Mode != RunExecutionInteractionPreview &&
		next.ExecutionProfile != profile.Profile {
		return RunExecutionInteractionSnapshot{}, errors.New(
			"Run execution interaction requires a matching execution profile")
	}
	if next.CreatedAt.Before(s.CreatedAt) {
		return RunExecutionInteractionSnapshot{}, errors.New(
			"Run execution interaction transition time cannot move backwards")
	}
	return next, nil
}

func CanChangeRunExecutionInteraction(status RunStatus) bool {
	return status == RunCreated || status == RunPaused
}

type RunExecutionInteractionOperation struct {
	KeyDigest          string
	RequestFingerprint string
	SnapshotID         string
	RunID              string
	RequestedBy        string
	CreatedAt          time.Time
}

func (o RunExecutionInteractionOperation) Validate() error {
	if !validLowerHexDigest(o.KeyDigest) || !validLowerHexDigest(o.RequestFingerprint) {
		return errors.New(
			"Run execution interaction operation digests must be lowercase SHA-256")
	}
	for label, value := range map[string]string{
		"snapshot id": o.SnapshotID, "Run id": o.RunID, "requester": o.RequestedBy,
	} {
		if !ValidAgentID(value) || strings.ContainsRune(value, 0) {
			return fmt.Errorf(
				"Run execution interaction operation %s must be normalized and bounded UTF-8",
				label)
		}
	}
	if o.CreatedAt.IsZero() {
		return errors.New("Run execution interaction operation creation time is required")
	}
	return nil
}
