package store

// extensionRuntimeStatements persists low-trust MCP Client descriptors and
// metadata-only invocation receipts. Executable targets, capability snapshots,
// and approval fingerprints are immutable-by-generation; bearer values and
// raw tool inputs/results have no column in this schema.
var extensionRuntimeStatements = []string{
	`DROP TRIGGER trg_supervisor_tool_call_model_attempt;`,
	`DROP TRIGGER trg_supervisor_tool_round_completion;`,
	`DROP INDEX idx_run_supervisor_tool_calls_pending;`,
	`ALTER TABLE run_supervisor_tool_calls RENAME TO run_supervisor_tool_calls_v118;`,
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
			'mcp_tool_call')),
		CHECK((tool_name IN ('workspace_list', 'workspace_read', 'workspace_glob',
			'workspace_grep', 'workspace_change', 'workspace_apply', 'workspace_delete')
			AND length(authority_json) BETWEEN 2 AND 4096 AND json_valid(authority_json) = 1)
			OR (tool_name NOT IN ('workspace_list', 'workspace_read', 'workspace_glob',
				'workspace_grep', 'workspace_change', 'workspace_apply', 'workspace_delete')
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
		FROM run_supervisor_tool_calls_v118;`,
	`DROP TABLE run_supervisor_tool_calls_v118;`,
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
	`CREATE TABLE mcp_client_servers (
		id TEXT PRIMARY KEY,
		protocol_version TEXT NOT NULL,
		name TEXT NOT NULL,
		transport TEXT NOT NULL,
		target TEXT NOT NULL,
		scope TEXT NOT NULL,
		run_id TEXT NOT NULL DEFAULT '',
		workspace_id TEXT NOT NULL,
		descriptor_json TEXT NOT NULL,
		descriptor_fingerprint TEXT NOT NULL,
		state TEXT NOT NULL,
		capability_json TEXT NOT NULL,
		capability_fingerprint TEXT NOT NULL DEFAULT '',
		approved_capability_fingerprint TEXT NOT NULL DEFAULT '',
		health TEXT NOT NULL,
		health_message TEXT NOT NULL DEFAULT '',
		discovery_lease_id TEXT NOT NULL DEFAULT '',
		discovery_lease_expires_at TEXT,
		generation INTEGER NOT NULL,
		reviewed_by TEXT NOT NULL DEFAULT '',
		reviewed_at TEXT,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		FOREIGN KEY(workspace_id) REFERENCES workspaces(id) ON DELETE RESTRICT,
		CHECK(protocol_version = 'mcp-client-server.v1'),
		CHECK(transport IN ('stdio', 'streamable_http')),
		CHECK(scope IN ('run', 'workspace')),
		CHECK((scope = 'run' AND length(run_id) BETWEEN 1 AND 256)
			OR (scope = 'workspace' AND run_id = '')),
		CHECK(json_valid(descriptor_json) AND length(CAST(descriptor_json AS BLOB))
			BETWEEN 2 AND 65536),
		CHECK(json_valid(capability_json) AND length(CAST(capability_json AS BLOB))
			BETWEEN 2 AND 1048576),
		CHECK(length(descriptor_fingerprint) = 64
			AND descriptor_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(capability_fingerprint = '' OR (length(capability_fingerprint) = 64
			AND capability_fingerprint NOT GLOB '*[^0-9a-f]*')),
		CHECK(approved_capability_fingerprint = '' OR
			(length(approved_capability_fingerprint) = 64
			AND approved_capability_fingerprint NOT GLOB '*[^0-9a-f]*')),
		CHECK(state IN ('staged', 'discovery_approved', 'capabilities_pending',
			'enabled', 'disabled', 'quarantined', 'revoked')),
		CHECK(health IN ('unknown', 'connecting', 'healthy', 'unavailable',
			'capability_drift')),
		CHECK((health = 'connecting' AND length(discovery_lease_id) BETWEEN 1 AND 256
			AND julianday(discovery_lease_expires_at) IS NOT NULL)
			OR (health != 'connecting' AND discovery_lease_id = ''
				AND discovery_lease_expires_at IS NULL)),
		CHECK(generation > 0),
		CHECK(length(id) BETWEEN 1 AND 256 AND length(name) BETWEEN 1 AND 256),
		CHECK(length(target) BETWEEN 1 AND 4096),
		CHECK(length(workspace_id) BETWEEN 1 AND 256),
		CHECK(length(health_message) <= 2048 AND length(reviewed_by) <= 256),
		CHECK(julianday(created_at) IS NOT NULL AND julianday(updated_at) IS NOT NULL
			AND julianday(updated_at) >= julianday(created_at)),
		CHECK(reviewed_at IS NULL OR julianday(reviewed_at) IS NOT NULL)
	);`,
	`CREATE INDEX idx_mcp_client_servers_scope_state
		ON mcp_client_servers(workspace_id, run_id, state, updated_at DESC, id);`,
	`CREATE TRIGGER trg_mcp_client_server_limit
		BEFORE INSERT ON mcp_client_servers
		WHEN (SELECT COUNT(*) FROM mcp_client_servers) >= 64
		BEGIN SELECT RAISE(ABORT, 'MCP client server limit exceeded'); END;`,
	`CREATE TRIGGER trg_mcp_client_server_descriptor_immutable
		BEFORE UPDATE ON mcp_client_servers
		WHEN NEW.descriptor_fingerprint != OLD.descriptor_fingerprint
			OR NEW.descriptor_json != OLD.descriptor_json
			OR NEW.id != OLD.id OR NEW.workspace_id != OLD.workspace_id
			OR NEW.run_id != OLD.run_id OR NEW.scope != OLD.scope
			OR NEW.protocol_version != OLD.protocol_version OR NEW.name != OLD.name
			OR NEW.transport != OLD.transport OR NEW.target != OLD.target
		BEGIN SELECT RAISE(ABORT, 'MCP client descriptor is immutable'); END;`,
	`CREATE TRIGGER trg_mcp_client_server_generation
		BEFORE UPDATE ON mcp_client_servers
		WHEN NEW.generation != OLD.generation + 1
		BEGIN SELECT RAISE(ABORT, 'MCP client server generation must advance exactly once'); END;`,
	`CREATE TRIGGER trg_mcp_client_servers_delete_immutable
		BEFORE DELETE ON mcp_client_servers
		BEGIN SELECT RAISE(ABORT, 'MCP client servers are retained for audit'); END;`,
	`CREATE TABLE mcp_client_calls (
		id TEXT PRIMARY KEY,
		protocol_version TEXT NOT NULL,
		run_id TEXT NOT NULL,
		workspace_id TEXT NOT NULL,
		server_id TEXT NOT NULL,
		tool_name TEXT NOT NULL,
		capability_fingerprint TEXT NOT NULL,
		arguments_sha256 TEXT NOT NULL,
		status TEXT NOT NULL,
		error_code TEXT NOT NULL DEFAULT '',
		result_bytes INTEGER NOT NULL,
		truncated INTEGER NOT NULL,
		started_at TEXT NOT NULL,
		completed_at TEXT NOT NULL,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(workspace_id) REFERENCES workspaces(id) ON DELETE RESTRICT,
		FOREIGN KEY(server_id) REFERENCES mcp_client_servers(id) ON DELETE RESTRICT,
		CHECK(protocol_version = 'mcp-client-call-audit.v1'),
		CHECK(status IN ('completed', 'denied', 'failed', 'cancelled', 'timed_out')),
		CHECK(length(capability_fingerprint) = 64
			AND capability_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(arguments_sha256) = 64 AND arguments_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(error_code) <= 128),
		CHECK(result_bytes BETWEEN 0 AND 131072),
		CHECK(truncated IN (0, 1)),
		CHECK(julianday(started_at) IS NOT NULL AND julianday(completed_at) IS NOT NULL
			AND julianday(completed_at) >= julianday(started_at))
	);`,
	`CREATE INDEX idx_mcp_client_calls_run_completed
		ON mcp_client_calls(run_id, completed_at DESC, id DESC);`,
	`CREATE TRIGGER trg_mcp_client_calls_update_immutable
		BEFORE UPDATE ON mcp_client_calls
		BEGIN SELECT RAISE(ABORT, 'MCP client call audit is immutable'); END;`,
	`CREATE TRIGGER trg_mcp_client_calls_delete_immutable
		BEFORE DELETE ON mcp_client_calls
		BEGIN SELECT RAISE(ABORT, 'MCP client call audit is immutable'); END;`,
}
