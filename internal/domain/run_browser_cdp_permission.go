package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	RunBrowserCDPPermissionProtocolVersion = "run_browser_cdp_permission.v1"
	RunBrowserCDPPermissionPolicyVersion   = "browser_cdp_permission_policy.v1"
	MaxRunBrowserCDPPermissionReasonRunes  = 1024
)

// RunBrowserCDPPermissionMode is independent from the host execution
// permission. It records the maximum CDP method family an operator intends to
// make available; it never opens a transport or starts a browser process.
type RunBrowserCDPPermissionMode string

const (
	RunBrowserCDPPermissionRestricted RunBrowserCDPPermissionMode = "restricted"
	RunBrowserCDPPermissionFullDebug  RunBrowserCDPPermissionMode = "full_debug"
)

type BrowserCDPPermissionGate string

const (
	BrowserCDPPermissionGateRestricted BrowserCDPPermissionGate = "browser_cdp_control"
	BrowserCDPPermissionGateFullDebug  BrowserCDPPermissionGate = "full_cdp_debug"
)

type runBrowserCDPPermissionDefinition struct {
	NavigateAllowed        bool
	DOMSnapshotAllowed     bool
	ScreenshotAllowed      bool
	RequestCaptureAllowed  bool
	RequestMutationAllowed bool
	RequestReplayAllowed   bool
	CookieAccessAllowed    bool
	ArbitraryMethodAllowed bool
	RiskTier               ExecutionRiskTier
	RequiredGate           BrowserCDPPermissionGate
	OperatorConfirmed      bool
}

var runBrowserCDPPermissionDefinitions = map[RunBrowserCDPPermissionMode]runBrowserCDPPermissionDefinition{
	RunBrowserCDPPermissionRestricted: {
		NavigateAllowed: true, DOMSnapshotAllowed: true, ScreenshotAllowed: true,
		RiskTier: ExecutionRiskMinimal, RequiredGate: BrowserCDPPermissionGateRestricted,
	},
	RunBrowserCDPPermissionFullDebug: {
		NavigateAllowed: true, DOMSnapshotAllowed: true, ScreenshotAllowed: true,
		RequestCaptureAllowed: true, RequestMutationAllowed: true,
		RequestReplayAllowed: true, CookieAccessAllowed: true,
		ArbitraryMethodAllowed: true, RiskTier: ExecutionRiskHigh,
		RequiredGate: BrowserCDPPermissionGateFullDebug, OperatorConfirmed: true,
	},
}

func ParseRunBrowserCDPPermissionMode(value string) (RunBrowserCDPPermissionMode, error) {
	mode := RunBrowserCDPPermissionMode(strings.ToLower(strings.TrimSpace(value)))
	if _, ok := runBrowserCDPPermissionDefinitions[mode]; !ok {
		return "", fmt.Errorf("unsupported Run browser CDP permission mode %q", value)
	}
	return mode, nil
}

func (m RunBrowserCDPPermissionMode) Valid() bool {
	parsed, err := ParseRunBrowserCDPPermissionMode(string(m))
	return err == nil && parsed == m
}

// BrowserCDPPermissionRuntimeCapabilities are process-local startup grants.
// FullDebugEnabled is valid only when the ordinary CDP control endpoint and
// the maximum Debug execution boundary were both enabled for this process.
type BrowserCDPPermissionRuntimeCapabilities struct {
	ControlEnabled   bool
	FullDebugEnabled bool
}

func (c BrowserCDPPermissionRuntimeCapabilities) Validate() error {
	if c.FullDebugEnabled && !c.ControlEnabled {
		return errors.New("full CDP debug requires browser CDP control")
	}
	return nil
}

func (c BrowserCDPPermissionRuntimeCapabilities) Allows(
	mode RunBrowserCDPPermissionMode,
) bool {
	if c.Validate() != nil {
		return false
	}
	switch mode {
	case RunBrowserCDPPermissionRestricted:
		return c.ControlEnabled
	case RunBrowserCDPPermissionFullDebug:
		return c.FullDebugEnabled
	default:
		return false
	}
}

