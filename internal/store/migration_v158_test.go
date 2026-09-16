package store

import (
	"path/filepath"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
)

func TestSchemaV158PreservesExistingSteeringAndExternalForeignKeys(t *testing.T) {
	state := openUnmigratedSQLiteStore(t, filepath.Join(t.TempDir(), "image-migration.db"))
	defer state.Close()
	if err := applyMigrationPrefixForTest(t.Context(), state, migrationPlan(), 157); err != nil {
		t.Fatal(err)
	}
	runs := application.NewRunService(state)
	_, run, err := runs.Create(t.Context(), application.CreateRunRequest{Goal: "Old image-free message", Profile: "review", Budget: domain.Budget{MaxTurns: 3}})
	if err != nil {
		t.Fatal(err)
	}
	content := "Existing original operator input"
	created := ts(time.Now().UTC())
	sha := domain.OperatorSteeringContentSHA256(content)
	if _, err := state.db.ExecContext(t.Context(), `INSERT INTO operator_steering_messages (id,run_id,session_id,sequence,status,content,content_sha256,requested_by,created_at) VALUES (?,?,?,1,'pending',?,?,'operator',?)`, "steer-image-migration", run.ID, run.SessionID, content, sha, created); err != nil {
		t.Fatal(err)
	}
	if err := state.applyMigration(t.Context(), migrationPlan()[157]); err != nil {
		t.Fatal(err)
	}
	// Inspect the historical v158 shape directly. The current public scanner
	// also reads attachment_count, introduced only by migration v160.
	var message domain.OperatorSteeringMessage
	err = state.db.QueryRowContext(t.Context(), `SELECT content,content_sha256,image_count FROM operator_steering_messages WHERE id=?`, "steer-image-migration").Scan(&message.Content, &message.ContentSHA256, &message.ImageCount)
	if err != nil || message.Content != content || message.ContentSHA256 != sha || message.ImageCount != 0 {
		t.Fatalf("old message changed: %#v %v", message, err)
	}
	rows, err := state.db.QueryContext(t.Context(), `PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		rows.Close()
		t.Fatal("migration changed external foreign keys")
	}
	rows.Close()
	for _, query := range []string{`UPDATE operator_steering_messages SET content='changed' WHERE id='steer-image-migration'`, `UPDATE operator_steering_messages SET image_count=1 WHERE id='steer-image-migration'`, `DELETE FROM operator_steering_messages WHERE id='steer-image-migration'`} {
		if _, err := state.db.ExecContext(t.Context(), query); err == nil {
			t.Fatalf("immutable old message accepted %s", query)
		}
	}
	if _, err := state.db.ExecContext(t.Context(), `INSERT INTO operator_steering_messages (id,run_id,session_id,sequence,status,content,content_sha256,requested_by,created_at) VALUES (?,?,?,2,'pending','',?,'operator',?)`, "steer-empty-image-migration", run.ID, run.SessionID, domain.OperatorSteeringContentSHA256(""), created); err == nil {
		t.Fatal("empty input without images was admitted")
	}
}
