package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/toolgateway"
)

// toolCatalog is the fixed v1 typed-action set. Every entry forwards into
// the existing Tool Gateway; Policy, Approval, budgets, and redaction stay
// authoritative. Unimplemented tools are never published.
func (s *Server) toolCatalog() []ToolDefinition {
	return []ToolDefinition{
		{Name: "read_file", Description: "Read one bounded workspace file through the existing Tool Gateway (Policy/redaction/budgets stay authoritative).",
			InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["path"],"properties":{"path":{"type":"string","minLength":1,"maxLength":2048}}}`)},
		{Name: "list_workspace", Description: "List one bounded workspace directory through the existing Tool Gateway.",
			InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"path":{"type":"string","minLength":1,"maxLength":2048}}}`)},
	}
}

func (s *Server) serveToolsList(_ context.Context, request Request, output io.Writer) {
	result := ListToolsResult{Tools: s.toolCatalog()}
	_ = s.write(output, Envelope{JSONRPC: "2.0", ID: request.ID, Result: mustJSON(result)})
}

func (s *Server) serveToolsCall(ctx context.Context, request Request, output io.Writer) {
	params, err := DecodeCallToolParams(request.Params)
	if err != nil {
		_ = s.write(output, Envelope{JSONRPC: "2.0", ID: request.ID,
			Error: &RPCError{Code: CodeInvalidParams, Message: err.Error()}})
		return
	}
	name := toolgateway.ToolName(params.Name)
	if name != toolgateway.ReadFileTool && name != toolgateway.ListWorkspaceTool {
		_ = s.write(output, Envelope{JSONRPC: "2.0", ID: request.ID,
			Error: &RPCError{Code: CodeMethodNotFound, Message: "tool not found in this server"}})
		return
	}
	// The client can only supply the closed tool payload; an arbitrary
	// executable, path, credential, or permission tier has no field to ride on.
	call := toolgateway.ToolCall{Name: name, Payload: params.Arguments,
		InvocationID: idgen.New("mcp-call"), OperationKey: string(request.ID),
		RunID: s.runID, SessionID: "", WorkspaceID: s.workspaceID,
		RequestedBy: "mcp:" + s.clientName}
	if _, err := s.store.GetRun(ctx, s.runID); err != nil {
		_ = s.write(output, Envelope{JSONRPC: "2.0", ID: request.ID,
			Error: &RPCError{Code: CodeInternalError, Message: "Run scope unavailable"}})
		return
	}
	outcome, err := s.tools.Invoke(ctx, call)
	if err != nil {
		reason := "invocation_failed"
		message := "tool invocation failed"
		if errors.Is(err, context.DeadlineExceeded) {
			reason = "timed_out"
			message = "tool call timed out"
		} else if errors.Is(err, context.Canceled) {
			reason = "cancelled"
			message = "tool call cancelled"
		}
		_ = s.write(output, Envelope{JSONRPC: "2.0", ID: request.ID,
			Error: &RPCError{Code: CodeInternalError, Message: message}})
		_ = s.store.RecordMCPAudit(context.WithoutCancel(ctx), s.runID, "mcp.tool_denied", map[string]any{
			"tool": params.Name, "reason": reason})
		return
	}
	if outcome.Result == nil {
		_ = s.write(output, Envelope{JSONRPC: "2.0", ID: request.ID,
			Error: &RPCError{Code: CodeInternalError, Message: outcome.Decision.Reason}})
		_ = s.store.RecordMCPAudit(ctx, s.runID, "mcp.tool_denied", map[string]any{
			"tool": params.Name, "reason": outcome.Decision.Reason})
		return
	}
	// Only the bounded redacted status and metadata travel back; raw tool
	// output bodies stay inside the gateway redaction boundary.
	text := fmt.Sprintf(`{"status":%q,"exit_code":%d,"summary":%q}`,
		outcome.Result.Status, outcome.Result.ExitCode, outcome.Result.Metadata["summary"])
	if len(outcome.Result.Metadata) > 0 {
		encoded, err := json.Marshal(outcome.Result.Metadata)
		if err == nil && len(encoded) <= 8192 {
			text = fmt.Sprintf(`{"status":%q,"exit_code":%d,"metadata":%s}`,
				outcome.Result.Status, outcome.Result.ExitCode, encoded)
		}
	}
	_ = s.write(output, Envelope{JSONRPC: "2.0", ID: request.ID,
		Result: mustJSON(CallToolResult{Content: []TextContent{{Type: "text", Text: text}}})})
	_ = s.store.RecordMCPAudit(context.WithoutCancel(ctx), s.runID, "mcp.tool_completed", map[string]any{
		"tool": params.Name, "status": outcome.Result.Status})
}

var _ = domain.ValidAgentID
var _ = fmt.Sprintf
