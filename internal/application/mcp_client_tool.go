package application

import (
	"context"
	"errors"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/mcp"
	"cyberagent-workbench/internal/toolgateway"
)

// SupervisorMCPClient is the narrow application boundary around the Go-owned
// MCP runtime. It deliberately excludes staging and review operations: model
// execution can observe only the capabilities already enabled by an operator.
type SupervisorMCPClient interface {
	Capabilities(context.Context, string, string) (mcp.ScopedCapabilities, error)
	Invoke(context.Context, mcp.InvokeRequest) (mcp.ClientCallResult, error)
}

type MCPClientToolExecutor struct {
	client       SupervisorMCPClient
	store        MCPExecutionPermissionStore
	capabilities domain.ExecutionPermissionRuntimeCapabilities
}

type MCPExecutionPermissionStore interface {
	GetRun(context.Context, string) (domain.Run, error)
	GetMission(context.Context, string) (domain.Mission, error)
	GetRunExecutionPermission(context.Context, string) (
		domain.RunExecutionPermissionSnapshot, error)
	GetRunExecutionLease(context.Context, string) (domain.RunExecutionLease, bool, error)
}

func NewMCPClientToolExecutor(client SupervisorMCPClient,
	store MCPExecutionPermissionStore,
	capabilities domain.ExecutionPermissionRuntimeCapabilities,
) (*MCPClientToolExecutor, error) {
	if client == nil || store == nil {
		return nil, errors.New("MCP client runtime and execution permission store are required")
	}
	if err := capabilities.Validate(); err != nil {
		return nil, err
	}
	return &MCPClientToolExecutor{client: client, store: store,
		capabilities: capabilities}, nil
}

func (e *MCPClientToolExecutor) ExecuteMCP(ctx context.Context,
	scope toolgateway.MCPExecutionScope, payload toolgateway.MCPToolCallPayload,
) (toolgateway.MCPExecutionResult, error) {
	if e == nil || e.client == nil || e.store == nil {
		return toolgateway.MCPExecutionResult{}, errors.New("MCP client runtime is unavailable")
	}
	if err := scope.Validate(); err != nil {
		return toolgateway.MCPExecutionResult{}, err
	}
	if err := e.recheckExecutionScope(ctx, scope); err != nil {
		return toolgateway.MCPExecutionResult{}, err
	}
	result, err := e.client.Invoke(ctx, mcp.InvokeRequest{
		RunID: scope.RunID, WorkspaceID: scope.WorkspaceID,
		ServerID: payload.ServerID, ToolName: payload.ToolName,
		CapabilityFingerprint: payload.CapabilityFingerprint,
		Arguments:             payload.Arguments,
	})
	if err != nil {
		return toolgateway.MCPExecutionResult{}, err
	}
	// Revocation during transport cannot undo a remote side effect. Reject its
	// result under stale authority without retrying the invocation.
	if err := e.recheckExecutionScope(ctx, scope); err != nil {
		return toolgateway.MCPExecutionResult{}, apperror.Wrap(apperror.CodeOf(apperror.Normalize(err)),
			"MCP call was dispatched but its authority changed before the result was accepted; do not automatically repeat the action", err)
	}
	return toolgateway.MCPExecutionResult{
		Content: result.Content, IsError: result.IsError, Truncated: result.Truncated,
		Metadata: map[string]string{"trust": "untrusted", "source": "mcp_client"},
	}, nil
}

// mcpRuntimeAuthorityCurrent binds a numeric Run fence to the authority
// instance that issued it. Historical unfenced calls are supported only by a
// legacy host with no runtime authority; they are never upgraded on replay.
func mcpRuntimeAuthorityCurrent(capabilities domain.ExecutionPermissionRuntimeCapabilities,
	runID string, fence uint64, epoch string,
) bool {
	if capabilities.RuntimeAuthority == nil {
		return fence == 0 && epoch == ""
	}
	return epoch != "" && epoch == capabilities.RuntimeAuthority.RuntimeEpoch() &&
		capabilities.RuntimeAuthority.AllowsRunAuthorizationFence(runID, fence)
}

func (e *MCPClientToolExecutor) recheckExecutionScope(ctx context.Context,
	scope toolgateway.MCPExecutionScope,
) error {
	run, err := e.store.GetRun(ctx, scope.RunID)
	if err != nil {
		return apperror.Normalize(err)
	}
	mission, err := e.store.GetMission(ctx, scope.MissionID)
	if err != nil {
		return apperror.Normalize(err)
	}
	permission, err := e.store.GetRunExecutionPermission(ctx, scope.RunID)
	if err != nil {
		return apperror.Normalize(err)
	}
	lease, found, err := e.store.GetRunExecutionLease(ctx, scope.RunID)
	if err != nil {
		return apperror.Normalize(err)
	}
	generation, live := e.capabilities.FullAccessGeneration(permission)
	fenceLive := mcpRuntimeAuthorityCurrent(e.capabilities, scope.RunID,
		scope.RunAuthorizationFence, scope.PermissionRuntimeEpoch)
	if run.ID != scope.RunID || run.MissionID != scope.MissionID || run.Terminal() ||
		mission.ID != scope.MissionID || mission.WorkspaceID != scope.WorkspaceID ||
		permission.ID != scope.PermissionSnapshotID ||
		permission.Revision != scope.PermissionRevision ||
		permission.Mode != scope.PermissionMode || !live || !fenceLive ||
		generation != scope.PermissionGeneration ||
		!found || lease.LeaseID != scope.LeaseID ||
		lease.Generation != scope.LeaseGeneration || !lease.ActiveAt(time.Now().UTC()) {
		return apperror.New(
			apperror.CodeConflict,
			"MCP execution permission, activation generation, or Run lease is stale")
	}
	return nil
}
