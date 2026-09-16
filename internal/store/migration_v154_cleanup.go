package store

// Directory removal has no after-content snapshot. A narrow reservation
// serializes cleanup with Thread publication and execution admission.
var threadDrydockCleanupStatements = []string{
	`CREATE TABLE drydock_cleanup_operations (
 operation_key_sha256 TEXT PRIMARY KEY CHECK(length(operation_key_sha256)=64),
 request_fingerprint TEXT NOT NULL CHECK(length(request_fingerprint)=64),
 drydock_id TEXT NOT NULL REFERENCES drydock_workspaces(id) ON DELETE RESTRICT,
 run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE RESTRICT,
 expected_generation INTEGER NOT NULL CHECK(expected_generation>0),
 status TEXT NOT NULL CHECK(status IN ('prepared','completed')),
 receipt_id TEXT REFERENCES drydock_lifecycle_receipts(id) ON DELETE RESTRICT,
 created_at TEXT NOT NULL CHECK(julianday(created_at) IS NOT NULL),
 completed_at TEXT,
 CHECK((status='prepared' AND receipt_id IS NULL AND completed_at IS NULL) OR
 (status='completed' AND receipt_id IS NOT NULL AND julianday(completed_at) IS NOT NULL))
);`,
	`CREATE UNIQUE INDEX idx_drydock_cleanup_pending ON drydock_cleanup_operations(drydock_id) WHERE status='prepared';`,
	`CREATE TRIGGER trg_drydock_cleanup_update BEFORE UPDATE ON drydock_cleanup_operations
 WHEN OLD.status!='prepared' OR NEW.status!='completed' OR
 NEW.operation_key_sha256!=OLD.operation_key_sha256 OR NEW.request_fingerprint!=OLD.request_fingerprint OR
 NEW.drydock_id!=OLD.drydock_id OR NEW.run_id!=OLD.run_id OR
 NEW.expected_generation!=OLD.expected_generation OR NEW.created_at!=OLD.created_at OR
 NOT EXISTS(SELECT 1 FROM drydock_lifecycle_receipts r WHERE r.id=NEW.receipt_id AND r.operation='cleanup'
 AND r.operation_key_sha256=NEW.operation_key_sha256 AND r.request_fingerprint=NEW.request_fingerprint
 AND r.drydock_id=NEW.drydock_id AND r.run_id=NEW.run_id AND r.generation_before=NEW.expected_generation)
 BEGIN SELECT RAISE(ABORT,'Drydock cleanup requires its exact final receipt'); END;`,
	`CREATE TRIGGER trg_drydock_cleanup_delete BEFORE DELETE ON drydock_cleanup_operations
 BEGIN SELECT RAISE(ABORT,'Drydock cleanup history is immutable'); END;`,
}
