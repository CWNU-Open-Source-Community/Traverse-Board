package store

var operatorSteeringPromotionStatements = []string{
	`CREATE TABLE operator_steering_promotions (
		id TEXT PRIMARY KEY,
		message_id TEXT NOT NULL UNIQUE REFERENCES operator_steering_messages(id) ON DELETE RESTRICT,
		replacement_message_id TEXT NOT NULL UNIQUE REFERENCES operator_steering_messages(id) ON DELETE RESTRICT,
		run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE RESTRICT,
		session_id TEXT NOT NULL,
		expected_revision INTEGER NOT NULL CHECK(expected_revision>=0),
		content_sha256 TEXT NOT NULL CHECK(length(content_sha256)=64),
		target_attempt_id TEXT NOT NULL,
		execution_id TEXT NOT NULL,
		cancellation_id TEXT NOT NULL UNIQUE REFERENCES operator_steering_cancellations(id) ON DELETE RESTRICT,
		requested_by TEXT NOT NULL,
		created_at TEXT NOT NULL,
		operation_key_digest TEXT NOT NULL UNIQUE CHECK(length(operation_key_digest)=64),
		request_fingerprint TEXT NOT NULL CHECK(length(request_fingerprint)=64),
		CHECK(message_id!=replacement_message_id)
	);`,
	`CREATE TABLE operator_steering_promotion_rejections (
		id TEXT PRIMARY KEY,
		message_id TEXT NOT NULL REFERENCES operator_steering_messages(id) ON DELETE RESTRICT,
		run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE RESTRICT,
		session_id TEXT NOT NULL,
		expected_revision INTEGER NOT NULL CHECK(expected_revision>=0),
		content_sha256 TEXT NOT NULL CHECK(length(content_sha256)=64),
		target_attempt_id TEXT NOT NULL,
		execution_id TEXT NOT NULL,
		requested_by TEXT NOT NULL,
		created_at TEXT NOT NULL,
		operation_key_digest TEXT NOT NULL UNIQUE CHECK(length(operation_key_digest)=64),
		request_fingerprint TEXT NOT NULL CHECK(length(request_fingerprint)=64)
	);`,
	`CREATE TRIGGER trg_operator_steering_promotion_binding BEFORE INSERT ON operator_steering_promotions
		WHEN EXISTS (SELECT 1 FROM operator_steering_promotion_rejections WHERE operation_key_digest=NEW.operation_key_digest)
		OR NOT EXISTS (SELECT 1 FROM operator_steering_messages old
			JOIN operator_steering_messages correction ON correction.id=NEW.replacement_message_id
			JOIN operator_steering_cancellations cancellation ON cancellation.id=NEW.cancellation_id
			WHERE old.id=NEW.message_id AND old.run_id=NEW.run_id AND old.session_id=NEW.session_id
			AND old.status='cancelled' AND old.delivery_mode='next_turn' AND old.revision=NEW.expected_revision
			AND old.content_sha256=NEW.content_sha256 AND old.image_count=0 AND old.attachment_count=0
			AND correction.run_id=old.run_id AND correction.session_id=old.session_id
			AND correction.status='pending' AND correction.delivery_mode='steer' AND correction.revision=0
			AND correction.content_sha256=NEW.content_sha256 AND correction.content=old.content
			AND correction.image_count=0 AND correction.attachment_count=0
			AND correction.target_attempt_id=NEW.target_attempt_id AND correction.requested_by=NEW.requested_by
			AND cancellation.message_id=old.id AND cancellation.run_id=old.run_id
			AND cancellation.kind='operator' AND cancellation.requested_by=NEW.requested_by)
		BEGIN SELECT RAISE(ABORT,'operator steering promotion binding is invalid'); END;`,
	`CREATE TRIGGER trg_operator_steering_promotion_immutable BEFORE UPDATE ON operator_steering_promotions
		BEGIN SELECT RAISE(ABORT,'operator steering promotion is immutable'); END;`,
	`CREATE TRIGGER trg_operator_steering_promotion_delete BEFORE DELETE ON operator_steering_promotions
		BEGIN SELECT RAISE(ABORT,'operator steering promotion cannot be deleted'); END;`,
	`CREATE TRIGGER trg_operator_steering_promotion_rejection_binding BEFORE INSERT ON operator_steering_promotion_rejections
		WHEN EXISTS (SELECT 1 FROM operator_steering_promotions WHERE operation_key_digest=NEW.operation_key_digest)
		OR NOT EXISTS (SELECT 1 FROM operator_steering_messages WHERE id=NEW.message_id AND run_id=NEW.run_id AND session_id=NEW.session_id)
		BEGIN SELECT RAISE(ABORT,'operator steering promotion rejection binding is invalid'); END;`,
	`CREATE TRIGGER trg_operator_steering_promotion_rejection_immutable BEFORE UPDATE ON operator_steering_promotion_rejections
		BEGIN SELECT RAISE(ABORT,'operator steering promotion rejection is immutable'); END;`,
	`CREATE TRIGGER trg_operator_steering_promotion_rejection_delete BEFORE DELETE ON operator_steering_promotion_rejections
		BEGIN SELECT RAISE(ABORT,'operator steering promotion rejection cannot be deleted'); END;`,
}
