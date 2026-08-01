package store

var runBrowserCDPPermissionStatements = []string{
	`CREATE TABLE run_browser_cdp_permission_snapshots (
		id TEXT PRIMARY KEY,
		run_id TEXT NOT NULL,
		mission_id TEXT NOT NULL,
		revision INTEGER NOT NULL,
		protocol_version TEXT NOT NULL,
		mode TEXT NOT NULL,
		navigate_allowed INTEGER NOT NULL,
		dom_snapshot_allowed INTEGER NOT NULL,
		screenshot_allowed INTEGER NOT NULL,
		request_capture_allowed INTEGER NOT NULL,
		request_mutation_allowed INTEGER NOT NULL,
		request_replay_allowed INTEGER NOT NULL,
		cookie_access_allowed INTEGER NOT NULL,
		arbitrary_method_allowed INTEGER NOT NULL,
		risk_tier TEXT NOT NULL,
		required_gate TEXT NOT NULL,
		policy_version TEXT NOT NULL,
		operator_confirmed INTEGER NOT NULL,
		transport_enabled INTEGER NOT NULL,
		browser_start_authorized INTEGER NOT NULL,
		runtime_authorized INTEGER NOT NULL,
		capability_grant INTEGER NOT NULL,
		requested_by TEXT NOT NULL,
		reason TEXT NOT NULL,
		created_at TEXT NOT NULL,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(mission_id) REFERENCES missions(id) ON DELETE RESTRICT,
		UNIQUE(run_id, revision),
		CHECK(revision > 0),
		CHECK(protocol_version = 'run_browser_cdp_permission.v1'),
		CHECK(policy_version = 'browser_cdp_permission_policy.v1'),
		CHECK(transport_enabled = 0 AND browser_start_authorized = 0
			AND runtime_authorized = 0 AND capability_grant = 0),
		CHECK(
			(mode = 'restricted' AND navigate_allowed = 1
				AND dom_snapshot_allowed = 1 AND screenshot_allowed = 1
				AND request_capture_allowed = 0 AND request_mutation_allowed = 0
				AND request_replay_allowed = 0 AND cookie_access_allowed = 0
				AND arbitrary_method_allowed = 0 AND risk_tier = 'minimal'
				AND required_gate = 'browser_cdp_control' AND operator_confirmed = 0)
			OR
			(mode = 'full_debug' AND navigate_allowed = 1
				AND dom_snapshot_allowed = 1 AND screenshot_allowed = 1
				AND request_capture_allowed = 1 AND request_mutation_allowed = 1
				AND request_replay_allowed = 1 AND cookie_access_allowed = 1
				AND arbitrary_method_allowed = 1 AND risk_tier = 'high'
				AND required_gate = 'full_cdp_debug' AND operator_confirmed = 1)
		),
		CHECK(id = trim(id) AND length(id) BETWEEN 1 AND 256 AND instr(id, char(0)) = 0),
		CHECK(run_id = trim(run_id) AND length(run_id) BETWEEN 1 AND 256
			AND instr(run_id, char(0)) = 0),
		CHECK(mission_id = trim(mission_id) AND length(mission_id) BETWEEN 1 AND 256
			AND instr(mission_id, char(0)) = 0),
		CHECK(requested_by = trim(requested_by) AND length(requested_by) BETWEEN 1 AND 256
			AND instr(requested_by, char(0)) = 0),
		CHECK(reason = trim(reason) AND length(reason) BETWEEN 1 AND 1024
			AND instr(reason, char(0)) = 0)
	);`,
	`CREATE INDEX idx_run_browser_cdp_permission_snapshots_run_revision
		ON run_browser_cdp_permission_snapshots(run_id, revision DESC);`,
	`CREATE TABLE run_browser_cdp_permission_operations (
		operation_key_digest TEXT PRIMARY KEY,
		request_fingerprint TEXT NOT NULL,
		snapshot_id TEXT NOT NULL UNIQUE,
		run_id TEXT NOT NULL,
		requested_by TEXT NOT NULL,
		created_at TEXT NOT NULL,
		FOREIGN KEY(snapshot_id) REFERENCES run_browser_cdp_permission_snapshots(id)
			ON DELETE RESTRICT,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		CHECK(length(operation_key_digest) = 64
			AND operation_key_digest = lower(operation_key_digest)
			AND operation_key_digest NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(request_fingerprint) = 64
			AND request_fingerprint = lower(request_fingerprint)
			AND request_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(requested_by = trim(requested_by) AND length(requested_by) BETWEEN 1 AND 256
			AND instr(requested_by, char(0)) = 0)
	) WITHOUT ROWID;`,
	`INSERT INTO run_browser_cdp_permission_snapshots
		(id, run_id, mission_id, revision, protocol_version, mode,
		navigate_allowed, dom_snapshot_allowed, screenshot_allowed,
		request_capture_allowed, request_mutation_allowed, request_replay_allowed,
		cookie_access_allowed, arbitrary_method_allowed, risk_tier, required_gate,
		policy_version, operator_confirmed, transport_enabled,
		browser_start_authorized, runtime_authorized, capability_grant,
		requested_by, reason, created_at)
		SELECT printf('run-browser-cdp-v91-%016x', run.rowid), run.id, run.mission_id, 1,
			'run_browser_cdp_permission.v1', 'restricted', 1, 1, 1, 0, 0, 0, 0, 0,
			'minimal', 'browser_cdp_control', 'browser_cdp_permission_policy.v1',
			0, 0, 0, 0, 0, 'schema_v91',
			'legacy compatibility restricted browser CDP permission', run.created_at
		FROM runs run;`,
	`CREATE TRIGGER trg_run_browser_cdp_permission_snapshot_insert
		BEFORE INSERT ON run_browser_cdp_permission_snapshots
		WHEN NOT EXISTS (
			SELECT 1 FROM runs run
			WHERE run.id = NEW.run_id AND run.mission_id = NEW.mission_id
				AND julianday(NEW.created_at) >= julianday(run.created_at)
				AND (
					(NEW.revision = 1 AND NEW.mode = 'restricted'
						AND run.status = 'created' AND NOT EXISTS (
							SELECT 1 FROM run_browser_cdp_permission_snapshots existing
							WHERE existing.run_id = NEW.run_id
						))
					OR
					(NEW.revision > 1 AND run.status IN ('created', 'paused')
						AND NOT EXISTS (
							SELECT 1 FROM run_execution_leases lease
							WHERE lease.run_id = NEW.run_id AND lease.status = 'active'
								AND julianday(lease.expires_at) > julianday('now')
						)
						AND EXISTS (
							SELECT 1 FROM run_browser_cdp_permission_snapshots previous
							WHERE previous.run_id = NEW.run_id
								AND previous.revision = NEW.revision - 1
								AND previous.protocol_version = NEW.protocol_version
								AND previous.policy_version = NEW.policy_version
								AND julianday(NEW.created_at) >= julianday(previous.created_at)
						))
				)
		)
		BEGIN
			SELECT RAISE(ABORT, 'Run browser CDP permission binding or transition is invalid');
		END;`,
	`CREATE TRIGGER trg_run_browser_cdp_permission_operation_insert
		BEFORE INSERT ON run_browser_cdp_permission_operations
		WHEN NOT EXISTS (
			SELECT 1 FROM run_browser_cdp_permission_snapshots snapshot
			WHERE snapshot.id = NEW.snapshot_id AND snapshot.run_id = NEW.run_id
				AND snapshot.requested_by = NEW.requested_by
				AND snapshot.created_at = NEW.created_at AND snapshot.revision > 1
		)
		BEGIN
			SELECT RAISE(ABORT, 'Run browser CDP permission operation binding is invalid');
		END;`,
	`CREATE TRIGGER trg_run_browser_cdp_permission_snapshot_update_immutable
		BEFORE UPDATE ON run_browser_cdp_permission_snapshots BEGIN
			SELECT RAISE(ABORT, 'Run browser CDP permission snapshot cannot be updated');
		END;`,
	`CREATE TRIGGER trg_run_browser_cdp_permission_snapshot_delete_immutable
		BEFORE DELETE ON run_browser_cdp_permission_snapshots BEGIN
			SELECT RAISE(ABORT, 'Run browser CDP permission snapshot cannot be deleted');
		END;`,
	`CREATE TRIGGER trg_run_browser_cdp_permission_operation_update_immutable
		BEFORE UPDATE ON run_browser_cdp_permission_operations BEGIN
			SELECT RAISE(ABORT, 'Run browser CDP permission operation cannot be updated');
		END;`,
	`CREATE TRIGGER trg_run_browser_cdp_permission_operation_delete_immutable
		BEFORE DELETE ON run_browser_cdp_permission_operations BEGIN
			SELECT RAISE(ABORT, 'Run browser CDP permission operation cannot be deleted');
		END;`,
}
