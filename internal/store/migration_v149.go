package store

// webFetchInlineAuthorizationStatements adds a source-bound approval ledger for
// one denied public-HTTPS fetch. The canonical URL is retained only for the
// operator projection; execution authority is the exact normalized host.
var webFetchInlineAuthorizationStatements = []string{
	`CREATE TABLE web_fetch_authorizations (
		id TEXT PRIMARY KEY,
		approval_id TEXT NOT NULL UNIQUE,
		thread_id TEXT NOT NULL,
		run_id TEXT NOT NULL,
		mission_id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		workspace_id TEXT NOT NULL DEFAULT '',
		supervisor_turn INTEGER NOT NULL,
		supervisor_tool_call_id TEXT NOT NULL,
		canonical_url TEXT NOT NULL,
		exact_target TEXT NOT NULL,
		request_fingerprint TEXT NOT NULL,
		authorization_scope TEXT NOT NULL DEFAULT 'once',
		status TEXT NOT NULL,
		requested_by TEXT NOT NULL,
		reviewed_by TEXT NOT NULL DEFAULT '',
		version INTEGER NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		decided_at TEXT,
		FOREIGN KEY(approval_id) REFERENCES tool_approvals(id) ON DELETE RESTRICT,
		FOREIGN KEY(thread_id) REFERENCES threads(id) ON DELETE RESTRICT,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(mission_id) REFERENCES missions(id) ON DELETE RESTRICT,
		FOREIGN KEY(session_id) REFERENCES sessions(id) ON DELETE RESTRICT,
		UNIQUE(run_id, supervisor_tool_call_id),
		CHECK(supervisor_turn > 0),
		CHECK(length(request_fingerprint) = 64),
		CHECK(authorization_scope IN ('once','thread')),
		CHECK(status IN ('pending','approved','denied','consumed')),
		CHECK(julianday(created_at) IS NOT NULL),
		CHECK(julianday(updated_at) IS NOT NULL),
		CHECK(decided_at IS NULL OR julianday(decided_at) IS NOT NULL),
		CHECK((status = 'pending' AND reviewed_by = '' AND decided_at IS NULL) OR
			(status <> 'pending' AND reviewed_by <> '' AND decided_at IS NOT NULL)),
		CHECK(status <> 'consumed' OR authorization_scope = 'once')
	);`,
	`CREATE INDEX idx_web_fetch_authorizations_thread_target
		ON web_fetch_authorizations(thread_id, exact_target, status, authorization_scope);`,
	`CREATE TRIGGER trg_web_fetch_authorizations_identity_immutable
		BEFORE UPDATE ON web_fetch_authorizations
		WHEN NEW.id <> OLD.id OR NEW.approval_id <> OLD.approval_id OR
			NEW.thread_id <> OLD.thread_id OR NEW.run_id <> OLD.run_id OR
			NEW.mission_id <> OLD.mission_id OR NEW.session_id <> OLD.session_id OR
			NEW.workspace_id <> OLD.workspace_id OR
			NEW.supervisor_turn <> OLD.supervisor_turn OR
			NEW.supervisor_tool_call_id <> OLD.supervisor_tool_call_id OR
			NEW.canonical_url <> OLD.canonical_url OR NEW.exact_target <> OLD.exact_target OR
			NEW.request_fingerprint <> OLD.request_fingerprint OR
			NEW.requested_by <> OLD.requested_by OR NEW.created_at <> OLD.created_at
		BEGIN SELECT RAISE(ABORT, 'web fetch authorization identity is immutable'); END;`,
	`CREATE TRIGGER trg_web_fetch_authorizations_delete_immutable
		BEFORE DELETE ON web_fetch_authorizations
		BEGIN SELECT RAISE(ABORT, 'web fetch authorization cannot be deleted'); END;`,
}
