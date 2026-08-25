package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const StandardCodePresetProtocolVersion = "standard_code_preset.v1"

type StandardCodeBackendIntent string

const (
	StandardCodeBackendAuto   StandardCodeBackendIntent = "auto"
	StandardCodeBackendLocal  StandardCodeBackendIntent = "local"
	StandardCodeBackendDocker StandardCodeBackendIntent = "docker"
)

func ParseStandardCodeBackendIntent(value string) (StandardCodeBackendIntent, error) {
	intent := StandardCodeBackendIntent(strings.ToLower(strings.TrimSpace(value)))
	switch intent {
	case StandardCodeBackendAuto, StandardCodeBackendLocal, StandardCodeBackendDocker:
		return intent, nil
	default:
		return "", fmt.Errorf("unsupported Standard Code backend intent %q", value)
	}
}

func (i StandardCodeBackendIntent) Valid() bool {
	parsed, err := ParseStandardCodeBackendIntent(string(i))
	return err == nil && parsed == i
}

type StandardCodeBackend string

const (
	StandardCodeSelectedLocal  StandardCodeBackend = "local"
	StandardCodeSelectedDocker StandardCodeBackend = "docker"
)

func (b StandardCodeBackend) Valid() bool {
	return b == StandardCodeSelectedLocal || b == StandardCodeSelectedDocker
}

func (b StandardCodeBackend) ExecutionProfile() RunExecutionProfile {
	if b == StandardCodeSelectedDocker {
		return RunExecutionProfileDocker
	}
	if b == StandardCodeSelectedLocal {
		return RunExecutionProfileLocal
	}
	return ""
}

type StandardCodeSelectionReason string

const (
	StandardCodeReasonAutoLocalReady StandardCodeSelectionReason = "auto_local_ready"
	StandardCodeReasonExplicitLocal  StandardCodeSelectionReason = "explicit_local"
	StandardCodeReasonExplicitDocker StandardCodeSelectionReason = "explicit_docker"
)

func (r StandardCodeSelectionReason) Valid() bool {
	switch r {
	case StandardCodeReasonAutoLocalReady, StandardCodeReasonExplicitLocal,
		StandardCodeReasonExplicitDocker:
		return true
	default:
		return false
	}
}

type StandardCodePresetAction string

const (
	StandardCodePresetConfigure         StandardCodePresetAction = "configure"
	StandardCodePresetPauseAndConfigure StandardCodePresetAction = "pause_and_configure"
)

func (a StandardCodePresetAction) Valid() bool {
	return a == StandardCodePresetConfigure || a == StandardCodePresetPauseAndConfigure
}

type StandardCodePresetStatus string

const (
	StandardCodePresetPreparing       StandardCodePresetStatus = "preparing"
	StandardCodePresetWaitingForPause StandardCodePresetStatus = "waiting_for_pause"
	StandardCodePresetConfigured      StandardCodePresetStatus = "configured"
)

func (s StandardCodePresetStatus) Valid() bool {
	switch s {
	case StandardCodePresetPreparing, StandardCodePresetWaitingForPause,
		StandardCodePresetConfigured:
		return true
	default:
		return false
	}
}

