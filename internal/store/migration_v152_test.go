package store

import (
	"path/filepath"
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runmutation"
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
	// Seed the exact legacy columns and idempotency receipt. The current writer
	// also writes revision fields introduced in v168, which do not belong here.
	const legacyMessageID = "steer-v151-existing-input"
	contentDigest := domain.OperatorSteeringContentSHA256(request.Content)
	keyDigest := runmutation.Fingerprint("operator_steering_operation.v1", run.ID, request.OperationKey)
	fingerprint := runmutation.Fingerprint("operator_steering_request.v1", run.ID, run.SessionID, contentDigest, request.RequestedBy)
	createdAt := ts(time.Now().UTC())
	if _, err := state.db.ExecContext(t.Context(), `INSERT INTO operator_steering_messages
		(id,run_id,session_id,sequence,status,content,content_sha256,requested_by,created_at)
		VALUES(?,?,?,1,'pending',?,?,?,?)`, legacyMessageID, run.ID, run.SessionID,
		request.Content, contentDigest, request.RequestedBy, createdAt); err != nil {
		t.Fatal(err)
	}
	if _, err := state.db.ExecContext(t.Context(), `INSERT INTO operator_steering_operations
		(operation_key_digest,request_fingerprint,message_id,run_id,requested_by,created_at)
		VALUES(?,?,?,?,?,?)`, keyDigest, fingerprint, legacyMessageID, run.ID,
		request.RequestedBy, createdAt); err != nil {
		t.Fatal(err)
	}
	restoreLegacyInputs()
	if err := state.applyMigrations(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	intent, err := state.ReserveThreadMessageIntent(t.Context(), request)
	if err != nil || intent.MessageID != legacyMessageID || intent.RunID != run.ID {
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
