package store

// Failed native requests have no executable call row. Keep their bounded,
// redacted diagnostic separately, under the original terminal attempt identity.
var supervisorToolRejectionStatements = []string{
	`CREATE TABLE run_supervisor_tool_rejections (
		run_id TEXT NOT NULL,
		turn INTEGER NOT NULL CHECK(turn > 0),
		attempt_id TEXT NOT NULL CHECK(length(attempt_id) > 0),
		model_attempt INTEGER NOT NULL CHECK(model_attempt > 0),
		diagnostic_json TEXT NOT NULL CHECK(json_valid(diagnostic_json) AND length(CAST(diagnostic_json AS BLOB)) BETWEEN 2 AND 65536),
		diagnostic_sha256 TEXT NOT NULL CHECK(length(diagnostic_sha256) = 64),
		created_at TEXT NOT NULL,
		PRIMARY KEY(run_id, turn, attempt_id, model_attempt),
		FOREIGN KEY(run_id) REFERENCES runs(id)
	);`,
	`CREATE TRIGGER trg_supervisor_tool_rejection_immutable BEFORE UPDATE ON run_supervisor_tool_rejections
	 BEGIN SELECT RAISE(ABORT, 'rejected tool diagnostic is immutable'); END;`,
}