// StandardCodePresetOperation is an intent-bound, non-authorizing receipt.
// Preparing and waiting rows are a write-ahead record for recoverable Drydock
// work or a requested pause; only Configured binds the complete policy tuple.
type StandardCodePresetOperation struct {
	ProtocolVersion       string
	KeyDigest             string
	RequestFingerprint    string
	RequestedRunID        string
	RunID                 string
	MissionID             string
	WorkspaceID           string
	Action                StandardCodePresetAction
	BackendIntent         StandardCodeBackendIntent
	SelectedBackend       StandardCodeBackend
	SelectionReason       StandardCodeSelectionReason
	Status                StandardCodePresetStatus
	DrydockID             string
	DrydockGeneration     int64
	DrydockCheckpointID   string
	ModeSnapshotID        string
	ProfileSnapshotID     string
	InteractionSnapshotID string
	PermissionSnapshotID  string
	BrowserCDPSnapshotID  string
	EventSequenceStart    int64
	EventSequenceEnd      int64
	RequestedBy           string
	CapabilityGrant       bool
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// StandardCodePresetCommit contains the complete desired snapshot tuple. A
// snapshot may be the current row (no append) or its exact next revision.
type StandardCodePresetCommit struct {
	Operation           StandardCodePresetOperation
	Mode                RunModeSnapshot
	Profile             RunExecutionProfileSnapshot
	Interaction         RunExecutionInteractionSnapshot
	Permission          RunExecutionPermissionSnapshot
	BrowserCDP          RunBrowserCDPPermissionSnapshot
	DrydockID           string
	DrydockGeneration   int64
	DrydockCheckpointID string
	CommittedAt         time.Time
}

func (o StandardCodePresetOperation) Validate() error {
	if o.ProtocolVersion != StandardCodePresetProtocolVersion {
		return fmt.Errorf("unsupported Standard Code preset protocol %q", o.ProtocolVersion)
	}
	if !validLowerHexDigest(o.KeyDigest) || !validLowerHexDigest(o.RequestFingerprint) {
		return errors.New("Standard Code preset operation digests must be lowercase SHA-256")
	}
	for label, value := range map[string]string{
		"Run id": o.RunID, "Mission id": o.MissionID, "Workspace id": o.WorkspaceID,
		"requester": o.RequestedBy,
	} {
		if !ValidAgentID(value) || strings.ContainsRune(value, 0) {
			return fmt.Errorf("Standard Code preset %s must be normalized and bounded UTF-8", label)
		}
	}
	if o.RequestedRunID != "" && (!ValidAgentID(o.RequestedRunID) ||
		strings.ContainsRune(o.RequestedRunID, 0)) {
		return errors.New("Standard Code preset requested Run id is invalid")
	}
	if !o.Action.Valid() || !o.BackendIntent.Valid() || !o.SelectedBackend.Valid() ||
		!o.SelectionReason.Valid() || !o.Status.Valid() {
		return errors.New("Standard Code preset action, backend, reason, or status is invalid")
	}
	if (o.BackendIntent == StandardCodeBackendAuto &&
		(o.SelectedBackend != StandardCodeSelectedLocal ||
			o.SelectionReason != StandardCodeReasonAutoLocalReady)) ||
		(o.BackendIntent == StandardCodeBackendLocal &&
			(o.SelectedBackend != StandardCodeSelectedLocal ||
				o.SelectionReason != StandardCodeReasonExplicitLocal)) ||
		(o.BackendIntent == StandardCodeBackendDocker &&
			(o.SelectedBackend != StandardCodeSelectedDocker ||
				o.SelectionReason != StandardCodeReasonExplicitDocker)) {
		return errors.New("Standard Code preset backend selection does not match its intent")
	}
	if o.EventSequenceStart <= 0 || o.EventSequenceEnd < o.EventSequenceStart {
		return errors.New("Standard Code preset event sequence is invalid")
	}
	if o.CapabilityGrant {
		return errors.New("Standard Code preset receipt cannot grant capability")
	}
	if o.CreatedAt.IsZero() || o.UpdatedAt.IsZero() || o.UpdatedAt.Before(o.CreatedAt) {
		return errors.New("Standard Code preset timestamps are invalid")
	}
	configuredIDs := []string{o.DrydockID, o.DrydockCheckpointID, o.ModeSnapshotID,
		o.ProfileSnapshotID, o.InteractionSnapshotID, o.PermissionSnapshotID,
		o.BrowserCDPSnapshotID}
	if o.Status == StandardCodePresetConfigured {
		if o.DrydockGeneration <= 0 {
			return errors.New("configured Standard Code preset requires a Drydock generation")
		}
		for _, value := range configuredIDs {
			if !ValidAgentID(value) || strings.ContainsRune(value, 0) {
				return errors.New("configured Standard Code preset has an invalid binding")
			}
		}
	} else {
		if o.DrydockGeneration != 0 {
			return errors.New("pending Standard Code preset cannot bind a Drydock generation")
		}
		for _, value := range configuredIDs {
			if value != "" {
				return errors.New("pending Standard Code preset cannot bind final snapshots")
			}
		}
	}
	return nil
}
