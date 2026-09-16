package store

import (
	"path/filepath"
	"testing"

	"cyberagent-workbench/internal/domain"
)

func TestSchemaV152PreservesLegacySteeringAndInstallsImmutableIntent(t *testing.T) {
	state := openUnmigratedSQLiteStore(t, filepath.Join(t.TempDir(), "schema-v151-message-intent.db"))
	defer state.Close()
	plan := migrationPlan()
	if err := applyMigrationPrefixForTest(t.Context(), state, plan, 151); err != nil {
		t.Fatal(err)
	}
	restoreLegacyInputs := addCurrentInputColumnsForLegacySeed(t, state)
	_, run := createWorkItemTestRun(t, t.Context(), state, "legacy Thread intent upgrade")
	request := domain.ThreadMessageIntentRequest{ThreadID: domain.InitialThreadID(run.ID), Content: "existing queued input", OperationKey: "thread-intent-migration-operation-0001", RequestedBy: "test_operator"}
	legacy, err := state.EnqueueOperatorSteering(t.Context(), domain.EnqueueOperatorSteeringRequest{RunID: run.ID, SessionID: run.SessionID, Content: request.Content, OperationKey: request.OperationKey, RequestedBy: request.RequestedBy})
	if err != nil {
		t.Fatal(err)
	}
	restoreLegacyInputs()
	if err := state.applyMigrations(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	intent, err := state.ReserveThreadMessageIntent(t.Context(), request)
	if err != nil || intent.MessageID != legacy.Message.ID || intent.RunID != run.ID {
		t.Fatalf("legacy intent adoption=%#v %v", intent, err)
	}
	if _, err := state.db.ExecContext(t.Context(), `UPDATE thread_message_intents SET files_json='[{}]' WHERE operation_key_digest=?`, intent.OperationKeyDigest); err == nil {
		t.Fatal("stored manifest was mutable")
	}
	if _, err := state.db.ExecContext(t.Context(), `UPDATE thread_message_intents SET rejected=1 WHERE operation_key_digest=?`, intent.OperationKeyDigest); err == nil {
		t.Fatal("accepted message was marked rejected")
	}
	assertNoForeignKeyViolations(t, state.db)
}
