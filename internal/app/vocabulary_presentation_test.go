package app

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHelpExplainsCanonicalVocabularyAndCompatibility(t *testing.T) {
	help, stderr, code := executeTestCommand(t, "help")
	if code != 0 || stderr != "" {
		t.Fatalf("help failed code=%d stderr=%s", code, stderr)
	}
	for _, expected := range []string{
		"Product vocabulary / 产品词汇",
		"Thread / 任务",
		"Run / 执行尝试",
		"Step / 步骤",
		"Tool Item / 工具项",
		"Workspace / 工作区",
		"Plan item / 计划项",
		"Mission: immutable Thread intent",
		"Session: Run-local context and authority boundary",
		"Compatibility identifiers / 兼容标识",
		"cyberagent-workbench",
		"CYBERAGENT_*",
		".prayu/...",
		"/api/v1/runs",
		"/api/v1/sessions",
		"work_item",
	} {
		if !strings.Contains(help, expected) {
			t.Errorf("help is missing %q", expected)
		}
	}
}

func TestPresentationVocabularyKeepsAPIEventAndSQLiteIdentities(t *testing.T) {
	openAPI, err := os.ReadFile(filepath.Join("..", "..", "docs", "openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`"/api/v1/runs"`,
		`"/api/v1/sessions"`,
		`"/api/v1/threads"`,
		`"mission_id"`,
		`"session_id"`,
		`"work_item_id"`,
	} {
		if !strings.Contains(string(openAPI), expected) {
			t.Errorf("OpenAPI compatibility identity %s is missing", expected)
		}
	}

	home := t.TempDir()
	t.Setenv("CYBERAGENT_HOME", home)
	created, stderr, code := executeTestCommand(t, "run", "create", "vocabulary identity guard", "--profile", "code")
	if code != 0 {
		t.Fatalf("run create failed: %s", stderr)
	}
	runID := runIDPattern.FindString(created)
	if runID == "" {
		t.Fatalf("missing Run id: %s", created)
	}
	events, stderr, code := executeTestCommand(t, "run", "events", runID)
	if code != 0 || !strings.Contains(events, "run.created") {
		t.Fatalf("stable event identity missing output=%s stderr=%s code=%d", events, stderr, code)
	}

	db, err := sql.Open("sqlite3", filepath.Join(home, "cyberagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for table, columns := range map[string][]string{
		"missions":   {"id", "workspace_id", "scope_json"},
		"runs":       {"id", "mission_id", "session_id"},
		"sessions":   {"id", "workspace_id"},
		"work_items": {"id", "run_id", "owner_agent_id"},
	} {
		assertSQLiteColumns(t, db, table, columns)
	}
}

func assertSQLiteColumns(t *testing.T, db *sql.DB, table string, expected []string) {
	t.Helper()
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, name := range expected {
		if !columns[name] {
			t.Errorf("SQLite compatibility column %s.%s is missing", table, name)
		}
	}
}
