package application_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/contextmgr"
	"cyberagent-workbench/internal/store"
)

func TestContextMemoryServiceRequiresWorkspaceAndDeletesExplicitly(t *testing.T) {
	ctx := context.Background()
	state, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	workspace := store.WorkspaceRecord{ID: "workspace-context-memory", Name: "context-memory",
		RootPath: t.TempDir(), CreatedAt: time.Now().UTC()}
	if err := state.SaveWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	service := application.NewContextMemoryService(state)
	memory, err := service.Create(ctx, contextmgr.CreateMemoryRequest{
		Scope: contextmgr.MemoryScopeProject, ScopeID: workspace.ID,
		Title: "Build preference", Content: "Run the package tests first.",
		RequestedBy: "desktop_operator",
	})
	if err != nil {
		t.Fatal(err)
	}
	if memory.ID == "" || memory.Version != 1 {
		t.Fatalf("unexpected memory: %#v", memory)
	}
	if _, err := service.Create(ctx, contextmgr.CreateMemoryRequest{
		Scope: contextmgr.MemoryScopeProject, ScopeID: "missing-workspace",
		Title: "Invalid", Content: "Never store this.", RequestedBy: "desktop_operator",
	}); err == nil {
		t.Fatal("memory for a missing Workspace was accepted")
	}
	items, err := service.List(ctx, contextmgr.MemoryFilter{
		Scope: contextmgr.MemoryScopeProject, ScopeID: workspace.ID,
	})
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%#v err=%v", items, err)
	}
	exported, err := service.Export(ctx, contextmgr.MemoryFilter{
		Scope: contextmgr.MemoryScopeProject, ScopeID: workspace.ID,
	})
	if err != nil || exported.CapabilityGrant || len(exported.Items) != 1 {
		t.Fatalf("export=%#v err=%v", exported, err)
	}
	if _, err := service.Delete(ctx, memory.ID, memory.Version, "model"); err == nil {
		t.Fatal("model-triggered delete was accepted")
	}
	deleted, err := service.Delete(ctx, memory.ID, memory.Version, "desktop_operator")
	if err != nil || !deleted {
		t.Fatalf("deleted=%t err=%v", deleted, err)
	}
}
