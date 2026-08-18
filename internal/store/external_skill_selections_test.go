package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/skills"
)

func TestExternalSkillSelectionIsImmutableAndPinsInstallation(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "external-skill-selection.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	installation, installOperation, installResult := fixturePackageInstallation(t,
		"external-selection-review", "1.0.0", "external-install-operation",
		time.Now().UTC().Add(-time.Minute))
	if _, _, _, err := st.PreparePackageInstallation(ctx, installation,
		installOperation); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.CompletePackageInstallation(ctx, installResult); err != nil {
		t.Fatal(err)
	}
	mission, run, err := application.NewRunService(st).Create(ctx,
		application.CreateRunRequest{
			Goal: "external Skill selection Store test", Profile: "review",
			Budget: domain.Budget{MaxTurns: 4, MaxTokens: 4096},
		})
	if err != nil {
		t.Fatal(err)
	}
	result, err := application.NewExternalSkillSelectionService(st).Select(ctx,
		application.SelectExternalSkillsRequest{
			RunID: run.ID, PackageRefs: []string{"external-selection-review@1.0.0"},
			SpecialistRef: "external-selection-review@1.0.0", TokenBudget: 1024,
			OperationKey: "external-selection-operation", RequestedBy: "operator",
			ConfirmUntrustedContext: true,
		})
	if err != nil {
		t.Fatal(err)
	}
	selection := result.Selection
	if selection.MissionID != mission.ID || selection.ToolCapabilityGrant ||
		!selection.ContextDeliveryAuthorized {
		t.Fatalf("unexpected external Skill selection: %#v", selection)
	}

	for _, mutation := range []struct {
		query string
		arg   string
	}{
		{`UPDATE run_external_skill_selections SET token_budget = 2048 WHERE id = ?`, selection.ID},
		{`DELETE FROM run_external_skill_selections WHERE id = ?`, selection.ID},
		{`UPDATE run_external_skill_selection_items SET specialist_eligible = 0 WHERE selection_id = ?`, selection.ID},
		{`DELETE FROM run_external_skill_selection_operations WHERE selection_id = ?`, selection.ID},
	} {
		if _, err := st.db.ExecContext(ctx, mutation.query, mutation.arg); err == nil {
			t.Fatalf("immutable external Skill selection mutation succeeded: %s", mutation.query)
		}
	}

	removal, removalOperation := fixturePackageRemoval(t, installation,
		"external-selection-remove", time.Now().UTC())
	if _, _, err := st.CreatePackageRemoval(ctx, removal,
		removalOperation); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("external selection pinned removal error = %v", err)
	}
	assertExternalInstallationCanBeSelectedByAnotherRun(t, ctx, st)
	assertExternalSelectionCannotCommitWithoutOperation(t, ctx, st)
}

