package store

// Schema downgrade fixtures are cumulative: historical migration tests create a
// database at the latest schema and then remove newer objects before reopening
// it. Keep extension-runtime objects at the front of that chain so those tests
// continue to exercise the migration they name instead of encountering a gap.
func removeSchemaV121ForTestStatements() []string {
	return []string{
		`DROP TABLE plugin_hook_audits`,
		`DROP TABLE plugin_publisher_reviews`,
		`DROP TABLE plugin_publishers`,
		`DROP TABLE plugin_installation_transitions`,
		`DROP TABLE plugin_installations`,
		`DROP TABLE plugin_objects`,
		`DELETE FROM schema_migrations WHERE version = 121`,
	}
}

func removeSchemaV120ForTestStatements() []string {
	return append(removeSchemaV121ForTestStatements(), []string{
		`DROP TABLE mcp_client_calls`,
		`DROP TABLE mcp_client_servers`,
		`DELETE FROM schema_migrations WHERE version = 120`,
	}...)
}
