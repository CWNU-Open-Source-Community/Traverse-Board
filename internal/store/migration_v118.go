package store

var batchDeliveryStatements = []string{
	`CREATE TABLE batch_delivery_plans (
		id TEXT PRIMARY KEY,
		run_id TEXT NOT NULL,
		proposal_id TEXT NOT NULL UNIQUE,
		root_agent_id TEXT NOT NULL,
		workspace_id TEXT NOT NULL,
		status TEXT NOT NULL,
		spec_json TEXT NOT NULL,
		base_commit TEXT NOT NULL,
		source_branch TEXT NOT NULL,
		operation_digest TEXT NOT NULL UNIQUE,
		request_fingerprint TEXT NOT NULL,
		created_by TEXT NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(proposal_id) REFERENCES child_task_proposals(id) ON DELETE RESTRICT,
		FOREIGN KEY(root_agent_id) REFERENCES agent_nodes(id) ON DELETE RESTRICT,
		CHECK(status IN ('preparing', 'active', 'reviewing', 'merging',
			'completed', 'blocked', 'aborted')),
		CHECK(json_valid(spec_json) AND length(spec_json) BETWEEN 1 AND 65536),
		CHECK((length(base_commit) = 40 OR length(base_commit) = 64)
			AND base_commit = lower(base_commit)
			AND base_commit NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(operation_digest) = 64 AND operation_digest NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(request_fingerprint) = 64 AND request_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(source_branch) BETWEEN 1 AND 255),
		CHECK(length(created_by) BETWEEN 1 AND 256),
		CHECK(julianday(created_at) IS NOT NULL AND julianday(updated_at) IS NOT NULL
			AND julianday(updated_at) >= julianday(created_at))
	);`,
	`CREATE INDEX idx_batch_delivery_plans_run_created
		ON batch_delivery_plans(run_id, created_at DESC, id DESC);`,
	`CREATE TABLE batch_delivery_workspaces (
		plan_id TEXT NOT NULL,
		ordinal INTEGER NOT NULL,
		agent_id TEXT NOT NULL,
		generation INTEGER NOT NULL,
		status TEXT NOT NULL,
		branch TEXT NOT NULL UNIQUE,
		worktree_root TEXT NOT NULL UNIQUE,
		base_commit TEXT NOT NULL,
		head_commit TEXT NOT NULL DEFAULT '',
		owner_token_digest TEXT NOT NULL,
		tool_profile_json TEXT NOT NULL,
		tool_profile_fingerprint TEXT NOT NULL,
		lease_expires_at TEXT NOT NULL,
		last_heartbeat_at TEXT NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		PRIMARY KEY(plan_id, ordinal),
		FOREIGN KEY(plan_id) REFERENCES batch_delivery_plans(id) ON DELETE RESTRICT,
		FOREIGN KEY(agent_id) REFERENCES agent_nodes(id) ON DELETE RESTRICT,
		CHECK(ordinal BETWEEN 1 AND 2),
		CHECK(generation > 0),
		CHECK(status IN ('preparing', 'dispatched', 'acknowledged', 'working',
			'question', 'ready_for_review', 'changes_requested', 'accepted',
			'merged', 'cancelled', 'failed', 'orphaned')),
		CHECK(length(branch) BETWEEN 1 AND 255 AND length(worktree_root) BETWEEN 1 AND 4096),
		CHECK((length(base_commit) = 40 OR length(base_commit) = 64)
			AND base_commit = lower(base_commit)
			AND base_commit NOT GLOB '*[^0-9a-f]*'),
		CHECK(head_commit = '' OR ((length(head_commit) = 40 OR length(head_commit) = 64)
			AND head_commit = lower(head_commit)
			AND head_commit NOT GLOB '*[^0-9a-f]*')),
		CHECK(length(owner_token_digest) = 64 AND owner_token_digest NOT GLOB '*[^0-9a-f]*'),
		CHECK(json_valid(tool_profile_json) AND length(tool_profile_json) BETWEEN 1 AND 8192),
		CHECK(length(tool_profile_fingerprint) = 64
			AND tool_profile_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(julianday(lease_expires_at) IS NOT NULL AND julianday(last_heartbeat_at) IS NOT NULL),
		CHECK(julianday(created_at) IS NOT NULL AND julianday(updated_at) IS NOT NULL
			AND julianday(updated_at) >= julianday(created_at))
	) WITHOUT ROWID;`,
	`CREATE INDEX idx_batch_delivery_workspaces_lease
		ON batch_delivery_workspaces(status, lease_expires_at, plan_id, ordinal);`,
	`CREATE TABLE batch_delivery_mailbox (
		id TEXT PRIMARY KEY,
		plan_id TEXT NOT NULL,
		ordinal INTEGER NOT NULL,
		generation INTEGER NOT NULL,
		sequence INTEGER NOT NULL,
		kind TEXT NOT NULL,
		actor TEXT NOT NULL,
		summary TEXT NOT NULL,
		evidence_refs_json TEXT NOT NULL,
		operation_digest TEXT NOT NULL UNIQUE,
		request_fingerprint TEXT NOT NULL,
		created_at TEXT NOT NULL,
		UNIQUE(plan_id, ordinal, generation, sequence),
		FOREIGN KEY(plan_id, ordinal) REFERENCES batch_delivery_workspaces(plan_id, ordinal)
			ON DELETE RESTRICT,
		CHECK(generation > 0 AND sequence > 0),
		CHECK(kind IN ('dispatch', 'ack', 'progress', 'question', 'evidence',
			'ready_for_review', 'changes_requested', 'accepted', 'aborted')),
		CHECK(length(actor) BETWEEN 1 AND 256),
		CHECK(length(summary) BETWEEN 1 AND 16384),
		CHECK(json_valid(evidence_refs_json) AND length(evidence_refs_json) BETWEEN 2 AND 32768),
		CHECK(length(operation_digest) = 64 AND operation_digest NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(request_fingerprint) = 64 AND request_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(julianday(created_at) IS NOT NULL)
	);`,
	`CREATE INDEX idx_batch_delivery_mailbox_plan_sequence
		ON batch_delivery_mailbox(plan_id, ordinal, generation, sequence);`,
	`CREATE TABLE batch_delivery_receipts (
		id TEXT PRIMARY KEY,
		plan_id TEXT NOT NULL,
		ordinal INTEGER NOT NULL,
		generation INTEGER NOT NULL,
		protocol_version TEXT NOT NULL,
		base_commit TEXT NOT NULL,
		head_commit TEXT NOT NULL,
		diff_sha256 TEXT NOT NULL,
		call_chain_sha256 TEXT NOT NULL,
		diff_bytes INTEGER NOT NULL,
		diff_stat TEXT NOT NULL,
		changed_files_json TEXT NOT NULL,
		test_receipts_json TEXT NOT NULL,
		evidence_refs_json TEXT NOT NULL,
		limitations_json TEXT NOT NULL,
		operation_digest TEXT NOT NULL UNIQUE,
		request_fingerprint TEXT NOT NULL,
		created_at TEXT NOT NULL,
		UNIQUE(plan_id, ordinal, generation),
		FOREIGN KEY(plan_id, ordinal) REFERENCES batch_delivery_workspaces(plan_id, ordinal)
			ON DELETE RESTRICT,
		CHECK(protocol_version = 'batch-delivery-receipt.v1'),
		CHECK(generation > 0),
		CHECK((length(base_commit) = 40 OR length(base_commit) = 64)
			AND (length(head_commit) = 40 OR length(head_commit) = 64)),
		CHECK(length(diff_sha256) = 64 AND diff_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(call_chain_sha256) = 64 AND call_chain_sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK(diff_bytes BETWEEN 1 AND 16777216),
		CHECK(length(diff_stat) BETWEEN 1 AND 2048),
		CHECK(json_valid(changed_files_json) AND json_valid(test_receipts_json)
			AND json_valid(evidence_refs_json) AND json_valid(limitations_json)),
		CHECK(length(operation_digest) = 64 AND length(request_fingerprint) = 64),
		CHECK(julianday(created_at) IS NOT NULL)
	);`,
	`CREATE TABLE batch_delivery_reviews (
		id TEXT PRIMARY KEY,
		plan_id TEXT NOT NULL,
		ordinal INTEGER NOT NULL,
		generation INTEGER NOT NULL,
		protocol_version TEXT NOT NULL,
		receipt_id TEXT NOT NULL UNIQUE,
		reviewer TEXT NOT NULL,
		verdict TEXT NOT NULL,
		summary TEXT NOT NULL,
		base_commit TEXT NOT NULL,
		head_commit TEXT NOT NULL,
		diff_sha256 TEXT NOT NULL,
		call_chain_sha256 TEXT NOT NULL,
		full_diff_reviewed INTEGER NOT NULL,
		call_chain_reviewed INTEGER NOT NULL,
		tests_reviewed INTEGER NOT NULL,
		operation_digest TEXT NOT NULL UNIQUE,
		request_fingerprint TEXT NOT NULL,
		created_at TEXT NOT NULL,
		FOREIGN KEY(receipt_id) REFERENCES batch_delivery_receipts(id) ON DELETE RESTRICT,
		FOREIGN KEY(plan_id, ordinal) REFERENCES batch_delivery_workspaces(plan_id, ordinal)
			ON DELETE RESTRICT,
		CHECK(protocol_version = 'batch-delivery-review.v1'),
		CHECK(generation > 0),
		CHECK(verdict IN ('accepted', 'changes_requested')),
		CHECK(length(reviewer) BETWEEN 1 AND 256 AND length(summary) BETWEEN 1 AND 16384),
		CHECK(length(diff_sha256) = 64 AND length(call_chain_sha256) = 64),
		CHECK(full_diff_reviewed = 1 AND call_chain_reviewed = 1 AND tests_reviewed = 1),
		CHECK(length(operation_digest) = 64 AND length(request_fingerprint) = 64),
		CHECK(julianday(created_at) IS NOT NULL)
	);`,
	`CREATE TABLE batch_delivery_merge_queues (
		id TEXT PRIMARY KEY,
		plan_id TEXT NOT NULL,
		protocol_version TEXT NOT NULL,
		status TEXT NOT NULL,
		base_commit TEXT NOT NULL,
		latest_base_commit TEXT NOT NULL,
		integration_branch TEXT NOT NULL UNIQUE,
		integration_root TEXT NOT NULL UNIQUE,
		integration_head TEXT NOT NULL DEFAULT '',
		ordered_ordinals_json TEXT NOT NULL,
		next_index INTEGER NOT NULL,
		failure_code TEXT NOT NULL DEFAULT '',
		failure_summary TEXT NOT NULL DEFAULT '',
		operation_digest TEXT NOT NULL UNIQUE,
		request_fingerprint TEXT NOT NULL,
		created_by TEXT NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		FOREIGN KEY(plan_id) REFERENCES batch_delivery_plans(id) ON DELETE RESTRICT,
		CHECK(protocol_version = 'batch-delivery-merge-queue.v1'),
		CHECK(status IN ('prepared', 'running', 'blocked', 'completed', 'aborted')),
		CHECK((length(base_commit) = 40 OR length(base_commit) = 64)
			AND (length(latest_base_commit) = 40 OR length(latest_base_commit) = 64)),
		CHECK(integration_head = '' OR length(integration_head) = 40 OR length(integration_head) = 64),
		CHECK(json_valid(ordered_ordinals_json) AND next_index >= 0),
		CHECK(length(operation_digest) = 64 AND length(request_fingerprint) = 64),
		CHECK(length(created_by) BETWEEN 1 AND 256),
		CHECK(julianday(created_at) IS NOT NULL AND julianday(updated_at) IS NOT NULL)
	);`,
	`CREATE INDEX idx_batch_delivery_merge_queues_plan_created
		ON batch_delivery_merge_queues(plan_id, created_at DESC, id DESC);`,
	`CREATE TABLE batch_delivery_merge_steps (
		queue_id TEXT NOT NULL,
		step_index INTEGER NOT NULL,
		ordinal INTEGER NOT NULL,
		input_head TEXT NOT NULL,
		pre_merge_head TEXT NOT NULL,
		post_merge_head TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL,
		validation_json TEXT NOT NULL,
		failure_code TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		completed_at TEXT,
		PRIMARY KEY(queue_id, step_index),
		FOREIGN KEY(queue_id) REFERENCES batch_delivery_merge_queues(id) ON DELETE RESTRICT,
		CHECK(step_index >= 0 AND ordinal BETWEEN 1 AND 2),
		CHECK((length(input_head) = 40 OR length(input_head) = 64)
			AND (length(pre_merge_head) = 40 OR length(pre_merge_head) = 64)),
		CHECK(post_merge_head = '' OR length(post_merge_head) = 40 OR length(post_merge_head) = 64),
		CHECK(status IN ('prepared', 'running', 'blocked', 'completed', 'aborted')),
		CHECK(json_valid(validation_json)),
		CHECK(julianday(created_at) IS NOT NULL),
		CHECK(completed_at IS NULL OR julianday(completed_at) IS NOT NULL)
	) WITHOUT ROWID;`,
	`CREATE TRIGGER trg_batch_delivery_mailbox_update_immutable
		BEFORE UPDATE ON batch_delivery_mailbox BEGIN
			SELECT RAISE(ABORT, 'batch delivery mailbox messages are immutable');
		END;`,
	`CREATE TRIGGER trg_batch_delivery_mailbox_delete_immutable
		BEFORE DELETE ON batch_delivery_mailbox BEGIN
			SELECT RAISE(ABORT, 'batch delivery mailbox messages are immutable');
		END;`,
	`CREATE TRIGGER trg_batch_delivery_receipts_update_immutable
		BEFORE UPDATE ON batch_delivery_receipts BEGIN
			SELECT RAISE(ABORT, 'batch delivery receipts are immutable');
		END;`,
	`CREATE TRIGGER trg_batch_delivery_receipts_delete_immutable
		BEFORE DELETE ON batch_delivery_receipts BEGIN
			SELECT RAISE(ABORT, 'batch delivery receipts are immutable');
		END;`,
	`CREATE TRIGGER trg_batch_delivery_reviews_update_immutable
		BEFORE UPDATE ON batch_delivery_reviews BEGIN
			SELECT RAISE(ABORT, 'batch delivery reviews are immutable');
		END;`,
	`CREATE TRIGGER trg_batch_delivery_reviews_delete_immutable
		BEFORE DELETE ON batch_delivery_reviews BEGIN
			SELECT RAISE(ABORT, 'batch delivery reviews are immutable');
		END;`,
}
