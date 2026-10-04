package store

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestSchemaV179PreservesHistoricalFileAuthorizationAndChecksums(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "file-operation-history.db")
	state := openUnmigratedSQLiteStore(t, path)
	if err := applyMigrationPrefixForTest(ctx, state, migrationPlan(), 177); err != nil {
		t.Fatal(err)
	}
	_, workspace, auth := populateAutoFileEditFixture(t, state)
	edit, auth := prepareAutoFileEdit(t, state, workspace, auth, "edit-history-v179", "historical bytes\n", "historical-file-operation")
	insertHistoricalAutomaticFileEdit(t, state, edit, auth)
	before, found, err := readHistoricalAutomaticFileSource(t, state, edit.ID)
	if err != nil || !found {
		t.Fatalf("historical source: %t %v", found, err)
	}
	approvalBefore, err := state.GetApprovalByProposal(ctx, edit.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.applyMigration(ctx, migrationPlan()[177]); err != nil {
		t.Fatal(err)
	}
	var checksums string
	if err := state.db.QueryRow(`SELECT group_concat(version || ':' || checksum, ',') FROM schema_migrations`).Scan(&checksums); err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	state, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	after, found, err := state.GetFileEditAutoAuthorization(ctx, edit.ID)
	if err != nil || !found || !reflect.DeepEqual(before, after) {
		t.Fatalf("historical file source changed: before=%+v after=%+v err=%v", before, after, err)
	}
	approvalAfter, err := state.GetApprovalByProposal(ctx, edit.ID)
	if err != nil || !reflect.DeepEqual(approvalBefore, approvalAfter) {
		t.Fatalf("historical consent changed: %+v %+v %v", approvalBefore, approvalAfter, err)
	}
	var afterChecksums string
	if err := state.db.QueryRow(`SELECT group_concat(version || ':' || checksum, ',') FROM schema_migrations WHERE version<=178`).Scan(&afterChecksums); err != nil || checksums != afterChecksums {
		t.Fatalf("historical migration changed: %v", err)
	}
	if _, err := state.db.Exec(`UPDATE file_edit_auto_authorizations SET run_authorization_fence=1 WHERE edit_id=?`, edit.ID); err == nil {
		t.Fatal("historical source was silently renewed")
	}
	assertNoForeignKeyViolations(t, state.db)
}
