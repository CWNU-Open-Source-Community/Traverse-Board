package store

var pluginRuntimeStatements = []string{
	`CREATE TABLE plugin_objects (
		package_fingerprint TEXT PRIMARY KEY,
		archive_sha256 TEXT NOT NULL UNIQUE,
		archive_bytes INTEGER NOT NULL,
		archive BLOB NOT NULL,
		created_at TEXT NOT NULL,
		CHECK(length(package_fingerprint) = 64
			AND package_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(archive_sha256) = 64 AND archive_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(archive_bytes BETWEEN 1 AND 4194304 AND length(archive) = archive_bytes),
		CHECK(julianday(created_at) IS NOT NULL)
	) WITHOUT ROWID;`,
	`CREATE TRIGGER trg_plugin_object_quota BEFORE INSERT ON plugin_objects
		WHEN (SELECT COALESCE(SUM(archive_bytes), 0) FROM plugin_objects)
			+ NEW.archive_bytes > 268435456
		BEGIN SELECT RAISE(ABORT, 'plugin object store quota exceeded'); END;`,
	`CREATE TRIGGER trg_plugin_objects_update_immutable BEFORE UPDATE ON plugin_objects
		BEGIN SELECT RAISE(ABORT, 'plugin objects are immutable'); END;`,
	`CREATE TRIGGER trg_plugin_objects_delete_immutable BEFORE DELETE ON plugin_objects
		BEGIN SELECT RAISE(ABORT, 'plugin objects are retained for rollback'); END;`,
	`CREATE TABLE plugin_installations (
		id TEXT PRIMARY KEY,
		protocol_version TEXT NOT NULL,
		plugin_id TEXT NOT NULL,
		plugin_version TEXT NOT NULL,
		publisher TEXT NOT NULL,
		manifest_json TEXT NOT NULL,
		source_json TEXT NOT NULL,
		archive_sha256 TEXT NOT NULL,
		package_fingerprint TEXT NOT NULL UNIQUE,
		archive_bytes INTEGER NOT NULL,
		signature_present INTEGER NOT NULL,
		signature_valid INTEGER NOT NULL,
		publisher_fingerprint TEXT NOT NULL DEFAULT '',
		publisher_public_key TEXT NOT NULL DEFAULT '',
		state TEXT NOT NULL,
		enabled_capabilities_json TEXT NOT NULL,
		generation INTEGER NOT NULL,
		supersedes_installation_id TEXT NOT NULL DEFAULT '',
		staged_by TEXT NOT NULL,
		reviewed_by TEXT NOT NULL DEFAULT '',
		reviewed_at TEXT,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		FOREIGN KEY(package_fingerprint) REFERENCES plugin_objects(package_fingerprint)
			ON DELETE RESTRICT,
		CHECK(protocol_version = 'plugin-installation.v1'),
		CHECK(json_valid(manifest_json) AND length(CAST(manifest_json AS BLOB))
			BETWEEN 2 AND 262144),
		CHECK(json_valid(source_json) AND length(CAST(source_json AS BLOB))
			BETWEEN 2 AND 16384),
		CHECK(json_valid(enabled_capabilities_json)
			AND json_type(enabled_capabilities_json) = 'array'),
		CHECK(length(archive_sha256) = 64 AND archive_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(package_fingerprint) = 64
			AND package_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(archive_bytes BETWEEN 1 AND 4194304),
		CHECK(signature_present IN (0, 1) AND signature_valid IN (0, 1)
			AND signature_valid <= signature_present),
		CHECK(publisher_fingerprint = '' OR (length(publisher_fingerprint) = 64
			AND publisher_fingerprint NOT GLOB '*[^0-9a-f]*')),
		CHECK(state IN ('staged', 'approved', 'enabled', 'disabled', 'rolled_back',
			'revoked', 'quarantined')),
		CHECK(generation > 0),
		CHECK(length(id) BETWEEN 1 AND 256 AND length(plugin_id) BETWEEN 1 AND 256),
		CHECK(length(plugin_version) BETWEEN 5 AND 64 AND length(publisher) BETWEEN 1 AND 256),
		CHECK(length(supersedes_installation_id) <= 256
			AND length(staged_by) BETWEEN 1 AND 256 AND length(reviewed_by) <= 256),
		CHECK(julianday(created_at) IS NOT NULL AND julianday(updated_at) IS NOT NULL
			AND julianday(updated_at) >= julianday(created_at)),
		CHECK(reviewed_at IS NULL OR julianday(reviewed_at) IS NOT NULL)
	);`,
	`CREATE INDEX idx_plugin_installations_plugin_created
		ON plugin_installations(plugin_id, created_at DESC, id DESC);`,
	`CREATE INDEX idx_plugin_installations_state_updated
		ON plugin_installations(state, updated_at DESC, id DESC);`,
	`CREATE UNIQUE INDEX idx_plugin_installations_one_enabled_version
		ON plugin_installations(plugin_id) WHERE state = 'enabled';`,
	`CREATE TRIGGER trg_plugin_installation_immutable_identity
		BEFORE UPDATE ON plugin_installations WHEN
			NEW.id != OLD.id OR NEW.plugin_id != OLD.plugin_id
			OR NEW.plugin_version != OLD.plugin_version OR NEW.publisher != OLD.publisher
			OR NEW.manifest_json != OLD.manifest_json OR NEW.source_json != OLD.source_json
			OR NEW.archive_sha256 != OLD.archive_sha256
			OR NEW.package_fingerprint != OLD.package_fingerprint
			OR NEW.publisher_fingerprint != OLD.publisher_fingerprint
			OR NEW.publisher_public_key != OLD.publisher_public_key
			OR NEW.supersedes_installation_id != OLD.supersedes_installation_id
			OR NEW.staged_by != OLD.staged_by OR NEW.archive_bytes != OLD.archive_bytes
			OR NEW.signature_present != OLD.signature_present
			OR NEW.signature_valid != OLD.signature_valid
			OR NEW.protocol_version != OLD.protocol_version
		BEGIN SELECT RAISE(ABORT, 'plugin installation identity is immutable'); END;`,
	`CREATE TRIGGER trg_plugin_installation_generation
		BEFORE UPDATE ON plugin_installations
		WHEN NEW.generation != OLD.generation + 1
		BEGIN SELECT RAISE(ABORT, 'plugin generation must advance exactly once'); END;`,
	`CREATE TRIGGER trg_plugin_installations_delete_immutable
		BEFORE DELETE ON plugin_installations
		BEGIN SELECT RAISE(ABORT, 'plugin installations are retained for audit'); END;`,
	`CREATE TABLE plugin_installation_transitions (
		id TEXT PRIMARY KEY,
		installation_id TEXT NOT NULL,
		from_state TEXT NOT NULL,
		to_state TEXT NOT NULL,
		from_generation INTEGER NOT NULL,
		to_generation INTEGER NOT NULL,
		enabled_capabilities_json TEXT NOT NULL,
		actor TEXT NOT NULL,
		created_at TEXT NOT NULL,
		FOREIGN KEY(installation_id) REFERENCES plugin_installations(id) ON DELETE RESTRICT,
		CHECK(json_valid(enabled_capabilities_json)
			AND json_type(enabled_capabilities_json) = 'array'),
		CHECK(from_generation > 0 AND to_generation = from_generation + 1),
		CHECK(length(actor) BETWEEN 1 AND 256),
		CHECK(julianday(created_at) IS NOT NULL)
	);`,
	`CREATE INDEX idx_plugin_transitions_installation
		ON plugin_installation_transitions(installation_id, to_generation);`,
	`CREATE TRIGGER trg_plugin_transitions_update_immutable
		BEFORE UPDATE ON plugin_installation_transitions
		BEGIN SELECT RAISE(ABORT, 'plugin transition audit is immutable'); END;`,
	`CREATE TRIGGER trg_plugin_transitions_delete_immutable
		BEFORE DELETE ON plugin_installation_transitions
		BEGIN SELECT RAISE(ABORT, 'plugin transition audit is immutable'); END;`,
	`CREATE TABLE plugin_publishers (
		fingerprint TEXT PRIMARY KEY,
		protocol_version TEXT NOT NULL,
		publisher TEXT NOT NULL,
		public_key TEXT NOT NULL,
		state TEXT NOT NULL,
		generation INTEGER NOT NULL,
		reviewed_by TEXT NOT NULL,
		reviewed_at TEXT NOT NULL,
		CHECK(protocol_version = 'plugin-publisher-trust.v1'),
		CHECK(length(fingerprint) = 64 AND fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(state IN ('trusted', 'revoked') AND generation > 0),
		CHECK(length(publisher) BETWEEN 1 AND 256 AND length(public_key) BETWEEN 1 AND 256),
		CHECK(length(reviewed_by) BETWEEN 1 AND 256 AND julianday(reviewed_at) IS NOT NULL)
	) WITHOUT ROWID;`,
	`CREATE TRIGGER trg_plugin_publisher_identity_immutable
		BEFORE UPDATE ON plugin_publishers WHEN NEW.fingerprint != OLD.fingerprint
			OR NEW.publisher != OLD.publisher OR NEW.public_key != OLD.public_key
			OR NEW.generation != OLD.generation + 1
		BEGIN SELECT RAISE(ABORT, 'plugin publisher identity or generation is invalid'); END;`,
	`CREATE TABLE plugin_publisher_reviews (
		id TEXT PRIMARY KEY,
		fingerprint TEXT NOT NULL,
		state TEXT NOT NULL,
		generation INTEGER NOT NULL,
		reviewed_by TEXT NOT NULL,
		reviewed_at TEXT NOT NULL,
		FOREIGN KEY(fingerprint) REFERENCES plugin_publishers(fingerprint) ON DELETE RESTRICT,
		CHECK(state IN ('trusted', 'revoked') AND generation > 0),
		CHECK(length(reviewed_by) BETWEEN 1 AND 256 AND julianday(reviewed_at) IS NOT NULL),
		UNIQUE(fingerprint, generation)
	);`,
	`CREATE TRIGGER trg_plugin_publisher_reviews_update_immutable
		BEFORE UPDATE ON plugin_publisher_reviews
		BEGIN SELECT RAISE(ABORT, 'plugin publisher reviews are immutable'); END;`,
	`CREATE TRIGGER trg_plugin_publisher_reviews_delete_immutable
		BEFORE DELETE ON plugin_publisher_reviews
		BEGIN SELECT RAISE(ABORT, 'plugin publisher reviews are immutable'); END;`,
	`CREATE TABLE plugin_hook_audits (
		id TEXT PRIMARY KEY,
		plugin_id TEXT NOT NULL,
		hook_id TEXT NOT NULL,
		event TEXT NOT NULL,
		run_id TEXT NOT NULL DEFAULT '',
		workspace_id TEXT NOT NULL DEFAULT '',
		tool_name TEXT NOT NULL DEFAULT '',
		outcome TEXT NOT NULL,
		created_at TEXT NOT NULL,
		CHECK(event IN ('pre_tool', 'post_tool', 'run_started', 'run_completed',
			'session_opened', 'session_closed', 'compaction', 'subagent', 'checkpoint')),
		CHECK(outcome IN ('completed', 'failed_closed', 'failed_continue')),
		CHECK(length(plugin_id) BETWEEN 1 AND 256 AND length(hook_id) BETWEEN 1 AND 256),
		CHECK(length(run_id) <= 256 AND length(workspace_id) <= 256
			AND length(tool_name) <= 256),
		CHECK(julianday(created_at) IS NOT NULL)
	);`,
	`CREATE INDEX idx_plugin_hook_audits_run_created
		ON plugin_hook_audits(run_id, created_at DESC, id DESC);`,
	`CREATE TRIGGER trg_plugin_hook_audits_update_immutable
		BEFORE UPDATE ON plugin_hook_audits
		BEGIN SELECT RAISE(ABORT, 'plugin hook audit is immutable'); END;`,
	`CREATE TRIGGER trg_plugin_hook_audits_delete_immutable
		BEFORE DELETE ON plugin_hook_audits
		BEGIN SELECT RAISE(ABORT, 'plugin hook audit is immutable'); END;`,
}
