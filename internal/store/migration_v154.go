package store

// Physical Drydock identity stays unique and immutable. Only adjacent epochs
// of the same Thread may share it; historical bindings never imply authority.
var threadDrydockBindingStatements = []string{
	`CREATE TABLE thread_drydock_bindings (
  run_id TEXT PRIMARY KEY REFERENCES runs(id) ON DELETE RESTRICT,
  thread_id TEXT NOT NULL REFERENCES threads(id) ON DELETE RESTRICT,
  drydock_id TEXT NOT NULL REFERENCES drydock_workspaces(id) ON DELETE RESTRICT,
  predecessor_run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE RESTRICT,
  created_at TEXT NOT NULL CHECK(julianday(created_at) IS NOT NULL)
 );`,
	`CREATE INDEX idx_thread_drydock_bindings_physical ON thread_drydock_bindings(drydock_id,created_at,run_id);`,
	`CREATE VIEW run_file_drydock_bindings AS
  SELECT d.run_id,r.mission_id,r.session_id,d.source_workspace_id,d.workspace_id,d.id AS drydock_id,
   COALESCE(tr.thread_id,'') AS thread_id
  FROM drydock_workspaces d JOIN runs r ON r.id=d.run_id
  LEFT JOIN thread_runs tr ON tr.run_id=r.id
  UNION ALL
  SELECT b.run_id,r.mission_id,r.session_id,d.source_workspace_id,d.workspace_id,d.id AS drydock_id,b.thread_id
  FROM thread_drydock_bindings b JOIN runs r ON r.id=b.run_id JOIN drydock_workspaces d ON d.id=b.drydock_id;`,
	`CREATE TRIGGER trg_thread_drydock_binding_insert BEFORE INSERT ON thread_drydock_bindings
  WHEN NOT EXISTS (
   SELECT 1 FROM threads thread JOIN thread_runs next ON next.thread_id=thread.id AND next.run_id=NEW.run_id
   JOIN thread_runs previous ON previous.thread_id=thread.id AND previous.run_id=NEW.predecessor_run_id AND previous.ordinal+1=next.ordinal
   JOIN runs candidate ON candidate.id=next.run_id JOIN runs predecessor ON predecessor.id=previous.run_id
   JOIN sessions linked ON linked.id=candidate.session_id
   JOIN run_file_drydock_bindings source ON source.run_id=previous.run_id AND source.thread_id=thread.id
   JOIN drydock_workspaces d ON d.id=source.drydock_id
   WHERE thread.id=NEW.thread_id AND thread.status='active' AND thread.active_run_id=candidate.id AND thread.last_run_id=candidate.id
    AND next.predecessor_run_id=previous.run_id AND candidate.status='created' AND predecessor.status IN ('completed','failed','cancelled')
    AND candidate.mission_id=thread.mission_id AND predecessor.mission_id=thread.mission_id
    AND candidate.session_id=next.session_id AND linked.workspace_id=thread.workspace_id AND linked.status='active'
    AND source.drydock_id=NEW.drydock_id AND source.source_workspace_id=thread.workspace_id AND d.state IN ('ready','delivered')
    AND NOT EXISTS(SELECT 1 FROM drydock_workspaces owned WHERE owned.run_id=candidate.id)
    AND NOT EXISTS(SELECT 1 FROM run_execution_leases lease JOIN run_file_drydock_bindings owner ON owner.run_id=lease.run_id
      WHERE owner.drydock_id=d.id AND lease.status='active' AND julianday(lease.expires_at)>julianday(NEW.created_at))
    AND NOT EXISTS(SELECT 1 FROM drydock_cleanup_operations cleanup WHERE cleanup.drydock_id=d.id AND cleanup.status='prepared')
    AND NOT EXISTS(SELECT 1 FROM workspace_checkpoint_transactions pending JOIN run_file_drydock_bindings owner ON owner.run_id=pending.run_id
      WHERE owner.drydock_id=d.id AND pending.status IN ('prepared','applying'))
  ) BEGIN SELECT RAISE(ABORT,'Thread working directory binding is not an exact quiescent successor'); END;`,
	`CREATE TRIGGER trg_thread_drydock_binding_update BEFORE UPDATE ON thread_drydock_bindings BEGIN SELECT RAISE(ABORT,'Thread working directory history is immutable'); END;`,
	`CREATE TRIGGER trg_thread_drydock_binding_delete BEFORE DELETE ON thread_drydock_bindings BEGIN SELECT RAISE(ABORT,'Thread working directory history is immutable'); END;`,
}
