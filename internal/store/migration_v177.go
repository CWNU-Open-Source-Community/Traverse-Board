package store

import "strings"

// Extend the existing installation table without changing v1 rows, object
// retention, review transitions or any historical migration checksum.
func portablePluginInstallationStatements() []string {
	var create string
	var recreate []string
	for _, statement := range pluginRuntimeStatements {
		if strings.HasPrefix(statement, "CREATE TABLE plugin_installations (") {
			create = statement
		}
		if (strings.HasPrefix(statement, "CREATE INDEX") || strings.HasPrefix(statement, "CREATE UNIQUE INDEX") || strings.HasPrefix(statement, "CREATE TRIGGER")) && strings.Contains(statement, "ON plugin_installations") {
			recreate = append(recreate, statement)
		}
	}
	if create == "" || len(recreate) != 6 {
		panic("portable installation migration lost its v121 anchors")
	}
	create = strings.Replace(create, "CREATE TABLE plugin_installations (", "CREATE TABLE plugin_installations_v177 (", 1)
	create = strings.Replace(create, "CHECK(protocol_version = 'plugin-installation.v1')", "CHECK(protocol_version IN ('plugin-installation.v1','plugin-installation.v2'))", 1)
	create = strings.Replace(create,
		"CHECK(length(plugin_version) BETWEEN 5 AND 64 AND length(publisher) BETWEEN 1 AND 256)",
		`CHECK(COALESCE((protocol_version='plugin-installation.v1'
			AND length(plugin_version) BETWEEN 5 AND 64 AND length(publisher) BETWEEN 1 AND 256)
			OR (protocol_version='plugin-installation.v2' AND length(plugin_version)=64
				AND plugin_version NOT GLOB '*[^0-9a-f]*' AND publisher=''
				AND signature_present=0 AND signature_valid=0
				AND publisher_fingerprint='' AND publisher_public_key=''
				AND json_extract(manifest_json,'$.protocol_version')='agent-package-snapshot.v1'
				AND json_extract(manifest_json,'$.package_id')=plugin_id
				AND json_extract(manifest_json,'$.revision')=plugin_version
				AND archive_sha256=plugin_version
				AND length(json_extract(source_json,'$.operation_key_digest'))=64
				AND json_extract(source_json,'$.operation_key_digest') NOT GLOB '*[^0-9a-f]*'
				AND id='plugin-import-' || json_extract(source_json,'$.operation_key_digest')
				AND json_extract(source_json,'$.surface') IN ('code','cyber')),0))`, 1)
	statements := []string{create,
		`INSERT INTO plugin_installations_v177 SELECT * FROM plugin_installations;`,
		`DROP TABLE plugin_installations;`,
		`ALTER TABLE plugin_installations_v177 RENAME TO plugin_installations;`}
	return append(statements, recreate...)
}
