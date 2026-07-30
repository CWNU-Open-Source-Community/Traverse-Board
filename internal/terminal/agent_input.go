package terminal

import (
	"context"
	"errors"
	"time"

	"cyberagent-workbench/internal/executionauth"
)

const AgentInputBridgeProtocolVersion = "terminal_agent_input_bridge.v1"

var ErrAgentInputBridgeDenied = errors.New("terminal Agent input bridge denied the request")

type AgentInputBridge struct {
	manager *Manager
	broker  *executionauth.TerminalInputBroker
}

type IssueAgentInputRequest struct {
	SessionID         string
	RequestedBy       string
	OperatorConfirmed bool
	TTL               time.Duration
}

type IssuedAgentInput struct {
	ProtocolVersion string
	Lease           executionauth.TerminalInputLease
	Token           string
}

type AgentWriteRequest struct {
	Token string
	Scope executionauth.TerminalInputScope
	Data  []byte
}

type AgentWriteResult struct {
	ProtocolVersion string
	LeaseID         string
	SessionID       string
	BytesWritten    int
}

func NewAgentInputBridge(manager *Manager,
	broker *executionauth.TerminalInputBroker,
) (*AgentInputBridge, error) {
	if manager == nil || broker == nil {
		return nil, ErrTerminalBoundary
	}
	return &AgentInputBridge{manager: manager, broker: broker}, nil
}

func (b *AgentInputBridge) Issue(ctx context.Context,
	request IssueAgentInputRequest,
) (IssuedAgentInput, error) {
	if b == nil || b.manager == nil || b.broker == nil ||
		ctx == nil || ctx.Err() != nil {
		return IssuedAgentInput{}, ErrAgentInputBridgeDenied
	}
	session, err := b.manager.Get(request.SessionID)
	if err != nil || session.State != SessionRunning ||
		session.AgentInputDefault || !session.JobAssignedAtCreation ||
		!session.KillOnJobClose {
		return IssuedAgentInput{}, ErrAgentInputBridgeDenied
	}
	scope := executionauth.TerminalInputScope{
		WorkspaceID: session.Scope.WorkspaceID, RunID: session.Scope.RunID,
		TerminalSessionID:        session.ID,
		InteractionSnapshotID:    session.Scope.InteractionSnapshotID,
		InteractionRevision:      session.Scope.InteractionRevision,
		ExecutionProfileRevision: session.Scope.ExecutionProfileRevision,
		PermissionSnapshotID:     session.Scope.PermissionSnapshotID,
		PermissionRevision:       session.Scope.PermissionRevision,
		PermissionMode:           session.Scope.PermissionMode,
		Mode:                     session.Scope.Mode,
	}
	issued, err := b.broker.Issue(executionauth.IssueTerminalInputLeaseRequest{
		Scope: scope, RequestedBy: request.RequestedBy,
		OperatorConfirmed: request.OperatorConfirmed, TTL: request.TTL,
	})
	if err != nil {
		return IssuedAgentInput{}, err
	}
	return IssuedAgentInput{
		ProtocolVersion: AgentInputBridgeProtocolVersion,
		Lease:           issued.Lease, Token: issued.Token,
	}, nil
}

func (b *AgentInputBridge) Write(ctx context.Context,
	request AgentWriteRequest,
) (AgentWriteResult, error) {
	if b == nil || b.manager == nil || b.broker == nil ||
		ctx == nil || ctx.Err() != nil ||
		validateTerminalInput(request.Data) != nil {
		return AgentWriteResult{}, ErrAgentInputBridgeDenied
	}
	lease, err := b.broker.Authorize(request.Token, request.Scope)
	if err != nil {
		return AgentWriteResult{}, err
	}
	session, err := b.manager.Get(request.Scope.TerminalSessionID)
	if err != nil || session.State != SessionRunning ||
		session.Scope.WorkspaceID != request.Scope.WorkspaceID ||
		session.Scope.RunID != request.Scope.RunID ||
		session.Scope.InteractionSnapshotID !=
			request.Scope.InteractionSnapshotID ||
		session.Scope.InteractionRevision != request.Scope.InteractionRevision ||
		session.Scope.ExecutionProfileRevision !=
			request.Scope.ExecutionProfileRevision ||
		session.Scope.PermissionSnapshotID !=
			request.Scope.PermissionSnapshotID ||
		session.Scope.PermissionRevision != request.Scope.PermissionRevision ||
		session.Scope.PermissionMode != request.Scope.PermissionMode ||
		session.Scope.Mode != request.Scope.Mode ||
		session.ID != lease.Scope.TerminalSessionID {
		return AgentWriteResult{}, ErrAgentInputBridgeDenied
	}
	count, err := b.manager.writeAuthorized(session.ID, request.Data)
	if err != nil {
		return AgentWriteResult{}, err
	}
	return AgentWriteResult{
		ProtocolVersion: AgentInputBridgeProtocolVersion,
		LeaseID:         lease.ID, SessionID: session.ID, BytesWritten: count,
	}, nil
}

func (b *AgentInputBridge) Revoke(leaseID string, requestedBy string,
	operatorConfirmed bool,
) (executionauth.TerminalInputLease, error) {
	if b == nil || b.broker == nil {
		return executionauth.TerminalInputLease{}, ErrAgentInputBridgeDenied
	}
	return b.broker.Revoke(leaseID, requestedBy, operatorConfirmed)
}
