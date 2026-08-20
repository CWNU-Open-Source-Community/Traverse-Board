package application

import (
	"context"
	"errors"

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
	client SupervisorMCPClient
}

func NewMCPClientToolExecutor(client SupervisorMCPClient) (*MCPClientToolExecutor, error) {
	if client == nil {
		return nil, errors.New("MCP client runtime is required")
	}
	return &MCPClientToolExecutor{client: client}, nil
}

func (e *MCPClientToolExecutor) ExecuteMCP(ctx context.Context,
	scope toolgateway.MCPExecutionScope, payload toolgateway.MCPToolCallPayload,
) (toolgateway.MCPExecutionResult, error) {
	if e == nil || e.client == nil {
		return toolgateway.MCPExecutionResult{}, errors.New("MCP client runtime is unavailable")
	}
	if err := scope.Validate(); err != nil {
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
	return toolgateway.MCPExecutionResult{
		Content: result.Content, IsError: result.IsError, Truncated: result.Truncated,
		Metadata: map[string]string{"trust": "untrusted", "source": "mcp_client"},
	}, nil
}