// RunBrowserCDPPermissionSnapshot is an immutable policy ceiling. The last
// four authority fields must remain false; a future concrete CDP operation
// must independently authorize process identity, target scope, transport, and
// the exact method.
type RunBrowserCDPPermissionSnapshot struct {
	ID                     string
	RunID                  string
	MissionID              string
	Revision               int64
	ProtocolVersion        string
	Mode                   RunBrowserCDPPermissionMode
	NavigateAllowed        bool
	DOMSnapshotAllowed     bool
	ScreenshotAllowed      bool
	RequestCaptureAllowed  bool
	RequestMutationAllowed bool
	RequestReplayAllowed   bool
	CookieAccessAllowed    bool
	ArbitraryMethodAllowed bool
	RiskTier               ExecutionRiskTier
	RequiredGate           BrowserCDPPermissionGate
	PolicyVersion          string
	OperatorConfirmed      bool
	TransportEnabled       bool
	BrowserStartAuthorized bool
	RuntimeAuthorized      bool
	CapabilityGrant        bool
	RequestedBy            string
	Reason                 string
	CreatedAt              time.Time
}

func NewInitialRunBrowserCDPPermissionSnapshot(id string, run Run, mission Mission,
	requestedBy string, at time.Time,
) (RunBrowserCDPPermissionSnapshot, error) {
	if run.MissionID != mission.ID {
		return RunBrowserCDPPermissionSnapshot{}, errors.New(
			"Run browser CDP permission Run and Mission identities do not match")
	}
	snapshot := newRunBrowserCDPPermissionSnapshot(id, run.ID, mission.ID, 1,
		RunBrowserCDPPermissionRestricted, false, requestedBy,
		"initial restricted browser CDP permission", at)
	if err := snapshot.Validate(); err != nil {
		return RunBrowserCDPPermissionSnapshot{}, err
	}
	return snapshot, nil
}

func newRunBrowserCDPPermissionSnapshot(id string, runID string, missionID string,
	revision int64, mode RunBrowserCDPPermissionMode, confirmed bool,
	requestedBy string, reason string, at time.Time,
) RunBrowserCDPPermissionSnapshot {
	definition := runBrowserCDPPermissionDefinitions[mode]
	return RunBrowserCDPPermissionSnapshot{
		ID: strings.TrimSpace(id), RunID: runID, MissionID: missionID, Revision: revision,
		ProtocolVersion: RunBrowserCDPPermissionProtocolVersion, Mode: mode,
		NavigateAllowed:        definition.NavigateAllowed,
		DOMSnapshotAllowed:     definition.DOMSnapshotAllowed,
		ScreenshotAllowed:      definition.ScreenshotAllowed,
		RequestCaptureAllowed:  definition.RequestCaptureAllowed,
		RequestMutationAllowed: definition.RequestMutationAllowed,
		RequestReplayAllowed:   definition.RequestReplayAllowed,
		CookieAccessAllowed:    definition.CookieAccessAllowed,
		ArbitraryMethodAllowed: definition.ArbitraryMethodAllowed,
		RiskTier:               definition.RiskTier, RequiredGate: definition.RequiredGate,
		PolicyVersion:     RunBrowserCDPPermissionPolicyVersion,
		OperatorConfirmed: confirmed, RequestedBy: strings.TrimSpace(requestedBy),
		Reason: strings.TrimSpace(reason), CreatedAt: at.UTC(),
	}
}

