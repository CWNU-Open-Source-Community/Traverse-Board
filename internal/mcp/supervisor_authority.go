package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"cyberagent-workbench/internal/domain"
)

const SupervisorCallAuthorityVersion = 1

// SupervisorCallAuthority is the durable, fail-closed execution authority
// attached to an mcp_tool_call in the Supervisor ledger. It contains only
// scope and revocation facts; server credentials and tool arguments never
// belong in this envelope.
type SupervisorCallAuthority struct {
	Version               int                               `json:"version"`
	RunID                 string                            `json:"run_id"`
	MissionID             string                            `json:"mission_id"`
	WorkspaceID           string                            `json:"workspace_id"`
	PermissionSnapshotID  string                            `json:"permission_snapshot_id"`
	PermissionRevision    int64                             `json:"permission_revision"`
	PermissionMode        domain.RunExecutionPermissionMode `json:"permission_mode"`
	PermissionGeneration  uint64                            `json:"permission_generation"`
	RunAuthorizationFence uint64                            `json:"run_authorization_fence"`
}

func (a SupervisorCallAuthority) Validate() error {
	if a.Version != SupervisorCallAuthorityVersion || !domain.ValidAgentID(a.RunID) ||
		!domain.ValidAgentID(a.MissionID) || !domain.ValidAgentID(a.WorkspaceID) ||
		!domain.ValidAgentID(a.PermissionSnapshotID) || a.PermissionRevision < 1 ||
		!a.PermissionMode.IncludesFullAccess() {
		return errors.New("Supervisor MCP authority is invalid")
	}
	return nil
}

func EncodeSupervisorCallAuthority(authority SupervisorCallAuthority) (json.RawMessage, error) {
	if err := authority.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(authority)
}

func DecodeSupervisorCallAuthority(raw json.RawMessage) (SupervisorCallAuthority, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var authority SupervisorCallAuthority
	if err := decoder.Decode(&authority); err != nil {
		return SupervisorCallAuthority{}, errors.New("Supervisor MCP authority is malformed")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return SupervisorCallAuthority{}, errors.New("Supervisor MCP authority contains trailing data")
	}
	return authority, authority.Validate()
}
