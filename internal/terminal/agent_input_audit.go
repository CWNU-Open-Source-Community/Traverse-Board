package terminal

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"cyberagent-workbench/internal/domain"
)

const AgentInputAuditProtocolVersion = "terminal_agent_input_audit.v1"

type AgentInputAuditKind string

const (
	AgentInputAuditGranted   AgentInputAuditKind = "granted"
	AgentInputAuditPrepared  AgentInputAuditKind = "prepared"
	AgentInputAuditCompleted AgentInputAuditKind = "completed"
	AgentInputAuditRevoked   AgentInputAuditKind = "revoked"
)

// AgentInputAuditRecord contains only binding metadata and content digests.
// Bearer tokens and terminal input bytes must never enter this record.
type AgentInputAuditRecord struct {
	ID                       string
	ProtocolVersion          string
	Kind                     AgentInputAuditKind
	RunID                    string
	MissionID                string
	SessionID                string
	WorkspaceID              string
	TerminalSessionID        string
	BindingID                string
	InteractionSnapshotID    string
	InteractionRevision      int64
	ExecutionProfileRevision int64
	PermissionSnapshotID     string
	PermissionRevision       int64
	PermissionMode           domain.RunExecutionPermissionMode
	RequestedBy              string
	OperationDigest          string
	DataSHA256               string
	DataBytes                int
	BytesWritten             int
	ProcessLocal             bool
	TokenPersisted           bool
	TokenExposed             bool
	RawInputPersisted        bool
	AutomaticRetryAllowed    bool
	CreatedAt                time.Time
}

func (r AgentInputAuditRecord) Validate() error {
	for _, value := range []string{
		r.ID, r.RunID, r.MissionID, r.SessionID, r.WorkspaceID,
		r.TerminalSessionID, r.BindingID, r.InteractionSnapshotID,
		r.PermissionSnapshotID, r.RequestedBy,
	} {
		if !domain.ValidAgentID(value) || strings.ContainsRune(value, 0) {
			return ErrTerminalBoundary
		}
	}
	if r.ProtocolVersion != AgentInputAuditProtocolVersion ||
		r.InteractionRevision <= 0 || r.ExecutionProfileRevision <= 0 ||
		r.PermissionRevision <= 0 ||
		r.PermissionMode != domain.RunExecutionPermissionDebug ||
		!validTerminalOperator(r.RequestedBy) || !r.ProcessLocal ||
		r.TokenPersisted || r.TokenExposed || r.RawInputPersisted ||
		r.AutomaticRetryAllowed || r.CreatedAt.IsZero() {
		return ErrTerminalBoundary
	}
	switch r.Kind {
	case AgentInputAuditGranted, AgentInputAuditRevoked:
		if r.OperationDigest != "" || r.DataSHA256 != "" ||
			r.DataBytes != 0 || r.BytesWritten != 0 {
			return ErrTerminalBoundary
		}
	case AgentInputAuditPrepared:
		if !validAgentInputDigest(r.OperationDigest) ||
			!validAgentInputDigest(r.DataSHA256) ||
			r.DataBytes <= 0 || r.DataBytes > MaxTerminalInputBytes ||
			r.BytesWritten != 0 {
			return ErrTerminalBoundary
		}
	case AgentInputAuditCompleted:
		if !validAgentInputDigest(r.OperationDigest) ||
			!validAgentInputDigest(r.DataSHA256) ||
			r.DataBytes <= 0 || r.DataBytes > MaxTerminalInputBytes ||
			r.BytesWritten <= 0 || r.BytesWritten > r.DataBytes {
			return ErrTerminalBoundary
		}
	default:
		return errors.New("unsupported terminal Agent-input audit kind")
	}
	return nil
}

func AgentInputDataDigest(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func validAgentInputDigest(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}