func (s RunBrowserCDPPermissionSnapshot) Validate() error {
	for label, value := range map[string]string{
		"snapshot id": s.ID, "Run id": s.RunID, "Mission id": s.MissionID,
		"requester": s.RequestedBy, "policy version": s.PolicyVersion,
	} {
		if !ValidAgentID(value) || strings.ContainsRune(value, 0) {
			return fmt.Errorf(
				"Run browser CDP permission %s must be normalized and bounded UTF-8", label)
		}
	}
	if s.Revision <= 0 {
		return errors.New("Run browser CDP permission revision must be positive")
	}
	if s.ProtocolVersion != RunBrowserCDPPermissionProtocolVersion {
		return fmt.Errorf("unsupported Run browser CDP permission protocol %q", s.ProtocolVersion)
	}
	definition, ok := runBrowserCDPPermissionDefinitions[s.Mode]
	if !ok {
		return fmt.Errorf("invalid Run browser CDP permission mode %q", s.Mode)
	}
	if s.NavigateAllowed != definition.NavigateAllowed ||
		s.DOMSnapshotAllowed != definition.DOMSnapshotAllowed ||
		s.ScreenshotAllowed != definition.ScreenshotAllowed ||
		s.RequestCaptureAllowed != definition.RequestCaptureAllowed ||
		s.RequestMutationAllowed != definition.RequestMutationAllowed ||
		s.RequestReplayAllowed != definition.RequestReplayAllowed ||
		s.CookieAccessAllowed != definition.CookieAccessAllowed ||
		s.ArbitraryMethodAllowed != definition.ArbitraryMethodAllowed ||
		s.RiskTier != definition.RiskTier || s.RequiredGate != definition.RequiredGate ||
		s.OperatorConfirmed != definition.OperatorConfirmed {
		return errors.New("Run browser CDP controls do not match the selected mode")
	}
	if s.PolicyVersion != RunBrowserCDPPermissionPolicyVersion {
		return fmt.Errorf("unsupported Run browser CDP permission policy %q", s.PolicyVersion)
	}
	if s.TransportEnabled || s.BrowserStartAuthorized || s.RuntimeAuthorized ||
		s.CapabilityGrant {
		return errors.New("Run browser CDP permission selection cannot grant runtime authority")
	}
	if !utf8.ValidString(s.Reason) || strings.TrimSpace(s.Reason) != s.Reason ||
		s.Reason == "" || utf8.RuneCountInString(s.Reason) >
		MaxRunBrowserCDPPermissionReasonRunes || strings.ContainsRune(s.Reason, 0) {
		return fmt.Errorf(
			"Run browser CDP permission reason must contain between 1 and %d normalized UTF-8 characters",
			MaxRunBrowserCDPPermissionReasonRunes)
	}
	if s.CreatedAt.IsZero() {
		return errors.New("Run browser CDP permission creation time is required")
	}
	return nil
}

func (s RunBrowserCDPPermissionSnapshot) Next(id string,
	mode RunBrowserCDPPermissionMode, confirmed bool, requestedBy string,
	reason string, at time.Time,
) (RunBrowserCDPPermissionSnapshot, error) {
	if err := s.Validate(); err != nil {
		return RunBrowserCDPPermissionSnapshot{}, err
	}
	if !mode.Valid() {
		return RunBrowserCDPPermissionSnapshot{}, fmt.Errorf(
			"invalid Run browser CDP permission mode %q", mode)
	}
	if mode == s.Mode {
		return RunBrowserCDPPermissionSnapshot{}, errors.New(
			"Run browser CDP permission transition must change mode")
	}
	next := newRunBrowserCDPPermissionSnapshot(id, s.RunID, s.MissionID,
		s.Revision+1, mode, confirmed, requestedBy, reason, at)
	if err := next.Validate(); err != nil {
		return RunBrowserCDPPermissionSnapshot{}, err
	}
	if next.CreatedAt.Before(s.CreatedAt) {
		return RunBrowserCDPPermissionSnapshot{}, errors.New(
			"Run browser CDP permission transition time cannot move backwards")
	}
	return next, nil
}

func CanChangeRunBrowserCDPPermission(status RunStatus) bool {
	return status == RunCreated || status == RunPaused
}

type RunBrowserCDPPermissionOperation struct {
	KeyDigest          string
	RequestFingerprint string
	SnapshotID         string
	RunID              string
	RequestedBy        string
	CreatedAt          time.Time
}

func (o RunBrowserCDPPermissionOperation) Validate() error {
	if !validLowerHexDigest(o.KeyDigest) || !validLowerHexDigest(o.RequestFingerprint) {
		return errors.New(
			"Run browser CDP permission operation digests must be lowercase SHA-256")
	}
	for label, value := range map[string]string{
		"snapshot id": o.SnapshotID, "Run id": o.RunID, "requester": o.RequestedBy,
	} {
		if !ValidAgentID(value) || strings.ContainsRune(value, 0) {
			return fmt.Errorf(
				"Run browser CDP permission operation %s must be normalized and bounded UTF-8",
				label)
		}
	}
	if o.CreatedAt.IsZero() {
		return errors.New("Run browser CDP permission operation creation time is required")
	}
	return nil
}
