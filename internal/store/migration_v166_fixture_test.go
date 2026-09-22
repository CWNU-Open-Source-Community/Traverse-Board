package store

import "testing"

// Current queue writers/readers and Supervisor context queries need v168's four
// message columns and two receipt tables. This fixture-only compatibility does
// not record a migration or change v165/v166's handoff and authorization guards.
// Its restore rejects revisions, edited identities, and attachment evidence,
// then verifies the exact original schema, migration ledger, and foreign keys.
func addV166FixtureQueueCompatibility(t testing.TB, state *SQLiteStore) func() {
	t.Helper()
	restore := append([]string{}, removeSchemaV168QueueForTestStatements()...)
	restore = append(restore,
		`ALTER TABLE operator_steering_messages DROP COLUMN revision;`,
		`ALTER TABLE operator_steering_messages DROP COLUMN original_content;`,
		`ALTER TABLE operator_steering_messages DROP COLUMN original_content_sha256;`,
		`ALTER TABLE operator_steering_messages DROP COLUMN edited_at;`,
		migrationTriggerBeforeForTest("trg_operator_steering_update_monotonic", 168))
	return withLegacySeedSchema(t, state, operatorSteeringRevisionStatements, restore)
}
