package store

var githubReviewStatements = []string{
	// GitHub evidence reads are ordinary Agent Code tools: their payload and
	// result are durable Supervisor records, while their authority is issued by
	// Go for the exact Run/workspace just like the workspace tools. SQLite
	// cannot widen these CHECK constraints in place, so preserve the v123 rows
	// while rebuilding the ledger.
	`DROP TRIGGER trg_supervisor_tool_call_model_attempt;`,
	`DROP TRIGGER trg_supervisor_tool_round_completion;`,
	`DROP INDEX idx_run_supervisor_tool_calls_pending;`,
	`ALTER TABLE run_supervisor_tool_calls RENAME TO run_supervisor_tool_calls_v123;`,
	`CREATE TABLE run_supervisor_tool_calls (
		run_id TEXT NOT NULL,
		turn INTEGER NOT NULL,
		attempt_id TEXT NOT NULL,
		round INTEGER NOT NULL,
		position INTEGER NOT NULL,
		model_attempt INTEGER NOT NULL,
		call_id TEXT NOT NULL,
		tool_name TEXT NOT NULL,
		payload_json TEXT NOT NULL,
		authority_json TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL,
		result_json TEXT NOT NULL DEFAULT '',
		error_code TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		completed_at TEXT,
		PRIMARY KEY(run_id, turn, attempt_id, round, position),
		UNIQUE(run_id, turn, attempt_id, call_id),
		FOREIGN KEY(run_id, turn, attempt_id, round)
			REFERENCES run_supervisor_tool_rounds(run_id, turn, attempt_id, round) ON DELETE CASCADE,
		CHECK(position BETWEEN 1 AND 4),
		CHECK(model_attempt > 0),
		CHECK(tool_name IN ('work_item_create', 'note_create',
			'specialist_delegation_propose', 'child_task_propose',
			'plan_delivery_propose', 'controlled_command_propose',
			'one_shot_command_propose', 'host_command_propose',
			'sandbox_docker_run_propose', 'skill_candidate_propose', 'debug_terminal',
			'workspace_list', 'workspace_read', 'workspace_glob', 'workspace_grep',
			'workspace_change', 'workspace_apply', 'workspace_delete', 'command_runtime',
			'mcp_tool_call', 'github_review_evidence_list', 'github_review_evidence_read')),
		CHECK((tool_name IN ('workspace_list', 'workspace_read', 'workspace_glob',
			'workspace_grep', 'workspace_change', 'workspace_apply', 'workspace_delete',
			'github_review_evidence_list', 'github_review_evidence_read')
			AND length(authority_json) BETWEEN 2 AND 4096 AND json_valid(authority_json) = 1)
			OR (tool_name NOT IN ('workspace_list', 'workspace_read', 'workspace_glob',
				'workspace_grep', 'workspace_change', 'workspace_apply', 'workspace_delete',
				'github_review_evidence_list', 'github_review_evidence_read')
				AND authority_json = '')),
		CHECK(status IN ('pending', 'completed', 'denied', 'failed')),
		CHECK((status = 'pending' AND result_json = '' AND error_code = '' AND completed_at IS NULL)
			OR (status = 'completed' AND length(result_json) > 0 AND error_code = '' AND completed_at IS NOT NULL)
			OR (status IN ('denied', 'failed') AND length(result_json) > 0 AND length(error_code) > 0
				AND completed_at IS NOT NULL))
	);`,
	`INSERT INTO run_supervisor_tool_calls
		(run_id, turn, attempt_id, round, position, model_attempt, call_id, tool_name,
		payload_json, authority_json, status, result_json, error_code, created_at, completed_at)
		SELECT run_id, turn, attempt_id, round, position, model_attempt, call_id, tool_name,
		payload_json, authority_json, status, result_json, error_code, created_at, completed_at
		FROM run_supervisor_tool_calls_v123;`,
	`DROP TABLE run_supervisor_tool_calls_v123;`,
	`CREATE INDEX idx_run_supervisor_tool_calls_pending
		ON run_supervisor_tool_calls(run_id, turn, attempt_id, status, round, position);`,
	`CREATE TRIGGER trg_supervisor_tool_call_model_attempt
		BEFORE INSERT ON run_supervisor_tool_calls
		WHEN NOT EXISTS (
			SELECT 1 FROM run_supervisor_tool_rounds
			WHERE run_id = NEW.run_id AND turn = NEW.turn AND attempt_id = NEW.attempt_id
				AND round = NEW.round AND model_attempt = NEW.model_attempt
		)
		BEGIN
			SELECT RAISE(ABORT, 'supervisor tool call model attempt mismatch');
		END;`,
	`CREATE TRIGGER trg_supervisor_tool_round_completion
		BEFORE UPDATE OF completed_at ON run_supervisor_tool_rounds
		WHEN NEW.completed_at IS NOT NULL AND EXISTS (
			SELECT 1 FROM run_supervisor_tool_calls
			WHERE run_id = NEW.run_id AND turn = NEW.turn AND attempt_id = NEW.attempt_id
				AND round = NEW.round AND status = 'pending'
		)
		BEGIN
			SELECT RAISE(ABORT, 'supervisor tool round still has pending calls');
		END;`,

	`CREATE TABLE github_review_connections (
		id TEXT PRIMARY KEY,
		protocol_version TEXT NOT NULL,
		host TEXT NOT NULL,
		owner TEXT NOT NULL,
		repository TEXT NOT NULL,
		full_name TEXT NOT NULL UNIQUE,
		credential_name TEXT NOT NULL,
		auth_kind TEXT NOT NULL,
		client_id TEXT NOT NULL DEFAULT '',
		network_json TEXT NOT NULL,
		enabled INTEGER NOT NULL,
		generation INTEGER NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		CHECK(protocol_version = 'github-review-connection.v1'),
		CHECK(host = 'github.com' AND full_name = owner || '/' || repository),
		CHECK(length(owner) BETWEEN 1 AND 100 AND length(repository) BETWEEN 1 AND 100),
		CHECK(length(credential_name) BETWEEN 1 AND 64),
		CHECK(auth_kind IN ('github_app_device','oauth_user','fine_grained_pat')),
		CHECK((auth_kind = 'github_app_device' AND length(client_id) BETWEEN 1 AND 128)
			OR (auth_kind <> 'github_app_device' AND client_id = '')),
		CHECK(json_valid(network_json) AND length(network_json) BETWEEN 2 AND 16384),
		CHECK(enabled IN (0,1) AND generation > 0),
		CHECK(julianday(created_at) IS NOT NULL AND julianday(updated_at) IS NOT NULL
			AND julianday(updated_at) >= julianday(created_at))
	);`,
	`CREATE INDEX idx_github_review_connections_enabled
		ON github_review_connections(enabled, full_name);`,
	`CREATE TRIGGER trg_github_review_connection_identity_immutable
		BEFORE UPDATE ON github_review_connections
		WHEN OLD.id <> NEW.id OR OLD.protocol_version <> NEW.protocol_version
			OR OLD.host <> NEW.host OR OLD.owner <> NEW.owner
			OR OLD.repository <> NEW.repository OR OLD.full_name <> NEW.full_name
			OR OLD.created_at <> NEW.created_at
		BEGIN SELECT RAISE(ABORT, 'GitHub review connection identity is immutable'); END;`,
	`CREATE TRIGGER trg_github_review_connection_generation
		BEFORE UPDATE ON github_review_connections
		WHEN NEW.generation <> OLD.generation + 1 OR julianday(NEW.updated_at) < julianday(OLD.updated_at)
		BEGIN SELECT RAISE(ABORT, 'GitHub review connection update requires next generation'); END;`,

	`CREATE TABLE github_review_snapshots (
		id TEXT PRIMARY KEY,
		protocol_version TEXT NOT NULL,
		connection_id TEXT NOT NULL,
		repository_full_name TEXT NOT NULL,
		pr_number INTEGER NOT NULL,
		base_sha TEXT NOT NULL,
		head_sha TEXT NOT NULL,
		merge_base_sha TEXT NOT NULL,
		fingerprint TEXT NOT NULL UNIQUE,
		state TEXT NOT NULL,
		snapshot_json TEXT NOT NULL,
		fetched_at TEXT NOT NULL,
		created_at TEXT NOT NULL,
		FOREIGN KEY(connection_id) REFERENCES github_review_connections(id) ON DELETE RESTRICT,
		CHECK(protocol_version = 'github-review-snapshot.v1'),
		CHECK(pr_number > 0),
		CHECK(length(base_sha) IN (40,64) AND length(head_sha) IN (40,64)
			AND length(merge_base_sha) IN (40,64)),
		CHECK(length(fingerprint) = 64 AND fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(state IN ('verified','partial','stale','unavailable','not_run')),
		CHECK(json_valid(snapshot_json) AND length(snapshot_json) BETWEEN 2 AND 16777216),
		CHECK(julianday(fetched_at) IS NOT NULL AND julianday(created_at) IS NOT NULL)
	);`,
	`CREATE INDEX idx_github_review_snapshots_pr
		ON github_review_snapshots(connection_id, pr_number, fetched_at DESC, id DESC);`,
	`CREATE TRIGGER trg_github_review_snapshot_immutable
		BEFORE UPDATE ON github_review_snapshots
		BEGIN SELECT RAISE(ABORT, 'GitHub review snapshot is immutable'); END;`,
	`CREATE TRIGGER trg_github_review_snapshot_delete_immutable
		BEFORE DELETE ON github_review_snapshots
		BEGIN SELECT RAISE(ABORT, 'GitHub review snapshot audit cannot be deleted'); END;`,

	`CREATE TABLE github_review_evidence_graphs (
		id TEXT PRIMARY KEY,
		protocol_version TEXT NOT NULL,
		run_id TEXT NOT NULL,
		workspace_id TEXT NOT NULL,
		snapshot_id TEXT NOT NULL,
		fingerprint TEXT NOT NULL,
		state TEXT NOT NULL,
		graph_json TEXT NOT NULL,
		created_at TEXT NOT NULL,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(workspace_id) REFERENCES workspaces(id) ON DELETE RESTRICT,
		FOREIGN KEY(snapshot_id) REFERENCES github_review_snapshots(id) ON DELETE RESTRICT,
		CHECK(protocol_version = 'github-review-evidence.v1'),
		CHECK(length(fingerprint) = 64 AND fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(state IN ('verified','partial','stale','unavailable','not_run')),
		CHECK(json_valid(graph_json) AND length(graph_json) BETWEEN 2 AND 16777216),
		CHECK(julianday(created_at) IS NOT NULL)
	);`,
	`CREATE INDEX idx_github_review_evidence_run
		ON github_review_evidence_graphs(run_id, created_at DESC, id DESC);`,
	`CREATE INDEX idx_github_review_evidence_fingerprint
		ON github_review_evidence_graphs(fingerprint);`,
	`CREATE TRIGGER trg_github_review_evidence_immutable
		BEFORE UPDATE ON github_review_evidence_graphs
		BEGIN SELECT RAISE(ABORT, 'GitHub review evidence graph is immutable'); END;`,
	`CREATE TRIGGER trg_github_review_evidence_delete_immutable
		BEFORE DELETE ON github_review_evidence_graphs
		BEGIN SELECT RAISE(ABORT, 'GitHub review evidence audit cannot be deleted'); END;`,

	`CREATE TABLE github_review_write_operations (
		id TEXT PRIMARY KEY,
		protocol_version TEXT NOT NULL,
		operation_key_sha256 TEXT NOT NULL UNIQUE,
		request_fingerprint TEXT NOT NULL,
		approval_fingerprint TEXT NOT NULL,
		approval_id TEXT,
		run_id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		workspace_id TEXT NOT NULL,
		connection_id TEXT NOT NULL,
		operation TEXT NOT NULL,
		spec_json TEXT NOT NULL,
		preview_json TEXT NOT NULL,
		capability_generation TEXT NOT NULL,
		base_sha TEXT NOT NULL,
		head_sha TEXT NOT NULL,
		merge_base_sha TEXT NOT NULL,
		status TEXT NOT NULL,
		receipt_json TEXT NOT NULL DEFAULT '{}',
		error_code TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		started_at TEXT,
		completed_at TEXT,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(session_id) REFERENCES sessions(id) ON DELETE RESTRICT,
		FOREIGN KEY(workspace_id) REFERENCES workspaces(id) ON DELETE RESTRICT,
		FOREIGN KEY(connection_id) REFERENCES github_review_connections(id) ON DELETE RESTRICT,
		FOREIGN KEY(approval_id) REFERENCES tool_approvals(id) ON DELETE RESTRICT,
		CHECK(protocol_version = 'github-review-write.v1'),
		CHECK(length(operation_key_sha256) = 64 AND operation_key_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(request_fingerprint) = 64 AND request_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(approval_fingerprint) = 64 AND approval_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(capability_generation) = 64 AND capability_generation NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(base_sha) IN (40,64) AND length(head_sha) IN (40,64)
			AND length(merge_base_sha) IN (40,64)),
		CHECK(operation IN ('reply','resolve','unresolve','submit_review','request_reviewer')),
		CHECK(json_valid(spec_json) AND length(spec_json) BETWEEN 2 AND 131072),
		CHECK(json_valid(preview_json) AND length(preview_json) BETWEEN 2 AND 131072),
		CHECK(json_valid(receipt_json) AND length(receipt_json) BETWEEN 2 AND 131072),
		CHECK(status IN ('proposed','running','succeeded','recovered','failed')),
		CHECK(length(error_code) <= 64 AND julianday(created_at) IS NOT NULL),
		CHECK((status = 'proposed' AND approval_id IS NULL AND started_at IS NULL AND completed_at IS NULL)
			OR (status = 'running' AND approval_id IS NOT NULL AND julianday(started_at) IS NOT NULL
				AND completed_at IS NULL)
			OR (status IN ('succeeded','recovered','failed') AND approval_id IS NOT NULL
				AND julianday(started_at) IS NOT NULL AND julianday(completed_at) IS NOT NULL
				AND julianday(completed_at) >= julianday(started_at)))
	);`,
	`CREATE INDEX idx_github_review_write_run
		ON github_review_write_operations(run_id, created_at DESC, id DESC);`,
	`CREATE INDEX idx_github_review_write_recovery
		ON github_review_write_operations(status, created_at, id);`,
	`CREATE TRIGGER trg_github_review_write_identity_immutable
		BEFORE UPDATE ON github_review_write_operations
		WHEN OLD.id <> NEW.id OR OLD.protocol_version <> NEW.protocol_version
			OR OLD.operation_key_sha256 <> NEW.operation_key_sha256
			OR OLD.request_fingerprint <> NEW.request_fingerprint
			OR OLD.approval_fingerprint <> NEW.approval_fingerprint
			OR OLD.run_id <> NEW.run_id OR OLD.session_id <> NEW.session_id
			OR OLD.workspace_id <> NEW.workspace_id OR OLD.connection_id <> NEW.connection_id
			OR OLD.operation <> NEW.operation OR OLD.spec_json <> NEW.spec_json
			OR OLD.preview_json <> NEW.preview_json
			OR OLD.capability_generation <> NEW.capability_generation
			OR OLD.base_sha <> NEW.base_sha OR OLD.head_sha <> NEW.head_sha
			OR OLD.merge_base_sha <> NEW.merge_base_sha OR OLD.created_at <> NEW.created_at
		BEGIN SELECT RAISE(ABORT, 'GitHub review write identity is immutable'); END;`,
	`CREATE TRIGGER trg_github_review_write_terminal_immutable
		BEFORE UPDATE ON github_review_write_operations
		WHEN OLD.status IN ('succeeded','recovered','failed')
		BEGIN SELECT RAISE(ABORT, 'terminal GitHub review write is immutable'); END;`,
	`CREATE TRIGGER trg_github_review_write_delete_immutable
		BEFORE DELETE ON github_review_write_operations
		BEGIN SELECT RAISE(ABORT, 'GitHub review write audit cannot be deleted'); END;`,
}
