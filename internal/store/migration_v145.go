package store

var runNetworkAuthorityExpansionStatements = []string{
	`CREATE TABLE run_network_authority_operations (
		operation_key_digest TEXT PRIMARY KEY,
		request_fingerprint TEXT NOT NULL,
		snapshot_id TEXT NOT NULL UNIQUE,
		run_id TEXT NOT NULL,
		expected_mode_revision INTEGER NOT NULL,
		requested_by TEXT NOT NULL,
		created_at TEXT NOT NULL,
		FOREIGN KEY(snapshot_id) REFERENCES run_mode_snapshots(id) ON DELETE RESTRICT
			DEFERRABLE INITIALLY DEFERRED,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		CHECK(length(operation_key_digest) = 64
			AND operation_key_digest = lower(operation_key_digest)
			AND operation_key_digest NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(request_fingerprint) = 64
			AND request_fingerprint = lower(request_fingerprint)
			AND request_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(expected_mode_revision > 0),
		CHECK(requested_by = trim(requested_by) AND length(requested_by) BETWEEN 1 AND 256
			AND instr(requested_by, char(0)) = 0)
	);`,
	`CREATE INDEX idx_run_network_authority_operations_run_created
		ON run_network_authority_operations(run_id, created_at);`,
	`DROP TRIGGER trg_run_mode_snapshot_insert;`,
	`CREATE TRIGGER trg_run_mode_snapshot_insert
		BEFORE INSERT ON run_mode_snapshots
		WHEN NOT EXISTS (
			SELECT 1 FROM runs run
			JOIN missions mission ON mission.id = run.mission_id
			WHERE run.id = NEW.run_id AND run.mission_id = NEW.mission_id
				AND mission.profile = NEW.profile
				AND julianday(NEW.created_at) >= julianday(run.created_at)
				AND (
					(NEW.revision = 1 AND run.status = 'created'
						AND json_extract(mission.scope_json, '$.workspace_id')
							IS json_extract(NEW.scope_json, '$.workspace_id')
						AND NOT EXISTS (
							SELECT 1 FROM run_mode_snapshots existing
							WHERE existing.run_id = NEW.run_id
						)
						AND (
							mission.scope_json = NEW.scope_json
							OR (
								json_extract(NEW.scope_json, '$.network_mode') = 'disabled'
								AND COALESCE(json_array_length(NEW.scope_json, '$.allowed_targets'), 0) = 0
								AND EXISTS (
									SELECT 1 FROM runs predecessor
									WHERE predecessor.mission_id = mission.id
										AND predecessor.id != run.id
										AND predecessor.status IN ('completed', 'failed', 'cancelled')
								)
							)
							OR EXISTS (
								SELECT 1 FROM runs predecessor
								JOIN run_mode_snapshots inherited
									ON inherited.run_id = predecessor.id
								WHERE predecessor.mission_id = mission.id
									AND predecessor.id != run.id
									AND predecessor.status IN ('completed', 'failed', 'cancelled')
									AND inherited.scope_json = NEW.scope_json
									AND inherited.revision = (
										SELECT MAX(latest.revision) FROM run_mode_snapshots latest
										WHERE latest.run_id = predecessor.id
									)
							)
						)
						AND (
							(json_extract(NEW.scope_json, '$.network_mode') = 'disabled'
								AND COALESCE(json_array_length(NEW.scope_json, '$.allowed_targets'), 0) = 0)
							OR (
								json_extract(NEW.scope_json, '$.network_mode') = 'allowlist'
								AND json_type(NEW.scope_json, '$.allowed_targets') = 'array'
								AND json_array_length(NEW.scope_json, '$.allowed_targets') BETWEEN 1 AND 256
								AND NOT EXISTS (
									SELECT 1 FROM json_each(NEW.scope_json, '$.allowed_targets') target
									WHERE target.type != 'text' OR length(target.value) NOT BETWEEN 1 AND 253
										OR target.value != trim(target.value)
										OR target.value != lower(target.value)
										OR target.value = 'public_https'
										OR target.value GLOB '*[^a-z0-9.-]*'
										OR instr(target.value, '.') = 0
										OR target.value LIKE '.%' OR target.value LIKE '%.'
										OR target.value LIKE '-%' OR target.value LIKE '%-'
										OR target.value LIKE '%..%' OR target.value LIKE '%.-%'
										OR target.value LIKE '%-.%'
										OR target.value NOT GLOB '*[a-z-]*'
										OR target.value = 'localhost' OR target.value LIKE '%.localhost'
										OR target.value LIKE '%.local' OR target.value LIKE '%.internal'
										OR target.value LIKE '%.intranet' OR target.value LIKE '%.corp'
										OR target.value LIKE '%.home' OR target.value LIKE '%.home.arpa'
										OR target.value LIKE '%.lan'
										OR target.value LIKE '%.localdomain' OR target.value LIKE '%.test'
										OR target.value LIKE '%.invalid' OR target.value LIKE '%.example'
										OR target.value IN ('metadata.google.internal',
											'metadata.azure.internal', 'instance-data.ec2.internal')
								)
								AND NOT EXISTS (
									SELECT 1 FROM json_each(NEW.scope_json, '$.allowed_targets') earlier
									JOIN json_each(NEW.scope_json, '$.allowed_targets') later
										ON CAST(earlier.key AS INTEGER) < CAST(later.key AS INTEGER)
									WHERE earlier.value >= later.value
								)
							)
						))
					OR
					(NEW.revision > 1 AND run.status IN ('created', 'paused')
						AND NOT EXISTS (
							SELECT 1 FROM run_execution_leases lease
							WHERE lease.run_id = NEW.run_id AND lease.status = 'active'
								AND julianday(lease.expires_at) > julianday('now')
						) AND EXISTS (
							SELECT 1 FROM run_mode_snapshots previous
							WHERE previous.run_id = NEW.run_id
								AND previous.revision = NEW.revision - 1
								AND previous.protocol_version = NEW.protocol_version
								AND previous.surface = NEW.surface
								AND previous.profile = NEW.profile
								AND previous.policy_version = NEW.policy_version
								AND json_extract(previous.scope_json, '$.workspace_id')
									IS json_extract(NEW.scope_json, '$.workspace_id')
								AND julianday(NEW.created_at) >= julianday(previous.created_at)
								AND (
									(previous.phase != NEW.phase
										AND previous.scope_json = NEW.scope_json)
									OR
									(previous.phase = NEW.phase
										AND json_extract(NEW.scope_json, '$.network_mode') = 'allowlist'
										AND json_type(NEW.scope_json, '$.allowed_targets') = 'array'
										AND json_array_length(NEW.scope_json, '$.allowed_targets') BETWEEN 1 AND 256
										AND NOT EXISTS (
											SELECT 1 FROM json_each(NEW.scope_json, '$.allowed_targets') target
											WHERE target.type != 'text' OR length(target.value) NOT BETWEEN 1 AND 253
												OR target.value != trim(target.value)
												OR target.value != lower(target.value)
												OR target.value = 'public_https'
												OR target.value GLOB '*[^a-z0-9.-]*'
												OR instr(target.value, '.') = 0
												OR target.value LIKE '.%' OR target.value LIKE '%.'
												OR target.value LIKE '-%' OR target.value LIKE '%-'
												OR target.value LIKE '%..%' OR target.value LIKE '%.-%'
												OR target.value LIKE '%-.%'
												OR target.value NOT GLOB '*[a-z-]*'
												OR target.value = 'localhost' OR target.value LIKE '%.localhost'
												OR target.value LIKE '%.local' OR target.value LIKE '%.internal'
												OR target.value LIKE '%.intranet' OR target.value LIKE '%.corp'
												OR target.value LIKE '%.home' OR target.value LIKE '%.home.arpa'
												OR target.value LIKE '%.lan'
												OR target.value LIKE '%.localdomain' OR target.value LIKE '%.test'
												OR target.value LIKE '%.invalid' OR target.value LIKE '%.example'
												OR target.value IN ('metadata.google.internal',
													'metadata.azure.internal', 'instance-data.ec2.internal')
										)
										AND NOT EXISTS (
											SELECT 1 FROM json_each(NEW.scope_json, '$.allowed_targets') earlier
											JOIN json_each(NEW.scope_json, '$.allowed_targets') later
												ON CAST(earlier.key AS INTEGER) < CAST(later.key AS INTEGER)
											WHERE earlier.value >= later.value
										)
										AND json_extract(previous.scope_json, '$.network_mode') IN ('disabled', 'allowlist')
										AND COALESCE(json_array_length(previous.scope_json, '$.allowed_targets'), 0)
											< json_array_length(NEW.scope_json, '$.allowed_targets')
										AND NOT EXISTS (
											SELECT 1
											FROM json_each(previous.scope_json, '$.allowed_targets') old_target
											WHERE NOT EXISTS (
												SELECT 1 FROM json_each(NEW.scope_json, '$.allowed_targets') new_target
												WHERE new_target.type = 'text'
													AND new_target.value = old_target.value
											)
										)
										AND EXISTS (
											SELECT 1 FROM run_network_authority_operations operation
											WHERE operation.snapshot_id = NEW.id
												AND operation.run_id = NEW.run_id
												AND operation.expected_mode_revision = NEW.revision - 1
												AND operation.requested_by = NEW.requested_by
												AND operation.created_at = NEW.created_at
										)
									)
								)
						))
				)
		)
		BEGIN
			SELECT RAISE(ABORT, 'Run mode snapshot binding or transition is invalid');
		END;`,
	`CREATE TRIGGER trg_run_network_authority_operation_update_immutable
		BEFORE UPDATE ON run_network_authority_operations BEGIN
			SELECT RAISE(ABORT, 'Run network authority operation cannot be updated');
		END;`,
	`CREATE TRIGGER trg_run_network_authority_operation_delete_immutable
		BEFORE DELETE ON run_network_authority_operations BEGIN
			SELECT RAISE(ABORT, 'Run network authority operation cannot be deleted');
		END;`,
}
