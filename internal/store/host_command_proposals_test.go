package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSchemaV96AddsImmutableHostCommandProposalLedger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schema-v95-host-proposals.db")
	st := openHistoricalTestDatabase(t, path, 95)

	ctx := context.Background()
	// The immutable historical prefix above is the upgrade input.
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	if version, err := upgraded.SchemaVersion(ctx); err != nil || version != LatestSchemaVersion {
		t.Fatalf("schema version=%d err=%v", version, err)
	}
	for _, table := range []string{
		"host_command_proposals", "host_command_proposal_operations",
		"host_command_proposal_reviews", "host_command_proposal_execution_intents",
		"host_command_proposal_results",
	} {
		var count int
		if err := upgraded.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).
			Scan(&count); err != nil || count != 1 {
			t.Fatalf("table %s count=%d err=%v", table, count, err)
		}
	}
}
