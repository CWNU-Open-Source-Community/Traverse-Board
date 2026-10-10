package store

import (
	"regexp"
	"slices"
	"strings"
)

// Add a distinct sbx policy tuple without relabelling historical Docker
// containers. Copy existing rows unchanged and restore the live v187 triggers,
// including Thread continuation and per-operation approval bindings.
func sandboxBackendSelectionStatements(previous []migration) []string {
	if len(previous) != 187 || previous[len(previous)-1].Version != 187 {
		panic("v188 requires the frozen v1-v187 migration prefix")
	}
	tables := []string{"run_execution_profile_snapshots", "run_execution_interaction_snapshots", "standard_code_preset_operations"}
	createTrigger := regexp.MustCompile(`(?is)^\s*CREATE TRIGGER\s+(?:IF NOT EXISTS\s+)?(\w+)`)
	dropTrigger := regexp.MustCompile(`(?is)^\s*DROP TRIGGER\s+(?:IF EXISTS\s+)?(\w+)`)
	dropTable := regexp.MustCompile(`(?is)^\s*DROP TABLE\s+(?:IF EXISTS\s+)?(\w+)`)
	triggerTable := regexp.MustCompile(`(?i)\bON\s+(\w+)`)
	triggers := map[string]string{}
	for _, step := range previous {
		for _, statement := range step.Statements {
			if match := createTrigger.FindStringSubmatch(statement); len(match) == 2 {
				triggers[match[1]] = statement
			} else if match := dropTrigger.FindStringSubmatch(statement); len(match) == 2 {
				delete(triggers, match[1])
			} else if match := dropTable.FindStringSubmatch(statement); len(match) == 2 {
				for name, definition := range triggers {
					binding := triggerTable.FindStringSubmatch(definition)
					if len(binding) == 2 && binding[1] == match[1] {
						delete(triggers, name)
					}
				}
			}
		}
	}
	var names []string
	for name, definition := range triggers {
		for _, table := range tables {
			if strings.Contains(definition, table) {
				names = append(names, name)
				break
			}
		}
	}
	slices.Sort(names)
	result := []string{"PRAGMA legacy_alter_table = ON;"}
	for _, name := range names {
		result = append(result, "DROP TRIGGER IF EXISTS "+name+";")
	}
	replace := func(statement, old, next string) string {
		if strings.Count(statement, old) != 1 {
			panic("v188 lost exact schema anchor: " + old)
		}
		return strings.Replace(statement, old, next, 1)
	}
	for _, table := range tables {
		var create, index string
		switch table {
		case "run_execution_profile_snapshots":
			create = requireMigrationStatement("CREATE TABLE "+table+" (", runExecutionProfileStatements)
			create = replace(create, "OR (profile = 'docker'", "OR (profile = 'sbx' AND backend = 'sbx' AND approval_policy = 'always'\n"+
				"\t\t\t\tAND filesystem_scope = 'workspace' AND risk_tier = 'elevated'\n"+
				"\t\t\t\tAND required_gate = 'sbx_microvm_gate')\n\t\t\tOR (profile = 'docker'")
			index = requireMigrationStatement("CREATE INDEX idx_run_execution_profile_snapshots_run_revision", runExecutionProfileStatements)
		case "run_execution_interaction_snapshots":
			create = requireMigrationStatement("CREATE TABLE "+table+"_v133 (", standardCodePresetStatements)
			create = replace(create, table+"_v133 (", table+" (")
			create = replace(create, "execution_profile IN ('preview', 'docker', 'local')", "execution_profile IN ('preview', 'docker', 'local', 'sbx')")
			create = replace(create, "OR (execution_profile = 'docker' AND required_gate = 'docker_sandbox_gate'))",
				"OR (execution_profile = 'docker' AND required_gate = 'docker_sandbox_gate')\n"+
					"\t\t\t\t\tOR (execution_profile = 'sbx' AND required_gate = 'sbx_microvm_gate'))")
			index = requireMigrationStatement("CREATE INDEX idx_run_execution_interaction_snapshots_run_revision", runExecutionInteractionStatements)
		case "standard_code_preset_operations":
			create = requireMigrationStatement("CREATE TABLE "+table+" (", standardCodePresetStatements)
			create = replace(create, "backend_intent IN ('auto', 'local', 'docker')", "backend_intent IN ('auto', 'local', 'docker', 'sbx')")
			create = replace(create, "selected_backend IN ('local', 'docker')", "selected_backend IN ('local', 'docker', 'sbx')")
			create = replace(create, "selection_reason IN ('auto_local_ready', 'explicit_local', 'explicit_docker')", "selection_reason IN ('auto_local_ready', 'explicit_local', 'explicit_docker', 'explicit_sbx')")
			create = replace(create, "OR (backend_intent = 'docker' AND selected_backend = 'docker' AND selection_reason = 'explicit_docker'))",
				"OR (backend_intent = 'docker' AND selected_backend = 'docker' AND selection_reason = 'explicit_docker')\n"+
					"\t\t\t\tOR (backend_intent = 'sbx' AND selected_backend = 'sbx' AND selection_reason = 'explicit_sbx'))")
			index = requireMigrationStatement("CREATE INDEX idx_standard_code_preset_operations_run_created", standardCodePresetStatements)
		}
		create = replace(create, "CREATE TABLE "+table+" (", "CREATE TABLE "+table+"_v188 (")
		result = append(result, create, "INSERT INTO "+table+"_v188 SELECT * FROM "+table+";",
			"DROP TABLE "+table+";", "ALTER TABLE "+table+"_v188 RENAME TO "+table+";", index)
	}
	for _, name := range names {
		result = append(result, triggers[name])
	}
	return append(result, "PRAGMA legacy_alter_table = OFF;")
}
