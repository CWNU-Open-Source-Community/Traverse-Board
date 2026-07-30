package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSchemaV89AddsImmutableControlledCommandProposalLedger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schema-v88-command-proposals.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, statement := range removeSchemaV89ForTestStatements() {
		if _, err := st.db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("downgrade v89 fixture with %q: %v", statement, err)
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
	if version, err := upgraded.SchemaVersion(ctx); err != nil ||
		version != LatestSchemaVersion {
		t.Fatalf("schema version=%d err=%v", version, err)
	}
	for _, table := range []string{
		"controlled_command_proposals",
		"controlled_command_proposal_operations",
		"controlled_command_proposal_reviews",
		"controlled_command_proposal_results",
	} {
		var count int
		if err := upgraded.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sqlite_master
			WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil ||
			count != 1 {
			t.Fatalf("table %s count=%d err=%v", table, count, err)
		}
	}
}

func removeSchemaV89ForTestStatements() []string {
	return append(removeSchemaV90ForTestStatements(), []string{
		`DROP TRIGGER trg_controlled_command_result_delete_immutable`,
		`DROP TRIGGER trg_controlled_command_result_update_immutable`,
		`DROP TRIGGER trg_controlled_command_review_delete_immutable`,
		`DROP TRIGGER trg_controlled_command_review_update_immutable`,
		`DROP TRIGGER trg_controlled_command_proposal_operation_delete_immutable`,
		`DROP TRIGGER trg_controlled_command_proposal_operation_update_immutable`,
		`DROP TRIGGER trg_controlled_command_proposal_delete_immutable`,
		`DROP TRIGGER trg_controlled_command_proposal_update_immutable`,
		`DROP TRIGGER trg_controlled_command_result_insert_binding`,
		`DROP TRIGGER trg_controlled_command_review_insert_binding`,
		`DROP TRIGGER trg_controlled_command_proposal_operation_insert_binding`,
		`DROP TRIGGER trg_controlled_command_proposal_insert_binding`,
		`DROP TABLE controlled_command_proposal_results`,
		`DROP INDEX idx_controlled_command_reviews_run_created`,
		`DROP TABLE controlled_command_proposal_reviews`,
		`DROP TABLE controlled_command_proposal_operations`,
		`DROP INDEX idx_controlled_command_proposals_run_created`,
		`DROP TABLE controlled_command_proposals`,
		`DELETE FROM schema_migrations WHERE version = 89`,
	}...)
}
