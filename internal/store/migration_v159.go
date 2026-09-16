package store

// Existing operation receipts remain untouched. A separately claimed start
// distinguishes reviewed intent from a possibly executed, unknown operation.
var threadGitStartedStatements = []string{
	`ALTER TABLE git_remote_operations ADD COLUMN started_at TEXT CHECK(started_at IS NULL OR julianday(started_at) IS NOT NULL);`,
	`ALTER TABLE git_mutation_operations ADD COLUMN started_at TEXT CHECK(started_at IS NULL OR julianday(started_at) IS NOT NULL);`,
	`CREATE TRIGGER trg_git_remote_started_once BEFORE UPDATE OF started_at ON git_remote_operations WHEN OLD.started_at IS NOT NULL AND (NEW.started_at IS NULL OR NEW.started_at <> OLD.started_at) BEGIN SELECT RAISE(ABORT, 'Remote Git execution start is immutable'); END;`,
	`CREATE TRIGGER trg_git_mutation_started_once BEFORE UPDATE OF started_at ON git_mutation_operations WHEN OLD.started_at IS NOT NULL AND (NEW.started_at IS NULL OR NEW.started_at <> OLD.started_at) BEGIN SELECT RAISE(ABORT, 'Git execution start is immutable'); END;`,
}
