package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
)

func TestSchemaV144EnablesExactNetworkAllowlistForControlledRunCreation(t *testing.T) {
	ctx := context.Background()
	state := openUnmigratedSQLiteStore(t,
		filepath.Join(t.TempDir(), "schema-v143-controlled-network.db"))
	defer state.Close()
	if err := applyMigrationPrefixForTest(ctx, state, migrationPlan(), 143); err != nil {
		t.Fatal(err)
	}
	workspace := WorkspaceRecord{ID: "workspace-v144-network", Name: "v144 network",
		RootPath: t.TempDir(), CreatedAt: time.Now().UTC()}
	if err := state.SaveWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	request := application.ControlledRunCreationRequest{
		Version: domain.RunCreationProtocolVersion, Goal: "read exact official sources",
		WorkspaceID: workspace.ID, NetworkMode: "allowlist",
		AllowedTargets: []string{"docs.example.com"},
		OperationKey:   "migration-v144-network-operation-0001", RequestedBy: "http_control",
	}
	service := application.NewControlledRunCreationService(state)
	if _, err := service.Create(ctx, request); err == nil {
		t.Fatal("v143 unexpectedly accepted controlled network creation")
	}
	if err := state.applyMigration(ctx, migrationPlan()[143]); err != nil {
		t.Fatal(err)
	}
	created, err := service.Create(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if created.Mission.Scope.NetworkMode != "allowlist" ||
		len(created.Mission.Scope.AllowedTargets) != 1 ||
		created.Mission.Scope.AllowedTargets[0] != "docs.example.com" {
		t.Fatalf("v144 network authority=%#v", created.Mission.Scope)
	}
	if version, err := state.SchemaVersion(ctx); err != nil || version != 144 {
		t.Fatalf("schema version=%d want=144 err=%v", version, err)
	}
	assertNoForeignKeyViolations(t, state.db)
}
