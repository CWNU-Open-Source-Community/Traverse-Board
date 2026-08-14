package store

var sandboxDockerContainerIOStatements = []string{
	`CREATE TABLE sandbox_docker_input_projections (
		id TEXT PRIMARY KEY,
		attempt_id TEXT NOT NULL UNIQUE,
		generation INTEGER NOT NULL,
		plan_id TEXT NOT NULL,
		observation_id TEXT NOT NULL,
		run_id TEXT NOT NULL,
		mission_id TEXT NOT NULL,
		workspace_id TEXT NOT NULL,
		protocol_version TEXT NOT NULL,
		input_artifact_digest TEXT NOT NULL,
		spec_fingerprint TEXT NOT NULL,
		authority_fingerprint TEXT NOT NULL,
		mount_target TEXT NOT NULL,
		mount_read_only INTEGER NOT NULL,
		entry_count INTEGER NOT NULL,
		total_bytes INTEGER NOT NULL,
		projection_fingerprint TEXT NOT NULL UNIQUE,
		created_at TEXT NOT NULL,
		FOREIGN KEY(plan_id) REFERENCES sandbox_docker_container_plans(id) ON DELETE RESTRICT,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(mission_id) REFERENCES missions(id) ON DELETE RESTRICT,
		FOREIGN KEY(workspace_id) REFERENCES workspaces(id) ON DELETE RESTRICT,
		CHECK(protocol_version = 'sandbox_docker_input_projection.v1'),
		CHECK(generation = 1),
		CHECK(mount_target = '/run/cyberagent/inputs'),
		CHECK(mount_read_only = 1),
		CHECK(entry_count BETWEEN 1 AND 16),
		CHECK(total_bytes BETWEEN 1 AND 16777216),
		CHECK(length(input_artifact_digest) = 64
			AND input_artifact_digest = lower(input_artifact_digest)
			AND input_artifact_digest NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(spec_fingerprint) = 64
			AND spec_fingerprint = lower(spec_fingerprint)
			AND spec_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(authority_fingerprint) = 64
			AND authority_fingerprint = lower(authority_fingerprint)
			AND authority_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(projection_fingerprint) = 64
			AND projection_fingerprint = lower(projection_fingerprint)
			AND projection_fingerprint NOT GLOB '*[^0-9a-f]*')
	)`,
	`CREATE TABLE sandbox_docker_log_capture_receipts (
		id TEXT PRIMARY KEY,
		attempt_id TEXT NOT NULL,
		generation INTEGER NOT NULL,
		run_id TEXT NOT NULL,
		container_id_fingerprint TEXT NOT NULL,
		protocol_version TEXT NOT NULL,
		status TEXT NOT NULL,
		stdout_bytes INTEGER NOT NULL,
		stdout_lines INTEGER NOT NULL,
		stdout_truncated_bytes INTEGER NOT NULL,
		stdout_truncated_lines INTEGER NOT NULL,
		stdout_truncated_deadline INTEGER NOT NULL,
		stdout_utf8_violations INTEGER NOT NULL,
		stdout_redacted_segments INTEGER NOT NULL,
		stdout_content_digest TEXT NOT NULL,
		stderr_bytes INTEGER NOT NULL,
		stderr_lines INTEGER NOT NULL,
		stderr_truncated_bytes INTEGER NOT NULL,
		stderr_truncated_lines INTEGER NOT NULL,
		stderr_truncated_deadline INTEGER NOT NULL,
		stderr_utf8_violations INTEGER NOT NULL,
		stderr_redacted_segments INTEGER NOT NULL,
		stderr_content_digest TEXT NOT NULL,
		capture_max_bytes INTEGER NOT NULL,
		capture_max_lines INTEGER NOT NULL,
		capture_fingerprint TEXT NOT NULL,
		receipt_fingerprint TEXT NOT NULL UNIQUE,
		created_at TEXT NOT NULL,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		CHECK(protocol_version = 'sandbox_docker_log_capture.v1'),
		CHECK(generation = 1),
		CHECK(status IN ('completed', 'truncated_bytes', 'truncated_lines',
			'truncated_deadline', 'invalid_stream')),
		CHECK(capture_max_bytes BETWEEN 1 AND 262144),
		CHECK(capture_max_lines BETWEEN 1 AND 4096),
		CHECK(stdout_bytes BETWEEN 0 AND capture_max_bytes),
		CHECK(stderr_bytes BETWEEN 0 AND capture_max_bytes),
		CHECK(stdout_lines BETWEEN 0 AND capture_max_lines),
		CHECK(stderr_lines BETWEEN 0 AND capture_max_lines),
		CHECK(stdout_truncated_bytes IN (0, 1) AND stdout_truncated_lines IN (0, 1)
			AND stdout_truncated_deadline IN (0, 1)),
		CHECK(stderr_truncated_bytes IN (0, 1) AND stderr_truncated_lines IN (0, 1)
			AND stderr_truncated_deadline IN (0, 1)),
		CHECK(stdout_utf8_violations >= 0 AND stderr_utf8_violations >= 0),
		CHECK(stdout_redacted_segments >= 0 AND stderr_redacted_segments >= 0),
		CHECK(length(container_id_fingerprint) = 64
			AND container_id_fingerprint = lower(container_id_fingerprint)
			AND container_id_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(stdout_content_digest) = 64
			AND stdout_content_digest = lower(stdout_content_digest)
			AND stdout_content_digest NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(stderr_content_digest) = 64
			AND stderr_content_digest = lower(stderr_content_digest)
			AND stderr_content_digest NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(capture_fingerprint) = 64
			AND capture_fingerprint = lower(capture_fingerprint)
			AND capture_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(receipt_fingerprint) = 64
			AND receipt_fingerprint = lower(receipt_fingerprint)
			AND receipt_fingerprint NOT GLOB '*[^0-9a-f]*')
	)`,
	`CREATE TABLE sandbox_docker_output_staging_receipts (
		id TEXT PRIMARY KEY,
		attempt_id TEXT NOT NULL UNIQUE,
		generation INTEGER NOT NULL,
		run_id TEXT NOT NULL,
		container_id_fingerprint TEXT NOT NULL,
		protocol_version TEXT NOT NULL,
		status TEXT NOT NULL,
		file_count INTEGER NOT NULL,
		total_bytes INTEGER NOT NULL,
		redacted_count INTEGER NOT NULL,
		entry_digest_set TEXT NOT NULL,
		export_fingerprint TEXT NOT NULL,
		receipt_fingerprint TEXT NOT NULL UNIQUE,
		created_at TEXT NOT NULL,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		CHECK(protocol_version = 'sandbox_docker_output_staging.v1'),
		CHECK(generation = 1),
		CHECK(status IN ('completed', 'truncated_bytes', 'invalid_archive',
			'rejected_path', 'rejected_link', 'rejected_duplicate')),
		CHECK(file_count BETWEEN 0 AND 64),
		CHECK(total_bytes BETWEEN 0 AND 16777216),
		CHECK(redacted_count BETWEEN 0 AND file_count),
		CHECK(length(container_id_fingerprint) = 64
			AND container_id_fingerprint = lower(container_id_fingerprint)
			AND container_id_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(entry_digest_set) = 64
			AND entry_digest_set = lower(entry_digest_set)
			AND entry_digest_set NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(export_fingerprint) = 64
			AND export_fingerprint = lower(export_fingerprint)
			AND export_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(receipt_fingerprint) = 64
			AND receipt_fingerprint = lower(receipt_fingerprint)
			AND receipt_fingerprint NOT GLOB '*[^0-9a-f]*')
	)`,
	`CREATE TABLE sandbox_docker_output_commit_receipts (
		id TEXT PRIMARY KEY,
		attempt_id TEXT NOT NULL,
		generation INTEGER NOT NULL,
		run_id TEXT NOT NULL,
		workspace_id TEXT NOT NULL,
		operation_key_digest TEXT NOT NULL UNIQUE,
		request_fingerprint TEXT NOT NULL,
		protocol_version TEXT NOT NULL,
		status TEXT NOT NULL,
		committed_count INTEGER NOT NULL,
		committed_digest_set TEXT NOT NULL,
		receipt_fingerprint TEXT NOT NULL UNIQUE,
		created_at TEXT NOT NULL,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(workspace_id) REFERENCES workspaces(id) ON DELETE RESTRICT,
		CHECK(protocol_version = 'sandbox_docker_output_commit.v1'),
		CHECK(generation = 1),
		CHECK(status = 'committed'),
		CHECK(committed_count BETWEEN 1 AND 64),
		CHECK(length(operation_key_digest) = 64
			AND operation_key_digest = lower(operation_key_digest)
			AND operation_key_digest NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(request_fingerprint) = 64
			AND request_fingerprint = lower(request_fingerprint)
			AND request_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(committed_digest_set) = 64
			AND committed_digest_set = lower(committed_digest_set)
			AND committed_digest_set NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(receipt_fingerprint) = 64
			AND receipt_fingerprint = lower(receipt_fingerprint)
			AND receipt_fingerprint NOT GLOB '*[^0-9a-f]*')
	)`,
	`CREATE TABLE sandbox_docker_output_commit_entries (
		receipt_id TEXT NOT NULL,
		ordinal INTEGER NOT NULL,
		path TEXT NOT NULL,
		sha256 TEXT NOT NULL,
		size_bytes INTEGER NOT NULL,
		media_type TEXT NOT NULL,
		PRIMARY KEY (receipt_id, ordinal),
		FOREIGN KEY(receipt_id) REFERENCES sandbox_docker_output_commit_receipts(id)
			ON DELETE RESTRICT,
		CHECK(ordinal BETWEEN 1 AND 64),
		CHECK(length(path) BETWEEN 1 AND 1024),
		CHECK(path NOT GLOB '/*' AND path NOT GLOB '*..*'),
		CHECK(size_bytes BETWEEN 1 AND 4194304),
		CHECK(length(sha256) = 64
			AND sha256 = lower(sha256)
			AND sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(media_type) BETWEEN 1 AND 256)
	)`,
}
