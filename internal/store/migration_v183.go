package store

import "strings"

// Existing v1 receipts keep their table, foreign keys, guards and fingerprints.
// Only new Plugin receipts use the v2 table; neither path installs a package.
func skillCandidatePluginReceiptStatements() []string {
	var table string
	for _, statement := range skillCandidateReviewStatements {
		if strings.HasPrefix(statement, "CREATE TABLE skill_candidate_imports (") {
			table = statement
			break
		}
	}
	if table == "" {
		panic("v183 requires the immutable v112 candidate receipt definition")
	}
	table = strings.ReplaceAll(table, "skill_candidate_imports", "skill_candidate_plugin_imports")
	table = strings.ReplaceAll(table, "REFERENCES skill_package_installations(id)", "REFERENCES plugin_installations(id)")
	table = strings.ReplaceAll(table, "'skill_candidate_import.v1'", "'skill_candidate_import.v2'")
	table = strings.Replace(table, "created_at TEXT NOT NULL,", `installation_generation INTEGER NOT NULL CHECK(installation_generation >= 1),
		package_fingerprint TEXT NOT NULL CHECK(length(package_fingerprint) = 64 AND package_fingerprint NOT GLOB '*[^0-9a-f]*'),
		archive_sha256 TEXT NOT NULL CHECK(length(archive_sha256) = 64 AND archive_sha256 NOT GLOB '*[^0-9a-f]*'),
		created_at TEXT NOT NULL,`, 1)
	return []string{table,
		`CREATE TRIGGER skill_candidate_plugin_import_insert_guard
		BEFORE INSERT ON skill_candidate_plugin_imports
		BEGIN
			SELECT RAISE(ABORT, 'candidate already has a legacy import receipt')
			WHERE EXISTS (SELECT 1 FROM skill_candidate_imports legacy
				WHERE legacy.candidate_id = NEW.candidate_id OR legacy.operation_key_digest = NEW.operation_key_digest);
			SELECT RAISE(ABORT, 'candidate Plugin import binding is invalid')
			WHERE NOT EXISTS (
				SELECT 1 FROM skill_candidates candidate
				JOIN skill_candidate_reviews review ON review.candidate_id = candidate.id
				JOIN plugin_installations installation ON installation.id = NEW.installation_id
				JOIN plugin_objects object ON object.archive_sha256 = installation.archive_sha256
					AND object.package_fingerprint = installation.package_fingerprint
				WHERE candidate.id = NEW.candidate_id
					AND candidate.candidate_fingerprint = NEW.candidate_fingerprint
					AND review.candidate_fingerprint = NEW.candidate_fingerprint
					AND review.review_fingerprint = NEW.review_fingerprint
					AND review.decision = 'approve' AND review.created_at <= NEW.created_at
					AND installation.protocol_version = 'plugin-installation.v2'
					AND installation.staged_by = NEW.imported_by
					AND installation.generation = NEW.installation_generation
					AND installation.package_fingerprint = NEW.package_fingerprint
					AND installation.archive_sha256 = NEW.archive_sha256
					AND installation.archive_sha256 = candidate.archive_sha256
					AND json_extract(installation.source_json, '$.kind') = 'catalog'
					AND json_extract(installation.source_json, '$.uri') = candidate.id
					AND json_extract(installation.source_json, '$.surface') = 'code'
					AND json_extract(installation.manifest_json, '$.format') = 'traverse-skill'
					AND json_extract(installation.manifest_json, '$.legacy.PackageFingerprint') = candidate.package_fingerprint
					AND json_extract(installation.manifest_json, '$.legacy.ArchiveSHA256') = candidate.archive_sha256
					AND installation.created_at <= NEW.created_at);
		END;`,
		`CREATE TRIGGER skill_candidate_legacy_import_no_plugin_duplicate
		BEFORE INSERT ON skill_candidate_imports
		BEGIN
			SELECT RAISE(ABORT, 'candidate already has a Plugin import receipt')
			WHERE EXISTS (SELECT 1 FROM skill_candidate_plugin_imports portable
				WHERE portable.candidate_id = NEW.candidate_id OR portable.operation_key_digest = NEW.operation_key_digest);
		END;`,
		`CREATE TRIGGER skill_candidate_plugin_imports_no_update BEFORE UPDATE ON skill_candidate_plugin_imports
		BEGIN SELECT RAISE(ABORT, 'Skill candidate Plugin imports are immutable'); END;`,
		`CREATE TRIGGER skill_candidate_plugin_imports_no_delete BEFORE DELETE ON skill_candidate_plugin_imports
		BEGIN SELECT RAISE(ABORT, 'Skill candidate Plugin imports are immutable'); END;`,
		`CREATE VIEW skill_candidate_all_imports AS
		SELECT id, protocol_version, operation_key_digest, request_fingerprint,
			candidate_id, candidate_fingerprint, review_fingerprint, installation_id,
			installation_fingerprint, imported_by, import_fingerprint, created_at,
			0 AS installation_generation, '' AS package_fingerprint, '' AS archive_sha256
		FROM skill_candidate_imports
		UNION ALL
		SELECT id, protocol_version, operation_key_digest, request_fingerprint,
			candidate_id, candidate_fingerprint, review_fingerprint, installation_id,
			installation_fingerprint, imported_by, import_fingerprint, created_at,
			installation_generation, package_fingerprint, archive_sha256
		FROM skill_candidate_plugin_imports;`,
	}
}
