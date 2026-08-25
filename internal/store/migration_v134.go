package store

import "strings"

var webEvidenceStatements = func() []string {
	createCalls := requireMigrationStatement(
		"CREATE TABLE run_supervisor_tool_calls_v131 (", commandRuntimeAdapterStatements)
	createCalls = replaceCommandRuntimeMigrationFragment(createCalls,
		"CREATE TABLE run_supervisor_tool_calls_v131 (",
		"CREATE TABLE run_supervisor_tool_calls_v134 (")
	createCalls = strings.ReplaceAll(createCalls, "'command_runtime')",
		"'command_runtime', 'web_search', 'web_fetch', 'web_citation')")
	if strings.Count(createCalls, "'web_citation')") < 3 {
		panic("web evidence Supervisor authority constraint is unavailable")
	}

	return []string{
		`CREATE TABLE web_evidence_sources (
			id TEXT PRIMARY KEY,
			protocol_version TEXT NOT NULL,
			run_id TEXT NOT NULL,
			mission_id TEXT NOT NULL,
			workspace_id TEXT NOT NULL,
			canonical_url TEXT NOT NULL,
			fingerprint TEXT NOT NULL,
			source_json TEXT NOT NULL,
			created_at TEXT NOT NULL,
			FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
			FOREIGN KEY(mission_id) REFERENCES missions(id) ON DELETE RESTRICT,
			UNIQUE(run_id, canonical_url),
			CHECK(protocol_version = 'web_evidence.v1'),
			CHECK(length(fingerprint) = 64),
			CHECK(json_valid(source_json) = 1)
		);`,
		`CREATE INDEX idx_web_evidence_sources_run
			ON web_evidence_sources(run_id, created_at DESC, id DESC);`,
		`CREATE TRIGGER trg_web_evidence_source_scope
			BEFORE INSERT ON web_evidence_sources
			WHEN NOT EXISTS (SELECT 1 FROM runs run
				JOIN missions mission ON mission.id = run.mission_id
				WHERE run.id = NEW.run_id AND run.mission_id = NEW.mission_id
					AND mission.workspace_id = NEW.workspace_id)
			BEGIN SELECT RAISE(ABORT, 'web source Run scope mismatch'); END;`,
		`CREATE TRIGGER trg_web_evidence_source_immutable
			BEFORE UPDATE ON web_evidence_sources
			BEGIN SELECT RAISE(ABORT, 'web evidence source is immutable'); END;`,
		`CREATE TRIGGER trg_web_evidence_source_delete_immutable
			BEFORE DELETE ON web_evidence_sources
			BEGIN SELECT RAISE(ABORT, 'web evidence source cannot be deleted'); END;`,

		`CREATE TABLE web_evidence_snapshots (
			id TEXT PRIMARY KEY,
			protocol_version TEXT NOT NULL,
			source_id TEXT NOT NULL,
			run_id TEXT NOT NULL,
			mission_id TEXT NOT NULL,
			fingerprint TEXT NOT NULL,
			digest TEXT NOT NULL,
			state TEXT NOT NULL,
			final_url TEXT NOT NULL,
			fetched_at TEXT NOT NULL,
			stale_at TEXT NOT NULL,
			snapshot_json TEXT NOT NULL,
			FOREIGN KEY(source_id) REFERENCES web_evidence_sources(id) ON DELETE RESTRICT,
			FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
			FOREIGN KEY(mission_id) REFERENCES missions(id) ON DELETE RESTRICT,
			CHECK(protocol_version = 'web_evidence.v1'),
			CHECK(length(fingerprint) = 64 AND length(digest) = 64),
			CHECK(state IN ('fetched', 'partial', 'blocked', 'failed')),
			CHECK(json_valid(snapshot_json) = 1)
		);`,
		`CREATE INDEX idx_web_evidence_snapshots_run
			ON web_evidence_snapshots(run_id, fetched_at DESC, id DESC);`,
		`CREATE INDEX idx_web_evidence_snapshots_source
			ON web_evidence_snapshots(source_id, fetched_at DESC, id DESC);`,
		`CREATE TRIGGER trg_web_evidence_snapshot_scope
			BEFORE INSERT ON web_evidence_snapshots
			WHEN NOT EXISTS (SELECT 1 FROM web_evidence_sources source
				WHERE source.id = NEW.source_id AND source.run_id = NEW.run_id
					AND source.mission_id = NEW.mission_id)
			BEGIN SELECT RAISE(ABORT, 'web snapshot source scope mismatch'); END;`,
		`CREATE TRIGGER trg_web_evidence_snapshot_immutable
			BEFORE UPDATE ON web_evidence_snapshots
			BEGIN SELECT RAISE(ABORT, 'web evidence snapshot is immutable'); END;`,
		`CREATE TRIGGER trg_web_evidence_snapshot_delete_immutable
			BEFORE DELETE ON web_evidence_snapshots
			BEGIN SELECT RAISE(ABORT, 'web evidence snapshot cannot be deleted'); END;`,

		`CREATE TABLE web_evidence_citations (
			id TEXT PRIMARY KEY,
			protocol_version TEXT NOT NULL,
			run_id TEXT NOT NULL,
			source_id TEXT NOT NULL,
			snapshot_id TEXT NOT NULL,
			fingerprint TEXT NOT NULL,
			citation_json TEXT NOT NULL,
			created_at TEXT NOT NULL,
			FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
			FOREIGN KEY(source_id) REFERENCES web_evidence_sources(id) ON DELETE RESTRICT,
			FOREIGN KEY(snapshot_id) REFERENCES web_evidence_snapshots(id) ON DELETE RESTRICT,
			CHECK(protocol_version = 'web_citation.v1'),
			CHECK(length(fingerprint) = 64),
			CHECK(json_valid(citation_json) = 1)
		);`,
		`CREATE INDEX idx_web_evidence_citations_run
			ON web_evidence_citations(run_id, created_at DESC, id DESC);`,
		`CREATE TRIGGER trg_web_evidence_citation_scope
			BEFORE INSERT ON web_evidence_citations
			WHEN NOT EXISTS (SELECT 1 FROM web_evidence_snapshots snapshot
				WHERE snapshot.id = NEW.snapshot_id AND snapshot.source_id = NEW.source_id
					AND snapshot.run_id = NEW.run_id
					AND snapshot.state IN ('fetched', 'partial'))
			BEGIN SELECT RAISE(ABORT, 'web citation requires a fetched same-Run snapshot'); END;`,
		`CREATE TRIGGER trg_web_evidence_citation_immutable
			BEFORE UPDATE ON web_evidence_citations
			BEGIN SELECT RAISE(ABORT, 'web evidence citation is immutable'); END;`,
		`CREATE TRIGGER trg_web_evidence_citation_delete_immutable
			BEFORE DELETE ON web_evidence_citations
			BEGIN SELECT RAISE(ABORT, 'web evidence citation cannot be deleted'); END;`,

		`CREATE TABLE web_evidence_operations (
			key_digest TEXT PRIMARY KEY,
			protocol_version TEXT NOT NULL,
			request_fingerprint TEXT NOT NULL,
			run_id TEXT NOT NULL,
			tool_name TEXT NOT NULL,
			response_json TEXT NOT NULL,
			created_at TEXT NOT NULL,
			FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
			CHECK(protocol_version = 'web_evidence_operation.v1'),
			CHECK(length(key_digest) = 64 AND length(request_fingerprint) = 64),
			CHECK(tool_name IN ('web_search', 'web_fetch', 'web_citation')),
			CHECK(json_valid(response_json) = 1)
		);`,
		`CREATE INDEX idx_web_evidence_operations_run
			ON web_evidence_operations(run_id, created_at DESC, key_digest);`,
		`CREATE TRIGGER trg_web_evidence_operation_immutable
			BEFORE UPDATE ON web_evidence_operations
			BEGIN SELECT RAISE(ABORT, 'web evidence operation is immutable'); END;`,
		`CREATE TRIGGER trg_web_evidence_operation_delete_immutable
			BEFORE DELETE ON web_evidence_operations
			BEGIN SELECT RAISE(ABORT, 'web evidence operation cannot be deleted'); END;`,

		`DROP TRIGGER trg_supervisor_tool_call_model_attempt;`,
		`DROP TRIGGER trg_supervisor_tool_round_completion;`,
		`DROP TRIGGER trg_supervisor_tool_stream_identity_immutable;`,
		`DROP TRIGGER trg_supervisor_tool_stream_identity_insert;`,
		`DROP INDEX idx_run_supervisor_tool_calls_pending;`,
		`DROP INDEX idx_supervisor_tool_stream_call_identity;`,
		`DROP INDEX idx_supervisor_tool_stream_item_identity;`,
		`ALTER TABLE run_supervisor_tool_calls RENAME TO run_supervisor_tool_calls_v133;`,
		createCalls,
		`INSERT INTO run_supervisor_tool_calls_v134 SELECT *
			FROM run_supervisor_tool_calls_v133;`,
		`DROP TABLE run_supervisor_tool_calls_v133;`,
		`ALTER TABLE run_supervisor_tool_calls_v134 RENAME TO run_supervisor_tool_calls;`,
		requireMigrationStatement("CREATE INDEX idx_run_supervisor_tool_calls_pending",
			githubReviewStatements),
		requireMigrationStatement("CREATE UNIQUE INDEX idx_supervisor_tool_stream_item_identity",
			itemStreamToolIdentityStatements),
		requireMigrationStatement("CREATE UNIQUE INDEX idx_supervisor_tool_stream_call_identity",
			itemStreamToolIdentityStatements),
		requireMigrationTrigger("trg_supervisor_tool_call_model_attempt",
			githubReviewStatements),
		requireMigrationTrigger("trg_supervisor_tool_round_completion",
			githubReviewStatements),
		requireMigrationTrigger("trg_supervisor_tool_stream_identity_insert",
			itemStreamToolIdentityStatements),
		requireMigrationTrigger("trg_supervisor_tool_stream_identity_immutable",
			itemStreamToolIdentityStatements),
	}
}()
