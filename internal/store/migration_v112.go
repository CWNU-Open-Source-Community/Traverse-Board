package store

var skillCandidateReviewStatements = []string{
	`CREATE TABLE skill_candidates (
		id TEXT PRIMARY KEY,
		protocol_version TEXT NOT NULL,
		operation_key_digest TEXT NOT NULL UNIQUE,
		request_fingerprint TEXT NOT NULL,
		invocation_id TEXT NOT NULL UNIQUE,
		run_id TEXT NOT NULL,
		root_agent_id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		workspace_id TEXT NOT NULL,
		surface TEXT NOT NULL,
		manifest_json TEXT NOT NULL,
		content TEXT NOT NULL,
		archive_sha256 TEXT NOT NULL,
		package_fingerprint TEXT NOT NULL,
		archive_bytes INTEGER NOT NULL,
		candidate_fingerprint TEXT NOT NULL UNIQUE,
		requested_by TEXT NOT NULL,
		created_at TEXT NOT NULL,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(run_id, root_agent_id) REFERENCES agent_nodes(run_id, id) ON DELETE RESTRICT,
		FOREIGN KEY(invocation_id) REFERENCES run_tool_calls(id) ON DELETE RESTRICT,
		CHECK(protocol_version = 'skill_candidate.v1'),
		CHECK(surface = 'code'),
		CHECK(json_valid(manifest_json) AND json_type(manifest_json) = 'object'
			AND length(CAST(manifest_json AS BLOB)) BETWEEN 1 AND 16384),
		CHECK(length(CAST(content AS BLOB)) BETWEEN 1 AND 4096),
		CHECK(length(operation_key_digest) = 64 AND operation_key_digest NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(request_fingerprint) = 64 AND request_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(archive_sha256) = 64 AND archive_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(package_fingerprint) = 64 AND package_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(archive_bytes BETWEEN 1 AND 65536),
		CHECK(length(candidate_fingerprint) = 64
			AND candidate_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(id = trim(id) AND length(id) BETWEEN 1 AND 256 AND instr(id, char(0)) = 0),
		CHECK(session_id = trim(session_id) AND length(session_id) BETWEEN 1 AND 256),
		CHECK(workspace_id = trim(workspace_id) AND length(workspace_id) BETWEEN 1 AND 256),
		CHECK(requested_by = 'run_supervisor'),
		CHECK(invocation_id = trim(invocation_id) AND length(invocation_id) BETWEEN 1 AND 256
			AND instr(invocation_id, char(0)) = 0)
	);`,
	`CREATE TRIGGER skill_candidate_insert_guard
		BEFORE INSERT ON skill_candidates
		BEGIN
			SELECT RAISE(ABORT, 'Skill candidate Run scope is invalid')
			WHERE NOT EXISTS (
			SELECT 1 FROM runs run JOIN missions mission ON mission.id = run.mission_id
				JOIN agent_nodes root ON root.run_id = run.id AND root.id = NEW.root_agent_id
				JOIN run_tool_calls invocation ON invocation.id = NEW.invocation_id
				WHERE run.id = NEW.run_id AND run.session_id = NEW.session_id
					AND mission.workspace_id = NEW.workspace_id AND run.status = 'running'
					AND root.role = 'root' AND root.parent_id IS NULL AND root.depth = 0
					AND invocation.run_id = NEW.run_id
					AND invocation.session_id = NEW.session_id
					AND invocation.workspace_id = NEW.workspace_id
					AND invocation.tool_name = 'skill_candidate_propose'
					AND invocation.action_class = 'agent_proposal'
					AND julianday(NEW.created_at) >= julianday(invocation.created_at));
			SELECT RAISE(ABORT, 'Skill candidate manifest identity is invalid')
			WHERE json_extract(NEW.manifest_json, '$.protocol') != 'skill.v1'
				OR json_extract(NEW.manifest_json, '$.name') IS NULL
				OR json_extract(NEW.manifest_json, '$.version') IS NULL
				OR json_extract(NEW.manifest_json, '$.publisher') IS NOT NULL
				OR json_extract(NEW.manifest_json, '$.surfaces') != json('["code"]');
			SELECT RAISE(ABORT, 'Skill candidate Registry capacity exceeded')
			WHERE (SELECT COUNT(*) FROM skill_candidates) >= 64;
			SELECT RAISE(ABORT, 'Skill candidate Run capacity exceeded')
			WHERE (SELECT COUNT(*) FROM skill_candidates WHERE run_id = NEW.run_id) >= 4;
		END;`,
	`CREATE INDEX idx_skill_candidates_run_created
		ON skill_candidates(run_id, created_at, id);`,
	`CREATE TABLE skill_candidate_reviews (
		id TEXT PRIMARY KEY,
		protocol_version TEXT NOT NULL,
		operation_key_digest TEXT NOT NULL UNIQUE,
		request_fingerprint TEXT NOT NULL,
		candidate_id TEXT NOT NULL UNIQUE,
		candidate_fingerprint TEXT NOT NULL,
		decision TEXT NOT NULL,
		reason TEXT NOT NULL,
		reviewer TEXT NOT NULL,
		review_fingerprint TEXT NOT NULL UNIQUE,
		created_at TEXT NOT NULL,
		FOREIGN KEY(candidate_id) REFERENCES skill_candidates(id) ON DELETE RESTRICT,
		CHECK(protocol_version = 'skill_candidate_review.v1'),
		CHECK(decision IN ('approve', 'reject')),
		CHECK((decision = 'approve') OR (decision = 'reject' AND length(trim(reason)) > 0)),
		CHECK(reason = trim(reason) AND length(reason) <= 2048
			AND length(CAST(reason AS BLOB)) <= 8192 AND instr(reason, char(0)) = 0),
		CHECK(length(operation_key_digest) = 64 AND operation_key_digest NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(request_fingerprint) = 64 AND request_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(candidate_fingerprint) = 64
			AND candidate_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(review_fingerprint) = 64 AND review_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(reviewer = trim(reviewer) AND length(reviewer) BETWEEN 1 AND 256
			AND length(CAST(reviewer AS BLOB)) <= 256 AND instr(reviewer, char(0)) = 0
			AND lower(reviewer) NOT IN ('agent', 'llm', 'model', 'repository', 'repo',
				'skill', 'supervisor', 'run_supervisor')),
		CHECK(id = trim(id) AND length(id) BETWEEN 1 AND 256)
	);`,
	`CREATE TRIGGER skill_candidate_review_insert_guard
		BEFORE INSERT ON skill_candidate_reviews
		BEGIN
			SELECT RAISE(ABORT, 'Skill candidate review binding is invalid')
			WHERE NOT EXISTS (
				SELECT 1 FROM skill_candidates candidate
				WHERE candidate.id = NEW.candidate_id
					AND candidate.candidate_fingerprint = NEW.candidate_fingerprint
					AND candidate.created_at <= NEW.created_at);
		END;`,
	`CREATE TABLE skill_candidate_imports (
		id TEXT PRIMARY KEY,
		protocol_version TEXT NOT NULL,
		operation_key_digest TEXT NOT NULL UNIQUE,
		request_fingerprint TEXT NOT NULL,
		candidate_id TEXT NOT NULL UNIQUE,
		candidate_fingerprint TEXT NOT NULL,
		review_fingerprint TEXT NOT NULL UNIQUE,
		installation_id TEXT NOT NULL UNIQUE,
		installation_fingerprint TEXT NOT NULL UNIQUE,
		imported_by TEXT NOT NULL,
		import_fingerprint TEXT NOT NULL UNIQUE,
		created_at TEXT NOT NULL,
		FOREIGN KEY(candidate_id) REFERENCES skill_candidates(id) ON DELETE RESTRICT,
		FOREIGN KEY(installation_id) REFERENCES skill_package_installations(id) ON DELETE RESTRICT,
		CHECK(protocol_version = 'skill_candidate_import.v1'),
		CHECK(length(operation_key_digest) = 64 AND operation_key_digest NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(request_fingerprint) = 64 AND request_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(candidate_fingerprint) = 64
			AND candidate_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(review_fingerprint) = 64 AND review_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(installation_fingerprint) = 64
			AND installation_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(import_fingerprint) = 64 AND import_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(imported_by = trim(imported_by) AND length(imported_by) BETWEEN 1 AND 256
			AND length(CAST(imported_by AS BLOB)) <= 256 AND instr(imported_by, char(0)) = 0
			AND lower(imported_by) NOT IN ('agent', 'llm', 'model', 'repository', 'repo',
				'skill', 'supervisor', 'run_supervisor')),
		CHECK(id = trim(id) AND length(id) BETWEEN 1 AND 256)
	);`,
	`CREATE TRIGGER skill_candidate_import_insert_guard
		BEFORE INSERT ON skill_candidate_imports
		BEGIN
			SELECT RAISE(ABORT, 'Skill candidate import review binding is invalid')
			WHERE NOT EXISTS (
				SELECT 1 FROM skill_candidate_reviews review
				WHERE review.candidate_id = NEW.candidate_id
					AND review.candidate_fingerprint = NEW.candidate_fingerprint
					AND review.review_fingerprint = NEW.review_fingerprint
					AND review.decision = 'approve' AND review.created_at <= NEW.created_at);
			SELECT RAISE(ABORT, 'Skill candidate import installation binding is invalid')
			WHERE NOT EXISTS (
				SELECT 1 FROM skill_candidates candidate
				JOIN skill_package_installations installation
					ON installation.id = NEW.installation_id
				JOIN skill_package_install_results result
					ON result.installation_id = installation.id
				WHERE candidate.id = NEW.candidate_id
					AND candidate.candidate_fingerprint = NEW.candidate_fingerprint
					AND candidate.package_fingerprint = installation.package_fingerprint
					AND candidate.archive_sha256 = installation.archive_sha256
					AND installation.installation_fingerprint = NEW.installation_fingerprint
					AND installation.surface = 'code' AND installation.operator_confirmed = 1
					AND result.completed_at <= NEW.created_at);
		END;`,
	`CREATE TRIGGER skill_candidates_no_update BEFORE UPDATE ON skill_candidates
		BEGIN SELECT RAISE(ABORT, 'Skill candidates are immutable'); END;`,
	`CREATE TRIGGER skill_candidates_no_delete BEFORE DELETE ON skill_candidates
		BEGIN SELECT RAISE(ABORT, 'Skill candidates are immutable'); END;`,
	`CREATE TRIGGER skill_candidate_reviews_no_update BEFORE UPDATE ON skill_candidate_reviews
		BEGIN SELECT RAISE(ABORT, 'Skill candidate reviews are immutable'); END;`,
	`CREATE TRIGGER skill_candidate_reviews_no_delete BEFORE DELETE ON skill_candidate_reviews
		BEGIN SELECT RAISE(ABORT, 'Skill candidate reviews are immutable'); END;`,
	`CREATE TRIGGER skill_candidate_imports_no_update BEFORE UPDATE ON skill_candidate_imports
		BEGIN SELECT RAISE(ABORT, 'Skill candidate imports are immutable'); END;`,
	`CREATE TRIGGER skill_candidate_imports_no_delete BEFORE DELETE ON skill_candidate_imports
		BEGIN SELECT RAISE(ABORT, 'Skill candidate imports are immutable'); END;`,
}
