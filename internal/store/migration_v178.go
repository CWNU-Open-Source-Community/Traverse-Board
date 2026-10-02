package store

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Rebuild only the existing snapshot tables. Historical rows and operations
// are copied byte-for-byte. All previous migrations/checksums remain immutable;
// deriving live trigger definitions from that fixed prefix avoids restoring an
// obsolete trigger after the table rebuild. No generated baseline is an input.
func operationApprovalPreferenceStatements(previous []migration) []string {
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
				// SQLite drops a table's own triggers automatically. Do not
				// resurrect one retired by an earlier table rebuild.
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
	for name, statement := range triggers {
		if strings.Contains(statement, "run_execution_permission_snapshots") || strings.Contains(statement, "thread_execution_permission_snapshots") {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	var result []string
	for _, name := range names {
		result = append(result, "DROP TRIGGER IF EXISTS "+name+";")
	}
	for _, kind := range []string{"run", "thread"} {
		table := kind + "_execution_permission_snapshots"
		var create string
		var indexes []string
		if kind == "run" {
			for _, statement := range workspaceAccessExecutionPermissionStatements {
				if strings.HasPrefix(statement, "CREATE TABLE "+table+"_v126 (") {
					create = strings.Replace(statement, table+"_v126", table+"_v178", 1)
				}
				if strings.HasPrefix(statement, "CREATE INDEX ") && strings.Contains(statement, "ON "+table+"(") {
					indexes = append(indexes, statement)
				}
			}
		} else {
			for _, statement := range threadExecutionPermissionStatements {
				if strings.HasPrefix(statement, "CREATE TABLE "+table+" (") {
					create = strings.Replace(statement, "CREATE TABLE "+table, "CREATE TABLE "+table+"_v178", 1)
				}
				if strings.HasPrefix(statement, "CREATE INDEX ") && strings.Contains(statement, "ON "+table+"(") {
					indexes = append(indexes, statement)
				}
			}
		}
		if create == "" || len(indexes) != 1 {
			panic("operation approval migration lost snapshot table anchors")
		}
		create = strings.Replace(create, "\t\tCHECK(protocol_version = '"+kind+"_execution_permission.v1'),\n", "", 1)
		create = strings.Replace(create, "\t\tCHECK(policy_version = 'execution_permission_policy.v1'),\n", "", 1)
		start := strings.Index(create, "\t\tCHECK(\n")
		if start < 0 {
			panic("operation approval migration lost legacy controls")
		}
		end := strings.Index(create[start:], "\n\t\t),") + start
		if end <= start {
			panic("operation approval migration lost legacy constraint terminator")
		}
		legacy := create[start+len("\t\tCHECK(") : end]
		constraint := fmt.Sprintf(`
		CHECK((protocol_version = '%[1]s' AND policy_version = 'execution_permission_policy.v1' AND (%[2]s))
			OR (protocol_version = '%[3]s' AND policy_version = 'execution_permission_policy.v2'
				AND mode IN ('ask','auto','full') AND approval_policy = 'per_operation'
				AND command_scope = 'per_operation' AND filesystem_scope = 'per_operation' AND network_scope = 'per_operation'
				AND persistent_terminal = 0 AND background_process = 0 AND agent_terminal_input = 0
				AND required_gate = 'operation_authority'
				AND ((mode = 'full' AND operator_confirmed = 1 AND risk_tier = 'high')
					OR (mode IN ('ask','auto') AND operator_confirmed = 0 AND risk_tier = 'minimal'))))`, kind+"_execution_permission.v1", legacy, kind+"_execution_permission.v2")
		create = create[:start] + constraint + create[end+len("\n\t\t)"):]
		result = append(result, create, "INSERT INTO "+table+"_v178 SELECT * FROM "+table+";", "DROP TABLE "+table+";",
			"ALTER TABLE "+table+"_v178 RENAME TO "+table+";")
		result = append(result, indexes...)
	}
	for _, name := range names {
		statement := triggers[name]
		for _, kind := range []string{"run", "thread"} {
			if name != "trg_"+kind+"_execution_permission_snapshot_insert" {
				continue
			}
			statement = strings.Replace(statement, "WHEN NOT EXISTS (", "WHEN NEW.protocol_version <> '"+kind+"_execution_permission.v2' OR NOT EXISTS (", 1)
			statement = strings.Replace(statement, "NEW.mode = 'conservative'", "NEW.mode = 'ask'", 1)
			statement = strings.Replace(statement, "previous.protocol_version = NEW.protocol_version",
				"(previous.protocol_version = NEW.protocol_version OR previous.protocol_version = '"+kind+"_execution_permission.v1')", 1)
			statement = strings.Replace(statement, "previous.policy_version = NEW.policy_version",
				"(previous.policy_version = NEW.policy_version OR previous.policy_version = 'execution_permission_policy.v1')", 1)
			if kind == "run" {
				statement = strings.Replace(statement, "((previous.mode = 'debug'", "((previous.mode = 'auto' AND NEW.mode = 'ask') OR (previous.mode = 'debug'", 1)
				statement = strings.Replace(statement, "previous.mode = 'full_access'", "previous.mode IN ('full_access','full')", 1)
				statement = strings.Replace(statement, "NEW.mode NOT IN ('full_access', 'debug')", "NEW.mode NOT IN ('full_access', 'debug','full')", 1)
			}
		}
		result = append(result, statement)
	}
	return result
}
