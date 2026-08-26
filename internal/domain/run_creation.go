package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"cyberagent-workbench/internal/durableoperation"
)

const (
	RunCreationProtocolVersion = "run_creation.v1"
	MaxRunCreationGoalBytes    = 4096
	runCreationReplayDomain    = "run_creation_operation.v1"
)

// RunCreationOperation is the durable, digest-only replay fact for one
// operator-controlled Mission/Run/Session creation transaction.
type RunCreationOperation struct {
	ProtocolVersion    string
	KeyDigest          string
	RequestFingerprint string
	MissionID          string
	RunID              string
	SessionID          string
	WorkspaceID        string
	RequestedBy        string
	CreatedAt          time.Time
}

func (o RunCreationOperation) Validate() error {
	if o.ProtocolVersion != RunCreationProtocolVersion {
		return fmt.Errorf("unsupported Run creation protocol %q", o.ProtocolVersion)
	}
	if _, err := o.ReplayIdentity(); err != nil {
		return errors.New("Run creation operation digests must be lowercase SHA-256")
	}
	identities := []struct {
		label string
		value string
	}{
		{label: "Mission id", value: o.MissionID},
		{label: "Run id", value: o.RunID},
		{label: "Session id", value: o.SessionID},
		{label: "Workspace id", value: o.WorkspaceID},
		{label: "requester", value: o.RequestedBy},
	}
	for _, identity := range identities {
		if !ValidAgentID(identity.value) || strings.ContainsRune(identity.value, 0) {
			return fmt.Errorf("Run creation operation %s must be normalized and bounded UTF-8",
				identity.label)
		}
	}
	if o.CreatedAt.IsZero() {
		return errors.New("Run creation operation creation time is required")
	}
	return nil
}

// ReplayIdentity returns only the storage-free identity subset shared by the
// durable-operation pilot. Mission, Run, Session, Workspace, requester, time,
// and transaction bindings remain owned by Run creation.
func (o RunCreationOperation) ReplayIdentity() (durableoperation.Identity, error) {
	if o.ProtocolVersion != RunCreationProtocolVersion {
		return durableoperation.Identity{}, fmt.Errorf(
			"unsupported Run creation protocol %q", o.ProtocolVersion)
	}
	return durableoperation.NewIdentity(runCreationReplayDomain, o.KeyDigest,
		o.RequestFingerprint)
}
