package store

import "strings"

// Extend the existing selection tables. Historical rows, rowids and receipts are
// copied verbatim; new items refer to the real Plugin lifecycle instead of an
// invented legacy installation or object key.
func pluginSkillSelectionStatements() []string {
	var tables, recreate, statements []string
	for _, statement := range externalSkillSelectionStatements {
		if strings.HasPrefix(statement, "CREATE TABLE run_external_skill_selections (") || strings.HasPrefix(statement, "CREATE TABLE run_external_skill_selection_items (") {
			tables = append(tables, statement)
		} else if (strings.HasPrefix(statement, "CREATE TRIGGER ") || strings.HasPrefix(statement, "CREATE INDEX ")) &&
			(strings.Contains(statement, "run_external_skill_selections") || strings.Contains(statement, "run_external_skill_selection_items")) {
			fields := strings.Fields(statement)
			statements = append(statements, "DROP "+fields[1]+" "+fields[2]+";")
			if strings.HasPrefix(statement, "CREATE TRIGGER trg_run_external_skill_selection_item_insert\n") {
				statement = replacePluginSelectionAnchor(statement, "WHEN NOT EXISTS (", "WHEN NEW.plugin_binding_json = '{}' AND NOT EXISTS (")
			}
			if strings.HasPrefix(statement, "CREATE TRIGGER trg_run_external_skill_selection_operation_insert\n") {
				statement = replacePluginSelectionAnchor(statement, "AND selection.created_at = NEW.created_at", `AND selection.created_at = NEW.created_at
     AND (selection.protocol_version = 'external_skill_selection.v2') = EXISTS (
      SELECT 1 FROM run_external_skill_selection_items item
      WHERE item.selection_id = selection.id AND item.plugin_installation_id IS NOT NULL)`)
			}
			recreate = append(recreate, statement)
		}
	}
	if len(tables) != 2 {
		panic("v184 requires the immutable v70 selection tables")
	}
	for _, view := range externalSkillProjectionStatements {
		statements = append(statements, "DROP VIEW "+strings.Fields(view)[2]+";")
		recreate = append(recreate, view)
	}
	header := replacePluginSelectionAnchor(tables[0], "CREATE TABLE run_external_skill_selections (", "CREATE TABLE run_external_skill_selections_v184 (")
	header = replacePluginSelectionAnchor(header, "CHECK(protocol_version = 'external_skill_selection.v1')", "CHECK(protocol_version IN ('external_skill_selection.v1','external_skill_selection.v2'))")
	items := replacePluginSelectionAnchor(tables[1], "CREATE TABLE run_external_skill_selection_items (", "CREATE TABLE run_external_skill_selection_items_v184 (")
	items = replacePluginSelectionAnchor(items, "specialist_eligible INTEGER NOT NULL,", `specialist_eligible INTEGER NOT NULL,
  plugin_binding_json TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(plugin_binding_json) AND json_type(plugin_binding_json) = 'object'),
  legacy_installation_id TEXT,
  plugin_installation_id TEXT,`)
	items = replacePluginSelectionAnchor(items, "FOREIGN KEY(installation_id) REFERENCES skill_package_installations(id) ON DELETE RESTRICT,", `FOREIGN KEY(legacy_installation_id) REFERENCES skill_package_installations(id) ON DELETE RESTRICT,
  FOREIGN KEY(plugin_installation_id) REFERENCES plugin_installations(id) ON DELETE RESTRICT,`)
	items = replacePluginSelectionAnchor(items, `CHECK(length(install_result_fingerprint) = 64
			AND install_result_fingerprint NOT GLOB '*[^0-9a-f]*'),`, `CHECK(COALESCE((plugin_binding_json = '{}' AND legacy_installation_id = installation_id AND plugin_installation_id IS NULL
   AND length(install_result_fingerprint) = 64 AND install_result_fingerprint NOT GLOB '*[^0-9a-f]*'
   AND object_key = 'sha256/' || substr(archive_sha256, 1, 2) || '/' || archive_sha256 || '.zip')
   OR (plugin_binding_json != '{}' AND plugin_installation_id = installation_id AND legacy_installation_id IS NULL
    AND install_result_fingerprint = '' AND object_key = ''
    AND json_type(plugin_binding_json, '$.package_id') = 'text'
    AND length(json_extract(plugin_binding_json, '$.package_id')) BETWEEN 1 AND 256
    AND json_type(plugin_binding_json, '$.component_id') = 'text'
    AND length(json_extract(plugin_binding_json, '$.component_id')) BETWEEN 1 AND 256
    AND json_extract(plugin_binding_json, '$.revision') = archive_sha256
    AND json_type(plugin_binding_json, '$.generation') = 'integer'
    AND json_extract(plugin_binding_json, '$.generation') >= 1), 0)),`)
	items = replacePluginSelectionAnchor(items, "\t\tCHECK(object_key = 'sha256/' || substr(archive_sha256, 1, 2) || '/' || archive_sha256 || '.zip'),\n", "")
	statements = append(statements, header, items,
		`INSERT INTO run_external_skill_selections_v184 SELECT rowid, * FROM run_external_skill_selections;`,
		`INSERT INTO run_external_skill_selection_items_v184 SELECT rowid, *, '{}', installation_id, NULL FROM run_external_skill_selection_items;`,
		`DROP TABLE run_external_skill_selection_items;`,
		`DROP TABLE run_external_skill_selections;`,
		`ALTER TABLE run_external_skill_selections_v184 RENAME TO run_external_skill_selections;`,
		`ALTER TABLE run_external_skill_selection_items_v184 RENAME TO run_external_skill_selection_items;`)
	// Explicit rowid columns preserve storage identity as well as all old values.
	for i, statement := range statements {
		if strings.HasPrefix(statement, "INSERT INTO run_external_skill_selections_v184 SELECT") {
			statements[i] = strings.Replace(statement, "_v184 SELECT", "_v184 (rowid, id, run_id, mission_id, mode_snapshot_id, mode_revision, protocol_version, surface, profile, token_budget, token_upper_bound, item_count, selection_fingerprint, requested_by, operator_confirmed, context_delivery_authorized, tool_capability_grant, created_at) SELECT", 1)
		}
		if strings.HasPrefix(statement, "INSERT INTO run_external_skill_selection_items_v184 SELECT") {
			statements[i] = strings.Replace(statement, "_v184 SELECT", "_v184 (rowid, selection_id, ordinal, installation_id, installation_fingerprint, install_result_fingerprint, name, version, surface, content_sha256, content_bytes, token_upper_bound, archive_sha256, archive_bytes, package_fingerprint, object_key, trust_class, tool_dependency_count, specialist_eligible, plugin_binding_json, legacy_installation_id, plugin_installation_id) SELECT", 1)
		}
	}
	statements = append(statements, recreate...)
	return append(statements, `CREATE TRIGGER trg_run_external_skill_selection_plugin_item_insert
 BEFORE INSERT ON run_external_skill_selection_items
 WHEN NEW.plugin_binding_json != '{}' AND NOT EXISTS (
  SELECT 1 FROM run_external_skill_selections selection
  JOIN plugin_installations installation ON installation.id = NEW.plugin_installation_id
  JOIN plugin_objects object ON object.archive_sha256 = installation.archive_sha256
   AND object.package_fingerprint = installation.package_fingerprint
  WHERE selection.id = NEW.selection_id AND selection.protocol_version = 'external_skill_selection.v2'
   AND NEW.ordinal = 1 + (SELECT COUNT(*) FROM run_external_skill_selection_items existing WHERE existing.selection_id = NEW.selection_id)
   AND installation.protocol_version = 'plugin-installation.v2' AND installation.state = 'enabled'
   AND json_extract(installation.source_json, '$.surface') = selection.surface AND NEW.surface = selection.surface
   AND EXISTS (SELECT 1 FROM json_each(installation.enabled_capabilities_json) WHERE value = 'skills')
   AND installation.plugin_id = json_extract(NEW.plugin_binding_json, '$.package_id')
   AND installation.generation = json_extract(NEW.plugin_binding_json, '$.generation')
   AND installation.archive_sha256 = NEW.archive_sha256 AND installation.archive_bytes = NEW.archive_bytes
   AND installation.package_fingerprint = NEW.package_fingerprint
   AND json_extract(installation.manifest_json, '$.format') = 'traverse-skill'
   AND json_array_length(installation.manifest_json, '$.skills') = 1
   AND json_extract(installation.manifest_json, '$.skills[0].instructions.Component.PackageID') = installation.plugin_id
   AND json_extract(installation.manifest_json, '$.skills[0].instructions.Component.ComponentID') = json_extract(NEW.plugin_binding_json, '$.component_id')
   AND json_extract(installation.manifest_json, '$.skills[0].instructions.SHA256') = NEW.content_sha256
   AND json_extract(installation.manifest_json, '$.legacy.Manifest.name') = NEW.name
   AND json_extract(installation.manifest_json, '$.legacy.Manifest.version') = NEW.version
   AND json_extract(installation.manifest_json, '$.legacy.Manifest.content_sha256') = NEW.content_sha256
   AND json_extract(installation.manifest_json, '$.legacy.Manifest.content_bytes') = NEW.content_bytes
   AND json_extract(installation.manifest_json, '$.legacy.Manifest.content_token_upper_bound') = NEW.token_upper_bound
   AND json_array_length(installation.manifest_json, '$.legacy.Manifest.tool_dependencies') = NEW.tool_dependency_count)
 BEGIN SELECT RAISE(ABORT, 'external Skill selection Plugin binding is invalid'); END;`)
}

func replacePluginSelectionAnchor(statement, old, replacement string) string {
	if strings.Count(statement, old) != 1 {
		panic("v184 lost an immutable selection schema anchor: " + old)
	}
	return strings.Replace(statement, old, replacement, 1)
}