func TestExternalSkillSelectionFreezesHistoricalBuiltinNameCollision(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "external-skill-builtin-collision.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	installation, installOperation, installResult := fixturePackageInstallation(t,
		"doctor", "0.9.0", "historical-doctor-install", time.Now().UTC().Add(-time.Minute))
	if _, _, _, err := st.PreparePackageInstallation(ctx, installation,
		installOperation); err != nil {
		t.Fatal(err)
	}
	installed, _, err := st.CompletePackageInstallation(ctx, installResult)
	if err != nil {
		t.Fatal(err)
	}
	mission, run, err := application.NewRunService(st).Create(ctx,
		application.CreateRunRequest{
			Goal: "replay a historical external Skill selection", Profile: "review",
			Budget: domain.Budget{MaxTurns: 2, MaxTokens: 2048},
		})
	if err != nil {
		t.Fatal(err)
	}
	mode, err := st.GetRunMode(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	createdAt := time.Now().UTC()
	selection, err := skills.ResolveExternalSelection(skills.ResolveExternalSelectionRequest{
		SelectionID: "historical-doctor-selection", RunID: run.ID, MissionID: mission.ID,
		ModeSnapshotID: mode.ID, ModeRevision: mode.Revision, Surface: mode.Surface,
		Profile: mission.Profile, Packages: []skills.InstalledPackage{installed},
		TokenBudget: 1024, RequestedBy: "operator", Confirmed: true, CreatedAt: createdAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	keyDigest := runmutation.Fingerprint("external_skill_selection_operation.v1",
		run.ID, "historical-doctor-selection-operation")
	operation := skills.ExternalSelectionOperation{
		KeyDigest: keyDigest, RequestFingerprint: skills.ExternalSelectionRequestFingerprint(selection),
		SelectionID: selection.ID, RunID: run.ID, RequestedBy: selection.RequestedBy,
		CreatedAt: selection.CreatedAt,
	}
	event, err := events.New(run.ID, mission.ID,
		events.ExternalSkillSelectionCreatedEvent, "external_skills", selection.ID,
		map[string]any{
			"protocol": selection.ProtocolVersion, "surface": selection.Surface,
			"profile": selection.Profile, "item_count": selection.ItemCount,
			"token_budget":       selection.TokenBudget,
			"token_upper_bound":  selection.TokenUpperBound,
			"operator_confirmed": true, "context_delivery": true,
			"tool_capability_grant": false,
		})
	if err != nil {
		t.Fatal(err)
	}
	event.CreatedAt = selection.CreatedAt
	if _, replayed, err := st.CreateExternalSkillSelection(ctx, selection,
		operation, event); err != nil || replayed {
		t.Fatalf("seed historical external selection: replayed=%t err=%v", replayed, err)
	}

	service := application.NewExternalSkillSelectionService(st)
	replayed, err := service.Select(ctx, application.SelectExternalSkillsRequest{
		RunID: run.ID, PackageRefs: []string{"doctor@0.9.0"}, TokenBudget: 1024,
		OperationKey: "historical-doctor-selection-operation", RequestedBy: "operator",
		ConfirmUntrustedContext: true,
	})
	if err != nil || !replayed.Replayed || replayed.Selection.ID != selection.ID {
		t.Fatalf("historical selection replay = %#v err=%v", replayed, err)
	}

	_, newRun, err := application.NewRunService(st).Create(ctx,
		application.CreateRunRequest{
			Goal: "reject a new external Skill collision", Profile: "review",
			Budget: domain.Budget{MaxTurns: 2, MaxTokens: 2048},
		})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Select(ctx, application.SelectExternalSkillsRequest{
		RunID: newRun.ID, PackageRefs: []string{"doctor@0.9.0"}, TokenBudget: 1024,
		OperationKey: "new-doctor-selection-operation", RequestedBy: "operator",
		ConfirmUntrustedContext: true,
	})
	if apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("new built-in name collision error = %v", err)
	}
}

func assertExternalInstallationCanBeSelectedByAnotherRun(t *testing.T, ctx context.Context,
	st *SQLiteStore,
) {
	t.Helper()
	_, run, err := application.NewRunService(st).Create(ctx,
		application.CreateRunRequest{
			Goal: "reuse an installed external Skill", Profile: "review",
			Budget: domain.Budget{MaxTurns: 2, MaxTokens: 1024},
		})
	if err != nil {
		t.Fatal(err)
	}
	result, err := application.NewExternalSkillSelectionService(st).Select(ctx,
		application.SelectExternalSkillsRequest{
			RunID: run.ID, PackageRefs: []string{"external-selection-review@1.0.0"},
			TokenBudget: 1024, OperationKey: "external-selection-reuse-operation",
			RequestedBy: "operator", ConfirmUntrustedContext: true,
		})
	if err != nil {
		t.Fatalf("reuse installed external Skill in another Run: %v", err)
	}
	if result.Replayed || result.Selection.RunID != run.ID ||
		len(result.Selection.Items) != 1 {
		t.Fatalf("unexpected reused external Skill selection: %#v", result)
	}
}

func assertExternalSelectionCannotCommitWithoutOperation(t *testing.T, ctx context.Context,
	st *SQLiteStore,
) {
	t.Helper()
	mission, run, err := application.NewRunService(st).Create(ctx,
		application.CreateRunRequest{
			Goal: "orphan external selection test", Profile: "review",
			Budget: domain.Budget{MaxTurns: 2, MaxTokens: 1024},
		})
	if err != nil {
		t.Fatal(err)
	}
	mode, err := st.GetRunMode(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	createdAt := time.Now().UTC()
	fingerprint := runmutation.Fingerprint("orphan-external-selection")
	_, err = tx.ExecContext(ctx, `INSERT INTO run_external_skill_selections
		(id, run_id, mission_id, mode_snapshot_id, mode_revision, protocol_version,
		surface, profile, token_budget, token_upper_bound, item_count,
		selection_fingerprint, requested_by, operator_confirmed,
		context_delivery_authorized, tool_capability_grant, created_at)
		VALUES (?, ?, ?, ?, ?, 'external_skill_selection.v1', ?, ?, 1, 1, 1,
			?, 'operator', 1, 1, 0, ?)`, "orphan-external-selection", run.ID,
		mission.ID, mode.ID, mode.Revision, mode.Surface, mode.Profile, fingerprint,
		ts(createdAt))
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err == nil {
		t.Fatal("external Skill selection committed without its operation")
	}
}
