package store

// supervisorAgentAttributionStatements adds append-only actor bindings without
// rebuilding the heavily constrained Supervisor and Command Runtime ledgers.
// Old Supervisor calls can be attributed to the unique root because their
// attempt_id is the root Supervisor attempt. Old command Jobs retained only an
// authority root, so their root attribution is explicitly marked legacy and
// carries no invented attempt identity. Rows without a provable root remain
// legacy_unknown.
var supervisorAgentAttributionStatements = []string{
	`CREATE TABLE run_supervisor_tool_call_agents (
		run_id TEXT NOT NULL,
		turn INTEGER NOT NULL,
		attempt_id TEXT NOT NULL,
		call_id TEXT NOT NULL,
		agent_id TEXT,
		agent_attempt_id TEXT,
		attribution_source TEXT NOT NULL,
		created_at TEXT NOT NULL,
		PRIMARY KEY(run_id, turn, attempt_id, call_id),
		FOREIGN KEY(run_id, turn, attempt_id, call_id)
			REFERENCES run_supervisor_tool_calls(run_id, turn, attempt_id, call_id)
			ON DELETE CASCADE,
		FOREIGN KEY(run_id, agent_id) REFERENCES agent_nodes(run_id, id)
			ON DELETE CASCADE,
		CHECK(attribution_source IN ('recorded', 'supervisor_root', 'legacy_root', 'legacy_unknown')),
		CHECK((attribution_source = 'legacy_unknown'
			AND agent_id IS NULL AND agent_attempt_id IS NULL)
			OR (attribution_source = 'legacy_root' AND agent_id IS NOT NULL)
			OR (attribution_source IN ('recorded', 'supervisor_root')
				AND agent_id IS NOT NULL AND agent_attempt_id IS NOT NULL))
	);`,
	`CREATE INDEX idx_supervisor_tool_call_agents_actor
		ON run_supervisor_tool_call_agents(run_id, agent_id, created_at, call_id);`,
	`INSERT INTO run_supervisor_tool_call_agents
		(run_id, turn, attempt_id, call_id, agent_id, agent_attempt_id,
		 attribution_source, created_at)
		SELECT call.run_id, call.turn, call.attempt_id, call.call_id,
			root.id, CASE WHEN root.id IS NULL THEN NULL ELSE call.attempt_id END,
			CASE WHEN root.id IS NULL THEN 'legacy_unknown' ELSE 'legacy_root' END,
			call.created_at
		FROM run_supervisor_tool_calls call
		LEFT JOIN agent_nodes root ON root.run_id = call.run_id
			AND root.parent_id IS NULL AND root.role = 'root';`,
	`CREATE TRIGGER trg_supervisor_tool_call_agent_immutable
		BEFORE UPDATE ON run_supervisor_tool_call_agents
		BEGIN
			SELECT RAISE(ABORT, 'Supervisor tool Agent attribution is immutable');
		END;`,
	`CREATE TABLE command_runtime_job_agents (
		job_id TEXT PRIMARY KEY,
		run_id TEXT NOT NULL,
		agent_id TEXT,
		agent_attempt_id TEXT,
		attribution_source TEXT NOT NULL,
		created_at TEXT NOT NULL,
		FOREIGN KEY(job_id) REFERENCES command_runtime_jobs(id) ON DELETE CASCADE,
		FOREIGN KEY(run_id, agent_id) REFERENCES agent_nodes(run_id, id)
			ON DELETE CASCADE,
		CHECK(attribution_source IN ('recorded', 'supervisor_root', 'operator_root', 'legacy_root', 'legacy_unknown')),
		CHECK((attribution_source = 'legacy_unknown'
			AND agent_id IS NULL AND agent_attempt_id IS NULL)
			OR (attribution_source = 'operator_root'
				AND agent_id IS NOT NULL AND agent_attempt_id IS NULL)
			OR (attribution_source = 'legacy_root' AND agent_id IS NOT NULL)
			OR (attribution_source IN ('recorded', 'supervisor_root')
				AND agent_id IS NOT NULL AND agent_attempt_id IS NOT NULL))
	);`,
	`CREATE INDEX idx_command_runtime_job_agents_actor
		ON command_runtime_job_agents(run_id, agent_id, created_at, job_id);`,
	`INSERT INTO command_runtime_job_agents
		(job_id, run_id, agent_id, agent_attempt_id, attribution_source, created_at)
		SELECT job.id, job.run_id, root.id, NULL,
			CASE WHEN root.id IS NULL THEN 'legacy_unknown' ELSE 'legacy_root' END,
			job.created_at
		FROM command_runtime_jobs job
		LEFT JOIN agent_nodes root ON root.run_id = job.run_id
			AND root.id = job.root_agent_id AND root.parent_id IS NULL
			AND root.role = 'root';`,
	`CREATE TRIGGER trg_command_runtime_job_agent_immutable
		BEFORE UPDATE ON command_runtime_job_agents
		BEGIN
			SELECT RAISE(ABORT, 'Command Runtime Agent attribution is immutable');
		END;`,
}
