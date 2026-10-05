package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

// The destination is a real migration prefix, never a schema assembled by
// reversing later DDL. The donor keeps its data, schema and migration ledger.
func historicalTestDatabaseFromSeed(t testing.TB, source *SQLiteStore, path string, version int) *SQLiteStore {
	t.Helper()
	target := openHistoricalTestDatabase(t, path, version)
	if err := copyHistoricalFixtureData(t.Context(), source, target); err != nil {
		t.Fatalf("import historical v%d fixture: %v", version, err)
	}
	return target
}

type historicalFixtureTable struct {
	name, ddl string
	columns   []string
}

func historicalFixtureIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func historicalFixtureTables(ctx context.Context, db *sql.DB) ([]historicalFixtureTable, error) {
	rows, err := db.QueryContext(ctx, `SELECT name,sql FROM sqlite_schema
		WHERE type='table' AND name NOT LIKE 'sqlite\_%' ESCAPE '\' AND name<>'schema_migrations'
		ORDER BY name`)
	if err != nil {
		return nil, err
	}
	var tables []historicalFixtureTable
	for rows.Next() {
		var table historicalFixtureTable
		if err := rows.Scan(&table.name, &table.ddl); err != nil {
			_ = rows.Close()
			return nil, err
		}
		tables = append(tables, table)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range tables {
		rows, err := db.QueryContext(ctx, "SELECT * FROM "+historicalFixtureIdentifier(tables[i].name)+" LIMIT 0")
		if err != nil {
			return nil, err
		}
		tables[i].columns, err = rows.Columns()
		_ = rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return tables, nil
}

// Final ledger rows cannot be replayed through insertion-time guards: for
// example a completed Specialist is no longer ready, and a Docker action may
// depend on an interleaved transition. Suspend only the destination triggers
// inside the import transaction, then restore their original SQL. CHECKs,
// uniqueness and the final foreign-key graph remain enforced. Missing tables
// and columns represent features absent from this historical prefix; explicit
// guards below refuse private history and modern authority that cannot be lost.
func copyHistoricalFixtureData(ctx context.Context, source, target *SQLiteStore) error {
	if source == target {
		return fmt.Errorf("historical fixture source and destination must differ")
	}
	version, err := target.SchemaVersion(ctx)
	if err != nil {
		return err
	}
	sourceTables, err := historicalFixtureTables(ctx, source.db)
	if err != nil {
		return err
	}
	targetTables, err := historicalFixtureTables(ctx, target.db)
	if err != nil {
		return err
	}
	donor := make(map[string]historicalFixtureTable, len(sourceTables))
	for _, table := range sourceTables {
		donor[table.name] = table
	}
	if err := rejectIncompatibleHistoricalFixture(ctx, source, donor, version); err != nil {
		return err
	}
	rows, err := target.db.QueryContext(ctx, `SELECT name,sql FROM sqlite_schema WHERE type='trigger' ORDER BY name`)
	if err != nil {
		return err
	}
	var triggers [][2]string
	for rows.Next() {
		var trigger [2]string
		if err := rows.Scan(&trigger[0], &trigger[1]); err != nil {
			_ = rows.Close()
			return err
		}
		triggers = append(triggers, trigger)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	tx, err := target.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `PRAGMA defer_foreign_keys=ON`); err != nil {
		return err
	}
	for _, trigger := range triggers {
		if _, err := tx.ExecContext(ctx, "DROP TRIGGER "+historicalFixtureIdentifier(trigger[0])); err != nil {
			return err
		}
	}
	// Remove prefix defaults only when the donor has the corresponding table.
	// Clear every table before inserting so cascading deletes cannot erase rows
	// already copied into a dependent table.
	for _, table := range targetTables {
		if _, exists := donor[table.name]; exists {
			if _, err := tx.ExecContext(ctx, "DELETE FROM "+historicalFixtureIdentifier(table.name)); err != nil {
				return err
			}
		}
	}
	for _, table := range targetTables {
		from, exists := donor[table.name]
		if !exists {
			continue
		}
		if err := copyHistoricalFixtureTable(ctx, source.db, tx, from, table, version); err != nil {
			return fmt.Errorf("copy %s: %w", table.name, err)
		}
	}
	for _, trigger := range triggers {
		if _, err := tx.ExecContext(ctx, trigger[1]); err != nil {
			return err
		}
	}
	rows, err = tx.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	violated := rows.Next()
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	if violated {
		return fmt.Errorf("historical fixture has foreign-key violations")
	}
	return tx.Commit()
}

func rejectIncompatibleHistoricalFixture(ctx context.Context, source *SQLiteStore,
	tables map[string]historicalFixtureTable, version int,
) error {
	guards := []struct {
		introduced   int
		table, where string
	}{
		{173, "run_supervisor_tool_rejections", "1"},
		{176, "run_supervisor_assistant_replay", "1"},
		{176, "run_supervisor_assistant_replay_bindings", "1"},
		{175, "specialist_task_briefs", "1"},
		{175, "agent_messages", "json_valid(payload_json) AND json_extract(payload_json,'$.version')='specialist_instruction.v2'"},
		{168, "operator_steering_revisions", "1"},
		{168, "operator_message_attachment_evidence", "1"},
		{168, "operator_steering_messages", "revision<>0 OR edited_at IS NOT NULL OR original_content<>content OR original_content_sha256<>content_sha256"},
		{172, "operator_steering_promotions", "1"},
		{172, "operator_steering_promotion_rejections", "1"},
		{170, "run_supervisor_provider_replay", "1"},
		{170, "run_supervisor_context_recoveries", "1"},
		{171, "operator_steering_midturn_claims", "1"},
		{171, "operator_steering_messages", "delivery_mode<>'next_turn' OR target_attempt_id<>''"},
		{162, "file_edit_auto_authorizations", "1"},
		{163, "command_runtime_jobs", "permission_runtime_epoch<>'' OR permission_generation<>0 OR permission_mode IN ('ask','auto','full')"},
		{177, "plugin_installations", "protocol_version<>'plugin-installation.v1'"},
	}
	for _, guard := range guards {
		if _, exists := tables[guard.table]; !exists || version >= guard.introduced {
			continue
		}
		// Earlier donors do not have the later compatibility columns.
		if (guard.table == "operator_steering_messages" &&
			((guard.introduced == 168 && !historicalFixtureHasColumn(tables[guard.table], "revision")) ||
				(guard.introduced == 171 && !historicalFixtureHasColumn(tables[guard.table], "delivery_mode")))) ||
			(guard.table == "command_runtime_jobs" && !historicalFixtureHasColumn(tables[guard.table], "permission_runtime_epoch")) {
			continue
		}
		var count int
		if err := source.db.QueryRowContext(ctx, "SELECT count(*) FROM "+historicalFixtureIdentifier(guard.table)+" WHERE "+guard.where).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return fmt.Errorf("historical v%d fixture cannot discard incompatible %s history", version, guard.table)
		}
	}
	return nil
}

func historicalFixtureHasColumn(table historicalFixtureTable, name string) bool {
	for _, column := range table.columns {
		if column == name {
			return true
		}
	}
	return false
}

func copyHistoricalFixtureTable(ctx context.Context, source *sql.DB, tx *sql.Tx,
	from, to historicalFixtureTable, version int,
) error {
	var columns, expressions []string
	if !strings.Contains(strings.ToUpper(from.ddl), "WITHOUT ROWID") && !strings.Contains(strings.ToUpper(to.ddl), "WITHOUT ROWID") {
		columns, expressions = append(columns, "rowid"), append(expressions, "rowid")
	}
	for _, column := range to.columns {
		if historicalFixtureHasColumn(from, column) {
			expression := historicalFixtureIdentifier(column)
			if to.name == "run_supervisor_tool_calls" && column == "authority_json" && version < 150 {
				expression = "CASE WHEN tool_name='mcp_tool_call' THEN '' ELSE authority_json END"
			}
			columns, expressions = append(columns, historicalFixtureIdentifier(column)), append(expressions, expression)
		}
	}
	if len(columns) == 0 {
		return fmt.Errorf("no common historical columns")
	}
	where := "1"
	switch to.name {
	case "specialist_model_calls":
		if version < 28 && historicalFixtureHasColumn(from, "protocol_repair") {
			where = "protocol_repair=0"
		}
	case "file_edit_apply_operations":
		if version < 115 && historicalFixtureHasColumn(from, "operation_kind") {
			where = "operation_kind='replace'"
		}
	case "file_edit_apply_results":
		if version < 115 {
			where = "operation_key_digest IN (SELECT operation_key_digest FROM file_edit_apply_operations WHERE operation_kind='replace')"
		}
	case "run_supervisor_tool_calls":
		where = historicalSupervisorCallFilter(version)
	case "run_supervisor_tool_call_agents":
		where = "EXISTS (SELECT 1 FROM run_supervisor_tool_calls call WHERE call.run_id=run_supervisor_tool_call_agents.run_id AND call.turn=run_supervisor_tool_call_agents.turn AND call.attempt_id=run_supervisor_tool_call_agents.attempt_id AND call.call_id=run_supervisor_tool_call_agents.call_id AND (" + historicalSupervisorCallFilter(version) + "))"
	}
	rows, err := source.QueryContext(ctx, "SELECT "+strings.Join(expressions, ",")+" FROM "+historicalFixtureIdentifier(from.name)+" WHERE "+where)
	if err != nil {
		return err
	}
	defer rows.Close()
	statement := "INSERT INTO " + historicalFixtureIdentifier(to.name) + " (" + strings.Join(columns, ",") + ") VALUES (" + strings.TrimSuffix(strings.Repeat("?,", len(columns)), ",") + ")"
	for rows.Next() {
		values, pointers := make([]any, len(columns)), make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, statement, values...); err != nil {
			return err
		}
	}
	return rows.Err()
}

func historicalSupervisorCallFilter(version int) string {
	filter := "1"
	if version < 150 {
		filter += " AND tool_name NOT IN ('browser_status','browser_navigate','browser_snapshot','browser_click','browser_type','browser_screenshot')"
	}
	if version < 124 {
		filter += " AND tool_name NOT IN ('github_review_evidence_list','github_review_evidence_read')"
	}
	return filter
}
