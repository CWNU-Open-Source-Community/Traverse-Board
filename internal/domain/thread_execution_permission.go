package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const ThreadExecutionPermissionProtocolVersion = "thread_execution_permission.v1"

// ThreadExecutionPermissionSnapshot is one durable, non-authorizing
// permission preference for a Thread's current and future Runs. It deliberately
// mirrors the closed Run permission policy fields so a later policy-table
// change cannot silently reinterpret an operator decision.
type ThreadExecutionPermissionSnapshot struct {
	ID                  string
	ThreadID            string
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

func NewInitialThreadExecutionPermissionSnapshot(id string, thread Thread,
	requestedBy string, at time.Time,
) (ThreadExecutionPermissionSnapshot, error) {
	snapshot := newThreadExecutionPermissionSnapshot(id, thread.ID, thread.MissionID, 1,
		RunExecutionPermissionConservative, false, requestedBy,
		"initial conservative Thread execution permission", at)
	if err := snapshot.Validate(); err != nil {
		return ThreadExecutionPermissionSnapshot{}, err
	}
	return snapshot, nil
}

func newThreadExecutionPermissionSnapshot(id, threadID, missionID string, revision int64,
	mode RunExecutionPermissionMode, confirmed bool, requestedBy, reason string,
	at time.Time,
) ThreadExecutionPermissionSnapshot {
	definition := runExecutionPermissionDefinitions[mode]
	return ThreadExecutionPermissionSnapshot{
		ID: strings.TrimSpace(id), ThreadID: strings.TrimSpace(threadID),
		MissionID: strings.TrimSpace(missionID), Revision: revision,
		ProtocolVersion: ThreadExecutionPermissionProtocolVersion, Mode: mode,
		ApprovalPolicy: definition.ApprovalPolicy, CommandScope: definition.CommandScope,
		FilesystemScope: definition.FilesystemScope, NetworkScope: definition.NetworkScope,
		PersistentTerminal: definition.PersistentTerminal,
		BackgroundProcess:  definition.BackgroundProcess,
		AgentTerminalInput: definition.AgentTerminalInput, RiskTier: definition.RiskTier,
		RequiredGate:      definition.RequiredGate,
		PolicyVersion:     RunExecutionPermissionPolicyVersion,
		OperatorConfirmed: confirmed, RequestedBy: strings.TrimSpace(requestedBy),
		Reason: strings.TrimSpace(reason), CreatedAt: at.UTC(),
	}
}

func (s ThreadExecutionPermissionSnapshot) Validate() error {
	for label, value := range map[string]string{
		"snapshot id": s.ID, "Thread id": s.ThreadID, "Mission id": s.MissionID,
		"requester": s.RequestedBy, "policy version": s.PolicyVersion,
	} {
		if !ValidAgentID(value) || strings.ContainsRune(value, 0) {
			return fmt.Errorf("Thread execution permission %s must be normalized and bounded UTF-8", label)
		}
	}
	if s.Revision <= 0 {
		return errors.New("Thread execution permission revision must be positive")
	}
	if s.ProtocolVersion != ThreadExecutionPermissionProtocolVersion {
		return fmt.Errorf("unsupported Thread execution permission protocol %q", s.ProtocolVersion)
	}
	definition, ok := runExecutionPermissionDefinitions[s.Mode]
	if !ok {
		return fmt.Errorf("invalid Thread execution permission mode %q", s.Mode)
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
		return errors.New("Thread execution permission controls do not match the selected mode")
	}
	if s.PolicyVersion != RunExecutionPermissionPolicyVersion {
		return fmt.Errorf("unsupported Thread execution permission policy %q", s.PolicyVersion)
	}
	if s.ProcessEnabled || s.ExecutionAuthorized || s.CapabilityGrant {
		return errors.New("Thread execution permission preference cannot grant runtime authority")
	}
	if !utf8.ValidString(s.Reason) || strings.TrimSpace(s.Reason) != s.Reason ||
		s.Reason == "" || utf8.RuneCountInString(s.Reason) > MaxRunExecutionPermissionReasonRunes ||
		strings.ContainsRune(s.Reason, 0) {
		return fmt.Errorf("Thread execution permission reason must contain between 1 and %d normalized UTF-8 characters", MaxRunExecutionPermissionReasonRunes)
	}
	if s.CreatedAt.IsZero() {
		return errors.New("Thread execution permission creation time is required")
	}
	return nil
}

func (s ThreadExecutionPermissionSnapshot) CapabilityMatrix() (
	ExecutionPermissionCapabilityMatrix, error,
) {
	if err := s.Validate(); err != nil {
		return ExecutionPermissionCapabilityMatrix{}, err
	}
	return runExecutionPermissionDefinitions[s.Mode].CapabilityMatrix, nil
}

func (s ThreadExecutionPermissionSnapshot) Next(id string,
	mode RunExecutionPermissionMode, confirmed bool, requestedBy, reason string,
	at time.Time,
) (ThreadExecutionPermissionSnapshot, error) {
	if err := s.Validate(); err != nil {
		return ThreadExecutionPermissionSnapshot{}, err
	}
	if !mode.Valid() {
		return ThreadExecutionPermissionSnapshot{}, fmt.Errorf(
			"invalid Thread execution permission mode %q", mode)
	}
	next := newThreadExecutionPermissionSnapshot(id, s.ThreadID, s.MissionID,
		s.Revision+1, mode, confirmed, requestedBy, reason, at)
	if err := next.Validate(); err != nil {
		return ThreadExecutionPermissionSnapshot{}, err
	}
	if next.CreatedAt.Before(s.CreatedAt) {
		return ThreadExecutionPermissionSnapshot{}, errors.New(
			"Thread execution permission transition time cannot move backwards")
	}
	return next, nil
}

type ThreadExecutionPermissionOperation struct {
	KeyDigest                      string
	RequestFingerprint             string
	SnapshotID                     string
	ThreadID                       string
	RequestedBy                    string
	CurrentRunID                   string
	CurrentRunEffect               ThreadExecutionPermissionCurrentRunEffect
	CurrentRunPermissionSnapshotID string
	CreatedAt                      time.Time
}

type ThreadExecutionPermissionCurrentRunEffect string

const (
	ThreadExecutionPermissionApplied          ThreadExecutionPermissionCurrentRunEffect = "applied"
	ThreadExecutionPermissionPausedAndApplied ThreadExecutionPermissionCurrentRunEffect = "paused_and_applied"
	ThreadExecutionPermissionNoActiveRun      ThreadExecutionPermissionCurrentRunEffect = "no_active_run"
)

func (o ThreadExecutionPermissionOperation) Validate() error {
	if !validLowerHexDigest(o.KeyDigest) || !validLowerHexDigest(o.RequestFingerprint) {
		return errors.New("Thread execution permission operation digests must be lowercase SHA-256")
	}
	for label, value := range map[string]string{
		"snapshot id": o.SnapshotID, "Thread id": o.ThreadID, "requester": o.RequestedBy,
	} {
		if !ValidAgentID(value) || strings.ContainsRune(value, 0) {
			return fmt.Errorf("Thread execution permission operation %s must be normalized and bounded UTF-8", label)
		}
	}
	if o.CreatedAt.IsZero() {
		return errors.New("Thread execution permission operation creation time is required")
	}
	if o.CurrentRunEffect == "" && o.CurrentRunID == "" &&
		o.CurrentRunPermissionSnapshotID == "" {
		// A service-level intent is completed atomically by the Store.
		return nil
	}
	switch o.CurrentRunEffect {
	case ThreadExecutionPermissionNoActiveRun:
		if o.CurrentRunID != "" || o.CurrentRunPermissionSnapshotID != "" {
			return errors.New("no-active-Run Thread permission effect cannot bind a Run")
		}
	case ThreadExecutionPermissionApplied, ThreadExecutionPermissionPausedAndApplied:
		if !ValidAgentID(o.CurrentRunID) ||
			!ValidAgentID(o.CurrentRunPermissionSnapshotID) ||
			strings.ContainsRune(o.CurrentRunID, 0) ||
			strings.ContainsRune(o.CurrentRunPermissionSnapshotID, 0) {
			return errors.New("Thread permission current Run effect identities are invalid")
		}
	default:
		return errors.New("Thread execution permission current Run effect is invalid")
	}
	return nil
}
