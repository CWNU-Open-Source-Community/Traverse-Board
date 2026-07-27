package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
)

func TestRunExecutionInteractionIsImmutableIdempotentAndProfileBound(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "run-execution-interaction.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	runs := application.NewRunService(st)
	_, run, err := runs.Create(ctx, application.CreateRunRequest{
		Goal: "select a bounded command interaction", Profile: "code",
		Budget: domain.Budget{MaxTurns: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	interactions := application.NewRunExecutionInteractionService(st)
	initial, err := interactions.Current(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if initial.Mode != domain.RunExecutionInteractionPreview ||
		initial.WorkspaceTrust != domain.WorkspaceTrustUntrusted ||
		initial.Revision != 1 || initial.AgentInputDefault ||
		initial.ProcessEnabled || initial.ExecutionAuthorized ||
		initial.CapabilityGrant {
		t.Fatalf("unexpected initial interaction: %#v", initial)
	}
	_, err = interactions.Change(ctx,
		application.ChangeRunExecutionInteractionRequest{
			RunID: run.ID, Mode: "controlled", Trust: "trusted",
			OperationKey: "interaction-before-profile-0001",
			RequestedBy:  "test_operator", Reason: "must match local profile",
			ConfirmWorkspaceTrust: true,
		})
	if apperror.CodeOf(err) != apperror.CodeInvalidArgument ||
		!strings.Contains(err.Error(), "transition is invalid") {
		t.Fatalf("controlled mode without local profile error=%v", err)
	}
	_, err = application.NewRunExecutionProfileService(st).Change(ctx,
		application.ChangeRunExecutionProfileRequest{
			RunID: run.ID, Profile: "local",
			OperationKey: "interaction-local-profile-0001",
			RequestedBy:  "test_operator", Reason: "prepare controlled mode",
		})
	if err != nil {
		t.Fatal(err)
	}
	request := application.ChangeRunExecutionInteractionRequest{
		RunID: run.ID, Mode: "controlled", Trust: "trusted",
		OperationKey: "interaction-controlled-0001",
		RequestedBy:  "test_operator", Reason: "trusted code workspace",
		ConfirmWorkspaceTrust: true,
	}
	selected, err := interactions.Change(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Replayed || selected.Interaction.Revision != 2 ||
		selected.Interaction.Mode != domain.RunExecutionInteractionControlled ||
		selected.Interaction.CommandForm != domain.ExecutionCommandStructuredArgv ||
		selected.Interaction.ExecutionProfile != domain.RunExecutionProfileLocal ||
		selected.Interaction.ExecutionProfileRevision != 2 ||
		selected.Interaction.PersistentTerminal ||
		selected.Interaction.AgentInputDefault ||
		selected.Interaction.ProcessEnabled ||
		selected.Interaction.ExecutionAuthorized ||
		selected.Interaction.CapabilityGrant {
		t.Fatalf("unexpected controlled interaction: %#v", selected)
	}
	replayed, err := interactions.Change(ctx, request)
	if err != nil || !replayed.Replayed ||
		replayed.Interaction.ID != selected.Interaction.ID {
		t.Fatalf("interaction replay changed result: %#v err=%v", replayed, err)
	}
	request.Mode = "debug"
	request.ConfirmDebugBoundary = true
	if _, err := interactions.Change(ctx, request); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("reused operation key error=%v", err)
	}
	for _, statement := range []string{
		`UPDATE run_execution_interaction_snapshots SET process_enabled = 1 WHERE id = ?`,
		`DELETE FROM run_execution_interaction_snapshots WHERE id = ?`,
	} {
		if _, err := st.db.ExecContext(ctx, statement, selected.Interaction.ID); err == nil {
			t.Fatalf("immutable interaction statement succeeded: %s", statement)
		}
	}
	eventList, err := st.ListRunEvents(ctx, run.ID)
	if err != nil || len(eventList) == 0 ||
		eventList[len(eventList)-1].Type !=
			events.RunExecutionInteractionSelectedEvent {
		t.Fatalf("interaction event missing: events=%#v err=%v", eventList, err)
	}
}

func removeSchemaV86ForTestStatements() []string {
	return append(removeSchemaV87ForTestStatements(), []string{
		`DROP TRIGGER trg_run_execution_interaction_operation_delete_immutable`,
		`DROP TRIGGER trg_run_execution_interaction_operation_update_immutable`,
		`DROP TRIGGER trg_run_execution_interaction_snapshot_delete_immutable`,
		`DROP TRIGGER trg_run_execution_interaction_snapshot_update_immutable`,
		`DROP TRIGGER trg_run_execution_interaction_operation_insert`,
		`DROP TRIGGER trg_run_execution_interaction_snapshot_insert`,
		`DROP TABLE run_execution_interaction_operations`,
		`DROP INDEX idx_run_execution_interaction_snapshots_run_revision`,
		`DROP TABLE run_execution_interaction_snapshots`,
		`DELETE FROM schema_migrations WHERE version = 86`,
	}...)
}

func TestSchemaV86BackfillsUntrustedPreviewInteraction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schema-v85-interaction.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, run, err := application.NewRunService(st).Create(ctx,
		application.CreateRunRequest{
			Goal: "legacy v85 Run", Profile: "review",
			Budget: domain.Budget{MaxTurns: 2},
		})
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range removeSchemaV86ForTestStatements() {
		if _, err := st.db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("downgrade v86 fixture with %q: %v", statement, err)
		}
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	interaction, err := upgraded.GetRunExecutionInteraction(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if interaction.Mode != domain.RunExecutionInteractionPreview ||
		interaction.Revision != 1 ||
		interaction.RequestedBy != "schema_v86" ||
		interaction.WorkspaceTrust != domain.WorkspaceTrustUntrusted ||
		interaction.AgentInputDefault || interaction.ProcessEnabled ||
		interaction.ExecutionAuthorized || interaction.CapabilityGrant {
		t.Fatalf("unexpected v86 compatibility interaction: %#v", interaction)
	}
	if version, err := upgraded.SchemaVersion(ctx); err != nil ||
		version != LatestSchemaVersion {
		t.Fatalf("schema version=%d err=%v", version, err)
	}
}
