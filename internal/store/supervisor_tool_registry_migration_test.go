package store

import (
	"strings"
	"testing"

	"cyberagent-workbench/internal/toolgateway"
)

func TestSupervisorToolLedgerAllowsEveryAdvertisedDefinition(t *testing.T) {
	st, err := Open(t.TempDir() + "/supervisor-tool-registry.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var tableSQL string
	if err := st.db.QueryRowContext(t.Context(), `SELECT sql FROM sqlite_master
		WHERE type = 'table' AND name = 'run_supervisor_tool_calls'`).Scan(&tableSQL); err != nil {
		t.Fatal(err)
	}
	for _, definition := range toolgateway.AllSupervisorToolDefinitions() {
		if !strings.Contains(tableSQL, "'"+string(definition.Name)+"'") {
			t.Fatalf("advertised Supervisor tool %q is absent from the durable ledger constraint: %s",
				definition.Name, tableSQL)
		}
	}
}
