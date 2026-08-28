package store

// threadSessionLifecycleStatements repairs legacy Session-only archive drift and
// makes the Thread the canonical lifecycle owner. Historical terminal Run
// Sessions deliberately retain their current status while an active Thread is
// restored; only the current nonterminal Run Session is reactivated.
var threadSessionLifecycleStatements = []string{
	`UPDATE sessions SET status = 'archived', updated_at = CASE
			WHEN julianday(updated_at) >= julianday((SELECT MAX(thread_record.updated_at)
				FROM thread_runs binding JOIN threads thread_record
					ON thread_record.id = binding.thread_id
				WHERE binding.session_id = sessions.id
					AND thread_record.status IN ('archived', 'deleted')))
			THEN updated_at
			ELSE (SELECT MAX(thread_record.updated_at)
				FROM thread_runs binding JOIN threads thread_record
					ON thread_record.id = binding.thread_id
				WHERE binding.session_id = sessions.id
					AND thread_record.status IN ('archived', 'deleted')) END
		WHERE status <> 'archived' AND EXISTS (
			SELECT 1 FROM thread_runs binding JOIN threads thread_record
				ON thread_record.id = binding.thread_id
			WHERE binding.session_id = sessions.id
				AND thread_record.status IN ('archived', 'deleted')
		);`,
	`UPDATE sessions SET status = 'active', updated_at = CASE
			WHEN julianday(updated_at) >= julianday((SELECT thread_record.updated_at
				FROM thread_runs binding JOIN threads thread_record
					ON thread_record.id = binding.thread_id
				WHERE binding.session_id = sessions.id
					AND binding.run_id = thread_record.active_run_id
					AND thread_record.status = 'active'))
			THEN updated_at
			ELSE (SELECT thread_record.updated_at
				FROM thread_runs binding JOIN threads thread_record
					ON thread_record.id = binding.thread_id
				WHERE binding.session_id = sessions.id
					AND binding.run_id = thread_record.active_run_id
					AND thread_record.status = 'active') END
		WHERE status <> 'active' AND EXISTS (
			SELECT 1 FROM thread_runs binding
			JOIN threads thread_record ON thread_record.id = binding.thread_id
			JOIN runs run ON run.id = binding.run_id
			WHERE binding.session_id = sessions.id
				AND binding.run_id = thread_record.active_run_id
				AND thread_record.status = 'active'
				AND run.status NOT IN ('completed', 'failed', 'cancelled')
		);`,
	`CREATE TRIGGER trg_thread_bound_session_status_guard
		BEFORE UPDATE OF status ON sessions
		WHEN
			(NEW.status = 'archived' AND EXISTS (
				SELECT 1 FROM thread_runs binding
				JOIN threads thread_record ON thread_record.id = binding.thread_id
				JOIN runs run ON run.id = binding.run_id
				WHERE binding.session_id = NEW.id
					AND binding.run_id = thread_record.active_run_id
					AND thread_record.status = 'active'
					AND run.status NOT IN ('completed', 'failed', 'cancelled')
			))
			OR
			(NEW.status = 'active' AND EXISTS (
				SELECT 1 FROM thread_runs binding
				JOIN threads thread_record ON thread_record.id = binding.thread_id
				WHERE binding.session_id = NEW.id
					AND thread_record.status IN ('archived', 'deleted')
			))
		BEGIN
			SELECT RAISE(ABORT,
				'Thread-bound Session status must be changed through Thread lifecycle');
		END;`,
	`CREATE TRIGGER trg_thread_status_projects_bound_sessions
		AFTER UPDATE OF status ON threads
		WHEN NEW.status <> OLD.status
		BEGIN
			UPDATE sessions SET status = 'archived', updated_at = NEW.updated_at
			WHERE NEW.status IN ('archived', 'deleted')
				AND id IN (SELECT session_id FROM thread_runs
					WHERE thread_id = NEW.id);
			UPDATE sessions SET status = 'active', updated_at = NEW.updated_at
			WHERE NEW.status = 'active' AND NEW.active_run_id IS NOT NULL
				AND id = (SELECT binding.session_id FROM thread_runs binding
					JOIN runs run ON run.id = binding.run_id
					WHERE binding.thread_id = NEW.id
						AND binding.run_id = NEW.active_run_id
						AND run.status NOT IN ('completed', 'failed', 'cancelled'));
		END;`,
	`CREATE TRIGGER trg_thread_run_insert_session_lifecycle
		BEFORE INSERT ON thread_runs
		WHEN NOT EXISTS (
			SELECT 1 FROM threads thread_record
			JOIN runs run ON run.id = NEW.run_id
			JOIN sessions session_record ON session_record.id = NEW.session_id
			WHERE thread_record.id = NEW.thread_id
				AND thread_record.status = 'active'
				AND (run.status IN ('completed', 'failed', 'cancelled')
					OR session_record.status = 'active')
		)
		BEGIN
			SELECT RAISE(ABORT, 'thread Run Session lifecycle is invalid');
		END;`,
}
