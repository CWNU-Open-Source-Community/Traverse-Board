package store

import (
	"cyberagent-workbench/internal/hooks"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSchemaV187PreservesHistoricalHookMeaningAndRecordsVersionBoundDecisions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v186.db")
	old := openHistoricalTestDatabase(t, path, 186)
	at := time.Now().UTC()
	if _, err := old.db.Exec(`INSERT INTO plugin_hook_audits(id,plugin_id,hook_id,event,run_id,workspace_id,tool_name,outcome,created_at) VALUES('old','guard','same-hook','pre_tool','run-old','workspace-one','file_write','completed',?)`, ts(at)); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	state, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	values, err := state.ListHookAudits(t.Context(), "run-old", "workspace-one", 200)
	if err != nil || len(values) != 1 || values[0].Outcome != "completed" || values[0].Rejected != nil || values[0].PluginFingerprint != "" {
		t.Fatalf("historical receipt changed: %#v %v", values, err)
	}
	engine := hooks.NewEngine(state)
	if err := engine.Replace([]hooks.Registration{{PluginID: "guard", PluginFingerprint: strings.Repeat("a", 64), Declaration: hooks.Declaration{ProtocolVersion: hooks.ProtocolVersion, ID: "same-hook", Event: hooks.PreTool, Action: hooks.ActionDeny, FailurePolicy: hooks.FailureDeny, TimeoutMillis: 100, Message: "private extension text"}}}); err != nil {
		t.Fatal(err)
	}
	result, err := engine.Execute(t.Context(), hooks.Input{Event: hooks.PreTool, RunID: "run-new", WorkspaceID: "workspace-one", ToolName: "file_write"})
	if err != nil || !result.Denied {
		t.Fatalf("deny=%#v %v", result, err)
	}
	values, err = state.ListHookAudits(t.Context(), "run-new", "workspace-one", 200)
	if err != nil || len(values) != 1 || values[0].Outcome != "completed" || values[0].Rejected == nil || !*values[0].Rejected || values[0].Action != hooks.ActionDeny || values[0].PluginFingerprint != strings.Repeat("a", 64) {
		t.Fatalf("decision=%#v %v", values, err)
	}
	if values, err := state.ListHookAudits(t.Context(), "run-new", "workspace-two", 200); err != nil || len(values) != 0 {
		t.Fatalf("scope leaked: %#v %v", values, err)
	}
	if values, err := state.ListHookAudits(t.Context(), "", "workspace-one", 200); err != nil || len(values) != 2 {
		t.Fatalf("workspace=%#v %v", values, err)
	}
	if _, err := state.db.Exec(`UPDATE plugin_hook_audits SET rejected=0 WHERE id=?`, values[0].ID); err == nil {
		t.Fatal("immutable receipt changed")
	}
	if _, err := state.db.Exec(`INSERT INTO plugin_hook_audits(id,plugin_id,hook_id,event,outcome,created_at,plugin_fingerprint,declared_action,rejected) VALUES('forged','guard','h','pre_tool','completed',?,'invalid','script',2)`, ts(at)); err == nil {
		t.Fatal("invalid diagnostics accepted")
	}
}
