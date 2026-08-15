package store

var browserNetworkReadinessStatements = []string{
	`CREATE TABLE browser_network_evidences (
		id TEXT PRIMARY KEY,
		fingerprint TEXT NOT NULL UNIQUE,
		executable_identity_fingerprint TEXT NOT NULL,
		acceptance_fingerprint TEXT NOT NULL,
		adapter TEXT NOT NULL,
		policy_version TEXT NOT NULL,
		collector_identity TEXT NOT NULL,
		passed INTEGER NOT NULL,
		payload_json TEXT NOT NULL,
		completed_at TEXT NOT NULL,
		expires_at TEXT NOT NULL,
		created_at TEXT NOT NULL,
		CHECK(json_valid(payload_json)),
		CHECK(json_extract(payload_json, '$.protocol_version') = 'browser_network_containment_evidence.v1'),
		CHECK(json_extract(payload_json, '$.id') = id),
		CHECK(json_extract(payload_json, '$.fingerprint') = fingerprint),
		CHECK(json_extract(payload_json, '$.executable_identity_fingerprint') = executable_identity_fingerprint),
		CHECK(json_extract(payload_json, '$.acceptance_fingerprint') = acceptance_fingerprint),
		CHECK(json_extract(payload_json, '$.adapter') = adapter),
		CHECK(json_extract(payload_json, '$.policy_version') = policy_version),
		CHECK(json_extract(payload_json, '$.collector_identity') = collector_identity),
		CHECK(json_extract(payload_json, '$.passed') = passed),
		CHECK(passed IN (0, 1)),
		CHECK(adapter = 'windows_wfp_dynamic.v1'),
		CHECK(policy_version = 'browser_network_containment_policy.v2'),
		CHECK(length(fingerprint) = 64 AND fingerprint = lower(fingerprint)
			AND fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(executable_identity_fingerprint) = 64
			AND executable_identity_fingerprint = lower(executable_identity_fingerprint)
			AND executable_identity_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(acceptance_fingerprint) = 64
			AND acceptance_fingerprint = lower(acceptance_fingerprint)
			AND acceptance_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(id = trim(id) AND length(id) BETWEEN 1 AND 256 AND instr(id, char(0)) = 0),
		CHECK(collector_identity = trim(collector_identity)
			AND length(collector_identity) BETWEEN 1 AND 256 AND instr(collector_identity, char(0)) = 0),
		CHECK(julianday(completed_at) IS NOT NULL
			AND julianday(expires_at) IS NOT NULL
			AND julianday(expires_at) > julianday(completed_at))
	) WITHOUT ROWID;`,
	`CREATE INDEX idx_browser_network_evidences_identity_completed
		ON browser_network_evidences(executable_identity_fingerprint, completed_at, id);`,
	`CREATE TABLE browser_network_evidence_operations (
		key_digest TEXT PRIMARY KEY,
		request_fingerprint TEXT NOT NULL,
		evidence_id TEXT NOT NULL,
		created_at TEXT NOT NULL,
		FOREIGN KEY(evidence_id) REFERENCES browser_network_evidences(id),
		CHECK(length(key_digest) = 64 AND key_digest = lower(key_digest)
			AND key_digest NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(request_fingerprint) = 64 AND request_fingerprint = lower(request_fingerprint)
			AND request_fingerprint NOT GLOB '*[^0-9a-f]*')
	) WITHOUT ROWID;`,
	`CREATE TABLE browser_network_reviews (
		id TEXT PRIMARY KEY,
		fingerprint TEXT NOT NULL UNIQUE,
		evidence_fingerprint TEXT NOT NULL,
		reviewer_identity TEXT NOT NULL,
		accepted INTEGER NOT NULL,
		reason_code TEXT NOT NULL,
		payload_json TEXT NOT NULL,
		reviewed_at TEXT NOT NULL,
		created_at TEXT NOT NULL,
		FOREIGN KEY(evidence_fingerprint) REFERENCES browser_network_evidences(fingerprint),
		CHECK(json_valid(payload_json)),
		CHECK(json_extract(payload_json, '$.protocol_version') = 'browser_network_containment_review.v1'),
		CHECK(json_extract(payload_json, '$.id') = id),
		CHECK(json_extract(payload_json, '$.fingerprint') = fingerprint),
		CHECK(json_extract(payload_json, '$.evidence_fingerprint') = evidence_fingerprint),
		CHECK(json_extract(payload_json, '$.reviewer_identity') = reviewer_identity),
		CHECK(json_extract(payload_json, '$.accepted') = accepted),
		CHECK(json_extract(payload_json, '$.reason_code') = reason_code),
		CHECK(accepted IN (0, 1)),
		CHECK(reason_code IN ('production_probe_confirmed', 'operator_rejected')),
		CHECK(length(fingerprint) = 64 AND fingerprint = lower(fingerprint)
			AND fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(evidence_fingerprint) = 64
			AND evidence_fingerprint = lower(evidence_fingerprint)
			AND evidence_fingerprint NOT GLOB '*[^0-9a-f]*'),
		CHECK(id = trim(id) AND length(id) BETWEEN 1 AND 256 AND instr(id, char(0)) = 0),
		CHECK(reviewer_identity = trim(reviewer_identity)
			AND length(reviewer_identity) BETWEEN 1 AND 256 AND instr(reviewer_identity, char(0)) = 0),
		CHECK(julianday(reviewed_at) IS NOT NULL)
	) WITHOUT ROWID;`,
	`CREATE INDEX idx_browser_network_reviews_evidence
		ON browser_network_reviews(evidence_fingerprint, reviewed_at, id);`,
	`CREATE TABLE browser_network_review_operations (
		key_digest TEXT PRIMARY KEY,
		request_fingerprint TEXT NOT NULL,
		review_id TEXT NOT NULL,
		created_at TEXT NOT NULL,
		FOREIGN KEY(review_id) REFERENCES browser_network_reviews(id),
		CHECK(length(key_digest) = 64 AND key_digest = lower(key_digest)
			AND key_digest NOT GLOB '*[^0-9a-f]*'),
		CHECK(length(request_fingerprint) = 64 AND request_fingerprint = lower(request_fingerprint)
			AND request_fingerprint NOT GLOB '*[^0-9a-f]*')
	) WITHOUT ROWID;`,
}
