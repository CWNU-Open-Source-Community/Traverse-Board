package app

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"cyberagent-workbench/internal/mcp"
	"cyberagent-workbench/internal/toolgateway"
)

// mcpCommand serves the Go-owned MCP Server v1 over stdio. The server is a
// local adapter only: it never listens on a socket and forwards every
// typed action through the existing Tool Gateway.
func (a *App) mcpCommand(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "serve" {
		return errors.New("usage: cyberagent mcp serve --run <run-id> --workspace <workspace-id>")
	}
	if err := a.ensureStore(); err != nil {
		return err
	}
	fs := newFlagSet("mcp serve", a.errOut)
	runID := fs.String("run", "", "exact Run identity the MCP scope is bound to")
	workspaceID := fs.String("workspace", "", "exact Workspace identity the MCP scope is bound to")
	maxConcurrent := fs.Int("max-concurrent", 8, "maximum in-flight requests from 1 to 16")
	callTimeout := fs.Duration("call-timeout", 30*time.Second, "per-request timeout")
	sessionTTL := fs.Duration("session-ttl", 24*time.Hour, "capability TTL after initialize")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 || strings.TrimSpace(*runID) == "" || strings.TrimSpace(*workspaceID) == "" {
		return errors.New("usage: cyberagent mcp serve --run <run-id> --workspace <workspace-id>")
	}
	gateway := toolgateway.New(a.store, a.checker)
	server, err := mcp.New(mcp.Options{Store: a.store, Tools: gateway,
		RunID: *runID, WorkspaceID: *workspaceID, SessionTTL: *sessionTTL,
		CallTimeout: *callTimeout, MaxConcurrent: *maxConcurrent})
	if err != nil {
		return err
	}
	return server.Serve(ctx, os.Stdin, os.Stdout)
}
