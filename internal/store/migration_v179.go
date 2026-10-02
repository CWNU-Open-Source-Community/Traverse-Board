package store

import "strings"

// Keep the existing immutable source ledger and every historical row. New
// Ask/Auto decisions have no Full activation generation; their optional host
// runtime epoch/fence is independent of that generation. Old Full sources
// retain the original constraints and are never converted into new grants.
func fileOperationApprovalStatements() []string {
	var statements, triggers []string
	var create string
	for _, statement := range automaticFileEditMoveAuthorizationStatements {
		switch {
		case strings.HasPrefix(statement, "DROP TRIGGER "):
			statements = append(statements, statement)
		case strings.HasPrefix(statement, "CREATE TABLE file_edit_auto_authorizations ("):
			create = statement
		case strings.HasPrefix(statement, "CREATE TRIGGER "):
			triggers = append(triggers, statement)
		}
	}
	if create == "" || len(triggers) != 7 {
		panic("file operation migration lost historical schema anchors")
	}
	create = strings.Replace(create, "CREATE TABLE file_edit_auto_authorizations (", "CREATE TABLE file_edit_auto_authorizations_v179 (", 1)
	create = strings.ReplaceAll(create, "operation_kind IN ('create','replace','move')", "operation_kind IN ('create','replace','move','delete')")
	create = strings.ReplaceAll(create, "length(runtime_epoch) BETWEEN 1 AND 256", "length(runtime_epoch) BETWEEN 0 AND 256")
	create = strings.ReplaceAll(create, "runtime_generation > 0", "runtime_generation >= 0")
	create = strings.Replace(create, "\t\tcreated_at TEXT NOT NULL,", "\t\tcreated_at TEXT NOT NULL,\n\t\trun_authorization_fence INTEGER NOT NULL DEFAULT 0 CHECK(run_authorization_fence >= 0),", 1)
	create = strings.ReplaceAll(create, "operation_kind IN ('create','replace')", "operation_kind IN ('create','replace','delete')")
	statements = append(statements, create,
		`INSERT INTO file_edit_auto_authorizations_v179 SELECT *, 0 FROM file_edit_auto_authorizations;`,
		`DROP TABLE file_edit_auto_authorizations;`,
		`ALTER TABLE file_edit_auto_authorizations_v179 RENAME TO file_edit_auto_authorizations;`,
		`CREATE INDEX idx_file_edit_auto_authorizations_run_created ON file_edit_auto_authorizations(run_id, created_at);`)
	for _, trigger := range triggers {
		row := "NEW"
		if strings.Contains(trigger, "CREATE TRIGGER trg_file_edit_auto_apply_insert") {
			row = "source"
		}
		constraint := `((permission.mode='full_access' AND ` + row + `.runtime_generation>0 AND length(` + row + `.runtime_epoch)>0 AND ` + row + `.operation_kind<>'delete')
			OR (permission.protocol_version='run_execution_permission.v2' AND permission.mode IN ('ask','auto','full')
				AND ((permission.mode='full' AND ` + row + `.runtime_generation>0 AND length(` + row + `.runtime_epoch)>0)
					OR (permission.mode IN ('ask','auto') AND ` + row + `.runtime_generation=0 AND ` + row + `.operation_kind<>'delete'))
				AND ((` + row + `.runtime_epoch='' AND ` + row + `.run_authorization_fence=0)
					OR (length(` + row + `.runtime_epoch)>0 AND ` + row + `.run_authorization_fence>0))))`
		trigger = strings.ReplaceAll(trigger, "permission.mode='full_access'", constraint)
		trigger = strings.Replace(trigger, "WHEN 'move' THEN 'move_file' ELSE 'replace_file' END", "WHEN 'move' THEN 'move_file' WHEN 'delete' THEN 'delete_file' ELSE 'replace_file' END", 1)
		statements = append(statements, trigger)
	}
	// The adapter and immutable mode scope still enforce disabled networking.
	// A three-mode permission preference must not masquerade as that isolation.
	const legacyPreset = "permission.mode = 'workspace_access' AND permission.network_scope = 'disabled'"
	for _, statement := range threadStandardCodeContinuationStatements {
		if strings.HasPrefix(statement, "CREATE TRIGGER ") {
			if strings.Count(statement, legacyPreset) != 1 {
				panic("file operation migration lost Standard Code permission anchor")
			}
			statement = strings.Replace(statement, legacyPreset, "("+legacyPreset+" OR (permission.protocol_version='run_execution_permission.v2' AND permission.mode IN ('ask','auto','full') AND permission.network_scope='per_operation'))", 1)
		}
		statements = append(statements, statement)
	}
	return statements
}
