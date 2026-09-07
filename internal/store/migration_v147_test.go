package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
)

func TestSchemaV147AllowsAtomicInitialExplicitModelRoute(t *testing.T) {
	ctx := context.Background()
	state := openUnmigratedSQLiteStore(t,
		filepath.Join(t.TempDir(), "schema-v146-explicit-model-route.db"))
	defer state.Close()
	plan := migrationPlan()
	if err := applyMigrationPrefixForTest(ctx, state, plan, 146); err != nil {
		t.Fatal(err)
	}
	workspace := WorkspaceRecord{ID: "workspace-v147-model-route", Name: "v147 model route",
		RootPath: t.TempDir(), CreatedAt: time.Now().UTC()}
	if err := state.SaveWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	request := application.ControlledRunCreationRequest{
		Version: domain.RunCreationProtocolVersion, Goal: "pin the first model route",
		WorkspaceID: workspace.ID, Profile: "code", ModelRoute: "custom-provider/model-one",
		OperationKey: "migration-v147-model-route-operation-0001", RequestedBy: "http_control",
	}
	service := application.NewControlledRunCreationService(state)
	if _, err := service.Create(ctx, request); err == nil {
		t.Fatal("v146 unexpectedly accepted an explicit initial model route")
	}
	if err := state.applyMigration(ctx, plan[146]); err != nil {
		t.Fatal(err)
	}
	created, err := service.Create(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if created.Run.Config.ModelRoute != request.ModelRoute ||
		created.Session.Route != request.ModelRoute {
		t.Fatalf("explicit route was not atomically pinned: run=%q session=%q",
			created.Run.Config.ModelRoute, created.Session.Route)
	}
	if version, err := state.SchemaVersion(ctx); err != nil || version != 147 {
		t.Fatalf("schema version=%d want=147 err=%v", version, err)
	}
	assertNoForeignKeyViolations(t, state.db)
}

func TestCleanInstallV147AllowsAtomicInitialExplicitModelRoute(t *testing.T) {
	ctx := context.Background()
	state, err := Open(filepath.Join(t.TempDir(), "clean-v147-explicit-model-route.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	workspace := WorkspaceRecord{ID: "workspace-clean-v147-model-route",
		Name: "clean v147 model route", RootPath: t.TempDir(), CreatedAt: time.Now().UTC()}
	if err := state.SaveWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	request := application.ControlledRunCreationRequest{
		Version: domain.RunCreationProtocolVersion, Goal: "pin a clean-install model route",
		WorkspaceID: workspace.ID, Profile: "code", ModelRoute: "custom-provider/model-one",
		OperationKey: "clean-v147-model-route-operation-0001", RequestedBy: "http_control",
	}
	created, err := application.NewControlledRunCreationService(state).Create(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if created.Run.Config.ModelRoute != request.ModelRoute ||
		created.Session.Route != request.ModelRoute {
		t.Fatalf("clean-install explicit route was not pinned: run=%q session=%q",
			created.Run.Config.ModelRoute, created.Session.Route)
	}
	if version, err := state.SchemaVersion(ctx); err != nil || version != LatestSchemaVersion {
		t.Fatalf("clean-install schema version=%d want=%d err=%v",
			version, LatestSchemaVersion, err)
	}
}
