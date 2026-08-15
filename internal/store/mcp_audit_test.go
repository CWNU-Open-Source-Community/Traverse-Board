package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
)

func TestRecordMCPAuditAppendsBoundedEvents(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "mcp-audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	runService := application.NewRunService(st)
	_, run, err := runService.Create(ctx, application.CreateRunRequest{
		Goal: "MCP audit", Profile: "review", ModelRoute: "review",
		Budget: domain.Budget{MaxTurns: 2, MaxToolCalls: 5},
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err = runService.Start(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RecordMCPAudit(ctx, run.ID, "mcp.initialized", map[string]any{
		"client_name": "editor", "client_version": "1.2.3",
	}); err != nil {
		t.Fatal(err)
	}
	longText := strings.Repeat("x", 300)
	if err := st.RecordMCPAudit(ctx, run.ID, "mcp.tool_completed", map[string]any{
		"tool": "read_file", "status": "completed", "raw_output": longText,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordMCPAudit(ctx, run.ID, "mcp.tool_denied", map[string]any{
		"tool": "shell", "reason": "not in this server",
	}); err != nil {
		t.Fatal(err)
	}
	events, err := st.ListRunEvents(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) < 3 {
		t.Fatalf("audit events were not appended: %d", len(events))
	}
	var initialized, completed, denied int
	for _, event := range events {
		if event.Source != "mcp_server" {
			continue
		}
		switch event.Type {
		case "mcp.initialized":
			initialized++
			if !strings.Contains(event.PayloadJSON, "\"client_name\":\"editor\"") {
				t.Fatalf("initialized payload lost: %s", event.PayloadJSON)
			}
		case "mcp.tool_completed":
			completed++
			if !strings.Contains(event.PayloadJSON, "\"[bounded]\"") {
				t.Fatalf("long payload string was not bounded: %s", event.PayloadJSON)
			}
			if strings.Contains(event.PayloadJSON, longText) {
				t.Fatal("raw output leaked into the audit payload")
			}
		case "mcp.tool_denied":
			denied++
		}
	}
	if initialized != 1 || completed != 1 || denied != 1 {
		t.Fatalf("unexpected audit events: init=%d completed=%d denied=%d", initialized, completed, denied)
	}
}

func TestRecordMCPAuditRejectsUnknownTypesAndMissingRuns(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "mcp-audit-guard.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	if err := st.RecordMCPAudit(ctx, "run-1", "mcp.shell", map[string]any{"command": "whoami"}); err == nil {
		t.Fatal("undeclared audit event type was accepted")
	}
	if err := st.RecordMCPAudit(ctx, "run-missing", "mcp.initialized", map[string]any{}); err == nil {
		t.Fatal("audit for a missing Run was accepted")
	}
}
