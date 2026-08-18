package store

var skillPackageModeMetadataStatements = []string{
	`DROP TRIGGER skill_package_installation_insert_guard;`,
	`ALTER TABLE skill_package_installations ADD COLUMN surfaces_json TEXT NOT NULL DEFAULT '[]'
		CHECK(surfaces_json IN ('[]', '["code"]', '["cyber"]', '["code","cyber"]'));`,
	`ALTER TABLE skill_package_installations ADD COLUMN phases_json TEXT NOT NULL DEFAULT '[]'
		CHECK(phases_json IN ('[]', '["plan"]', '["deliver"]', '["plan","deliver"]'));`,
	`ALTER TABLE skill_package_installations ADD COLUMN roles_json TEXT NOT NULL DEFAULT '[]'
		CHECK(roles_json IN ('[]', '["root"]', '["specialist"]', '["root","specialist"]'));`,
	`ALTER TABLE skill_package_installations ADD COLUMN user_invocable INTEGER NOT NULL DEFAULT 0
		CHECK(user_invocable IN (0, 1));`,
	`ALTER TABLE skill_package_installations ADD COLUMN model_invocable INTEGER NOT NULL DEFAULT 0
		CHECK(model_invocable IN (0, 1));`,
	`ALTER TABLE skill_package_installations ADD COLUMN explicit_only INTEGER NOT NULL DEFAULT 0
		CHECK(explicit_only IN (0, 1));`,
	`CREATE TRIGGER skill_package_installation_insert_guard
		BEFORE INSERT ON skill_package_installations
		BEGIN
			SELECT RAISE(ABORT, 'Skill package installation operation binding mismatch')
			WHERE NOT EXISTS (
				SELECT 1 FROM skill_package_install_operations operation
				WHERE operation.key_digest = NEW.operation_key_digest
					AND operation.request_fingerprint = NEW.request_fingerprint
					AND operation.installation_id = NEW.id
					AND operation.name = NEW.name AND operation.version = NEW.version
					AND operation.surface = NEW.surface
					AND operation.installed_by = NEW.installed_by
					AND operation.created_at = NEW.created_at);
			SELECT RAISE(ABORT, 'Skill package installation Profile metadata is invalid')
			WHERE EXISTS (SELECT 1 FROM json_each(NEW.profiles_json)
				WHERE type != 'text' OR value NOT IN ('code', 'review', 'learn', 'script'));
			SELECT RAISE(ABORT, 'Cyber Skill package must be script-only')
			WHERE NEW.surface = 'cyber' AND NEW.profiles_json != '["script"]';
			SELECT RAISE(ABORT, 'Skill package mode metadata must be declared together')
			WHERE (NEW.surfaces_json = '[]') != (NEW.phases_json = '[]')
				OR (NEW.surfaces_json = '[]') != (NEW.roles_json = '[]');
			SELECT RAISE(ABORT, 'Legacy Skill package invocation metadata is invalid')
			WHERE NEW.surfaces_json = '[]'
				AND (NEW.user_invocable != 0 OR NEW.model_invocable != 0 OR NEW.explicit_only != 0);
			SELECT RAISE(ABORT, 'Mode-aware Skill package invocation metadata is invalid')
			WHERE NEW.surfaces_json != '[]' AND (
				(NEW.user_invocable = 0 AND NEW.model_invocable = 0)
				OR (NEW.explicit_only = 1
					AND (NEW.user_invocable != 1 OR NEW.model_invocable != 0)));
			SELECT RAISE(ABORT, 'Skill package installation surface is unsupported by its manifest')
			WHERE NEW.surfaces_json != '[]' AND NOT EXISTS (
				SELECT 1 FROM json_each(NEW.surfaces_json) WHERE value = NEW.surface);
			SELECT RAISE(ABORT, 'Skill package tool dependency metadata is invalid')
			WHERE EXISTS (SELECT 1 FROM json_each(NEW.tool_dependencies_json)
				WHERE type != 'text'
					OR value NOT IN ('list_workspace', 'read_file', 'replace_file', 'script_process',
						'skill_candidate_propose'));
			SELECT RAISE(ABORT, 'Skill package Registry capacity exceeded')
			WHERE (SELECT COUNT(*) FROM skill_package_installations) >= 64;
			SELECT RAISE(ABORT, 'Skill package version history capacity exceeded')
			WHERE (SELECT COUNT(*) FROM skill_package_installations
				WHERE name = NEW.name) >= 8;
		END;`,
}
