package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
)

func TestTerminalSessionLedgerAndAudit(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "terminal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	_, run, err := application.NewRunService(st).Create(ctx, application.CreateRunRequest{
		Goal: "terminal", Profile: "code", Surface: "code", Phase: "plan",
		WorkspaceID: "ws-1", Budget: domain.Budget{MaxTurns: 5, MaxToolCalls: 5},
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	record := TerminalSessionRecord{ID: "term-1", ProtocolVersion: "user_terminal_session.v1",
		RunID: run.ID, WorkspaceID: "ws-1", State: "running", Cwd: "C:\\ws",
		Columns: 120, Rows: 32, CreatedAt: now, LastActivityAt: now}
	if err := st.CreateTerminalSession(ctx, record); err != nil {
		t.Fatal(err)
	}
	record.AgentInputActive = true
	record.State = "running"
	record.LastActivityAt = now.Add(time.Minute)
	if err := st.UpdateTerminalSession(ctx, record); err != nil {
		t.Fatal(err)
	}
	record.State = "closed"
	if err := st.UpdateTerminalSession(ctx, record); err != nil {
		t.Fatal(err)
	}
	// Closed sessions cannot be revived.
	record.State = "running"
	if err := st.UpdateTerminalSession(ctx, record); err == nil {
		t.Fatal("closed terminal session was revived")
	}
	if err := st.RecordTerminalSessionEvent(ctx, run.ID, "terminal.agent_input_issued", map[string]any{
		"actor": "agent", "session_id": "term-1", "ttl_seconds": 300, "secret_value": "should-be-bounded-XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordTerminalSessionEvent(ctx, run.ID, "terminal.hacked", map[string]any{}); err == nil {
		t.Fatal("unknown terminal audit event type accepted")
	}
	timeline, err := st.ListRunEvents(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range timeline {
		if event.Type == "terminal.agent_input_issued" {
			found = true
			if !strings.Contains(event.PayloadJSON, "[bounded]") {
				t.Fatalf("long payload value not bounded: %s", event.PayloadJSON)
			}
		}
	}
	if !found {
		t.Fatal("terminal audit event missing")
	}
}
