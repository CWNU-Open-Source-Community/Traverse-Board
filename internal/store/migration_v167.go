package store

// scheduledJobObservationConsentStatements adds an immutable, per-job receipt
// for the ordinary desktop's read-only, zero-model observation worker. Existing
// jobs are deliberately not backfilled.
var scheduledJobObservationConsentStatements = []string{
	`CREATE TABLE scheduled_job_observation_consents (
		job_id TEXT PRIMARY KEY,
		run_id TEXT NOT NULL,
		version INTEGER NOT NULL,
		confirmed_by TEXT NOT NULL,
		confirmed_at TEXT NOT NULL,
		operation_key_sha256 TEXT NOT NULL UNIQUE,
		request_fingerprint TEXT NOT NULL,
		FOREIGN KEY(job_id) REFERENCES scheduled_jobs(id) ON DELETE RESTRICT,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		CHECK(version = 1),
		CHECK(length(operation_key_sha256) = 64
			AND operation_key_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(request_fingerprint) = 64
			AND request_fingerprint NOT GLOB '*[^0-9a-f]*')
	);`,
	`CREATE INDEX idx_scheduled_job_observation_consents_run
		ON scheduled_job_observation_consents(run_id, confirmed_at, job_id);`,
	`CREATE TRIGGER trg_scheduled_job_observation_consent_insert
		BEFORE INSERT ON scheduled_job_observation_consents
		WHEN NOT EXISTS (
			SELECT 1 FROM scheduled_jobs job
			WHERE job.id = NEW.job_id AND job.owner_run_id = NEW.run_id
				AND job.execution_mode = 'read_only'
				AND CAST(json_extract(job.spec_json, '$.max_model_calls') AS INTEGER) = 0)
		BEGIN SELECT RAISE(ABORT, 'scheduled job observation consent source is invalid'); END;`,
	`CREATE TRIGGER trg_scheduled_job_observation_consent_update_immutable
		BEFORE UPDATE ON scheduled_job_observation_consents
		BEGIN SELECT RAISE(ABORT, 'scheduled job observation consent is immutable'); END;`,
	`CREATE TRIGGER trg_scheduled_job_observation_consent_delete_immutable
		BEFORE DELETE ON scheduled_job_observation_consents
		BEGIN SELECT RAISE(ABORT, 'scheduled job observation consent cannot be deleted'); END;`,
}
