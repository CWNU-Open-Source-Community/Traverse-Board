package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"cyberagent-workbench/internal/contextmgr"
	"cyberagent-workbench/internal/session"
)

func TestContextMemoryLifecyclePersistsAndDeletesData(t *testing.T) {
	ctx := context.Background()
	state, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	now := time.Now().UTC().Truncate(time.Second)
	retention := now.Add(time.Hour)
	memory, err := contextmgr.PrepareMemory(contextmgr.CreateMemoryRequest{
		ID: "memory-store-one", Scope: contextmgr.MemoryScopeProject,
		ScopeID: "workspace-memory", Title: "Validation preference",
		Content:    "Run focused tests before the complete suite.",
		SourceKind: "operator_explicit", SourceRef: "issue-106",
		References: []string{"README.md"}, RetentionUntil: &retention,
		RequestedBy: "cli_operator", ExplicitOperator: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.CreateContextMemory(ctx, memory); err != nil {
		t.Fatal(err)
	}
	loaded, err := state.GetContextMemory(ctx, memory.ID)
	if err != nil || loaded.ContentSHA256 != memory.ContentSHA256 {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
	active, err := state.ListContextMemories(ctx, contextmgr.MemoryFilter{
		Scope: contextmgr.MemoryScopeProject, ScopeID: memory.ScopeID,
	}, now)
	if err != nil || len(active) != 1 {
		t.Fatalf("active=%#v err=%v", active, err)
	}
	disabled := contextmgr.MemoryStatusDisabled
	updated, err := contextmgr.UpdateMemory(loaded, contextmgr.UpdateMemoryRequest{
		Status: &disabled, RequestedBy: "cli_operator", ExpectedVersion: loaded.Version,
	}, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := state.UpdateContextMemory(ctx, updated, loaded.Version); err != nil {
		t.Fatal(err)
	}
	active, err = state.ListContextMemories(ctx, contextmgr.MemoryFilter{
		Scope: contextmgr.MemoryScopeProject, ScopeID: memory.ScopeID,
	}, now)
	if err != nil || len(active) != 0 {
		t.Fatalf("disabled memory leaked into active list: %#v err=%v", active, err)
	}
	all, err := state.ListContextMemories(ctx, contextmgr.MemoryFilter{
		Scope: contextmgr.MemoryScopeProject, ScopeID: memory.ScopeID,
		IncludeDisabled: true, IncludeExpired: true,
	}, retention.Add(time.Hour))
	if err != nil || len(all) != 1 || all[0].Version != 2 {
		t.Fatalf("all=%#v err=%v", all, err)
	}
	deleted, err := state.DeleteContextMemory(ctx, memory.ID, all[0].Version)
	if err != nil || !deleted {
		t.Fatalf("deleted=%t err=%v", deleted, err)
	}
	if _, err := state.GetContextMemory(ctx, memory.ID); err == nil {
		t.Fatal("deleted long-term memory remained readable")
	}
}

func TestContextMemoryStoreEnforcesOptimisticVersionAndActor(t *testing.T) {
	ctx := context.Background()
	state, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	memory, err := contextmgr.PrepareMemory(contextmgr.CreateMemoryRequest{
		ID: "memory-store-version", Scope: contextmgr.MemoryScopeUser,
		ScopeID: contextmgr.LocalUserMemoryScope, Title: "Preference", Content: "Use concise output.",
		RequestedBy: "desktop_operator", ExplicitOperator: true,
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := state.CreateContextMemory(ctx, memory); err != nil {
		t.Fatal(err)
	}
	title := "Changed"
	updated, err := contextmgr.UpdateMemory(memory, contextmgr.UpdateMemoryRequest{
		Title: &title, RequestedBy: "desktop_operator", ExpectedVersion: 1,
	}, time.Now().UTC().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := state.UpdateContextMemory(ctx, updated, 7); err == nil {
		t.Fatal("stale expected version was accepted")
	}
	if _, err := state.db.ExecContext(ctx, `UPDATE context_memories
		SET updated_by = 'model', version = version + 1 WHERE id = ?`, memory.ID); err == nil {
		t.Fatal("database accepted a model-authored memory update")
	}
}

func TestSchemaV114MigrationPreservesLegacyDataAndReopens(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "v113-upgrade.db")
	state, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	createdAt := time.Now().UTC().Truncate(time.Second)
	legacy := session.WorkspaceRecord{ID: "workspace-before-v114", Name: "legacy-context",
		RootPath: t.TempDir(), CreatedAt: createdAt}
	if err := state.SaveWorkspace(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`DROP TABLE session_continuity_nodes`,
		`DROP TABLE run_instruction_snapshots`,
		`DROP TABLE context_memories`,
		`DELETE FROM schema_migrations WHERE version = 114`,
	} {
		if _, err := state.db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("prepare v113 fixture with %q: %v", statement, err)
		}
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := upgraded.GetWorkspaceByID(ctx, legacy.ID)
	if err != nil || loaded.Name != legacy.Name || loaded.RootPath != legacy.RootPath {
		t.Fatalf("legacy data changed during v114 migration: loaded=%#v err=%v", loaded, err)
	}
	if version, err := upgraded.SchemaVersion(ctx); err != nil || version != LatestSchemaVersion {
		t.Fatalf("schema version=%d want=%d err=%v", version, LatestSchemaVersion, err)
	}
	for _, table := range []string{"context_memories", "run_instruction_snapshots",
		"session_continuity_nodes"} {
		var count int
		if err := upgraded.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("v114 table %s is not empty/usable: count=%d err=%v", table, count, err)
		}
	}
	if err := upgraded.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err := reopened.GetWorkspaceByID(ctx, legacy.ID); err != nil {
		t.Fatalf("v114 restart lost legacy workspace: %v", err)
	}
}
