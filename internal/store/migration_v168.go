package store

// operatorSteeringRevisionStatements preserves the original submission identity
// while allowing one CAS guarded pending-to-pending content revision at a time.
// Attachment evidence receipts move model-visible evidence to delivery commit.
var operatorSteeringRevisionStatements = []string{
	`DROP TRIGGER trg_operator_steering_update_monotonic;`,
	`ALTER TABLE operator_steering_messages ADD COLUMN revision INTEGER NOT NULL DEFAULT 0 CHECK(revision >= 0);`,
	`ALTER TABLE operator_steering_messages ADD COLUMN original_content TEXT NOT NULL DEFAULT '';`,
	`ALTER TABLE operator_steering_messages ADD COLUMN original_content_sha256 TEXT NOT NULL DEFAULT '';`,
	`ALTER TABLE operator_steering_messages ADD COLUMN edited_at TEXT;`,
	`UPDATE operator_steering_messages SET original_content=content, original_content_sha256=content_sha256;`,
	`CREATE TABLE operator_steering_revisions (
		id TEXT PRIMARY KEY,
		message_id TEXT NOT NULL,
		run_id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		from_revision INTEGER NOT NULL,
		to_revision INTEGER NOT NULL,
		old_content_sha256 TEXT NOT NULL,
		new_content TEXT NOT NULL,
		new_content_sha256 TEXT NOT NULL,
		requested_by TEXT NOT NULL,
		created_at TEXT NOT NULL,
		operation_key_digest TEXT NOT NULL UNIQUE,
		request_fingerprint TEXT NOT NULL,
		FOREIGN KEY(message_id) REFERENCES operator_steering_messages(id) ON DELETE RESTRICT,
		FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE RESTRICT,
		FOREIGN KEY(session_id) REFERENCES sessions(id) ON DELETE RESTRICT,
		UNIQUE(message_id,to_revision),
		CHECK(from_revision >= 0 AND to_revision = from_revision + 1),
		CHECK(length(CAST(new_content AS BLOB)) BETWEEN 0 AND 16384),
		CHECK(length(old_content_sha256)=64 AND length(new_content_sha256)=64),
		CHECK(length(operation_key_digest)=64 AND length(request_fingerprint)=64),
		CHECK(requested_by=trim(requested_by) AND length(requested_by) BETWEEN 1 AND 256)
	);`,
	`CREATE INDEX idx_operator_steering_revisions_message ON operator_steering_revisions(message_id,to_revision);`,
	`CREATE TRIGGER trg_operator_steering_revision_insert
		BEFORE INSERT ON operator_steering_revisions
		WHEN NOT EXISTS (
			SELECT 1 FROM operator_steering_messages message JOIN runs run ON run.id=message.run_id
			WHERE message.id=NEW.message_id AND message.run_id=NEW.run_id
				AND message.session_id=NEW.session_id AND run.session_id=NEW.session_id
				AND run.status IN ('running','paused') AND message.status='pending'
				AND message.revision=NEW.from_revision
				AND message.content_sha256=NEW.old_content_sha256
				AND NOT EXISTS (SELECT 1 FROM operator_steering_deliveries delivery
					WHERE delivery.message_id=message.id AND delivery.status='prepared'))
		BEGIN SELECT RAISE(ABORT,'operator steering revision source is invalid'); END;`,
	`CREATE TRIGGER trg_operator_steering_revision_update_immutable BEFORE UPDATE ON operator_steering_revisions
		BEGIN SELECT RAISE(ABORT,'operator steering revisions cannot be updated'); END;`,
	`CREATE TRIGGER trg_operator_steering_revision_delete_immutable BEFORE DELETE ON operator_steering_revisions
		BEGIN SELECT RAISE(ABORT,'operator steering revisions cannot be deleted'); END;`,
	`CREATE TABLE operator_message_attachment_evidence (
		message_id TEXT NOT NULL,
		kind TEXT NOT NULL,
		attachment_id TEXT NOT NULL,
		session_message_id INTEGER NOT NULL UNIQUE,
		PRIMARY KEY(message_id,kind,attachment_id),
		FOREIGN KEY(message_id) REFERENCES operator_steering_messages(id) ON DELETE RESTRICT,
		FOREIGN KEY(session_message_id) REFERENCES session_messages(id) ON DELETE RESTRICT,
		CHECK(kind IN ('image','file'))
	) WITHOUT ROWID;`,
	`INSERT OR IGNORE INTO operator_message_attachment_evidence(message_id,kind,attachment_id,session_message_id)
		SELECT binding.message_id,'image',binding.image_id,MIN(evidence.id)
		FROM thread_message_images binding JOIN operator_steering_messages message ON message.id=binding.message_id
		JOIN session_messages evidence ON evidence.session_id=message.session_id
			AND evidence.source_kind='workspace_image' AND evidence.source_ref=binding.image_id
			AND instr(evidence.content,'"operator_message_id":"'||binding.message_id||'"')>0
		GROUP BY binding.message_id,binding.image_id;`,
	`INSERT OR IGNORE INTO operator_message_attachment_evidence(message_id,kind,attachment_id,session_message_id)
		SELECT binding.message_id,'file',binding.attachment_id,MIN(evidence.id)
		FROM thread_message_attachments binding JOIN operator_steering_messages message ON message.id=binding.message_id
		JOIN session_messages evidence ON evidence.session_id=message.session_id
			AND evidence.source_kind='uploaded_file' AND evidence.source_ref=binding.attachment_id
			AND instr(evidence.content,'"operator_message_id":"'||binding.message_id||'"')>0
		GROUP BY binding.message_id,binding.attachment_id;`,
	`CREATE TRIGGER trg_operator_message_attachment_evidence_insert
		BEFORE INSERT ON operator_message_attachment_evidence
		WHEN NOT EXISTS (
			SELECT 1 FROM operator_steering_messages message JOIN session_messages evidence
				ON evidence.id=NEW.session_message_id AND evidence.session_id=message.session_id
			WHERE message.id=NEW.message_id AND message.status='committed'
				AND instr(evidence.content,'"operator_message_id":"'||NEW.message_id||'"')>0
				AND ((NEW.kind='image' AND evidence.source_kind='workspace_image'
					AND evidence.source_ref=NEW.attachment_id AND EXISTS(SELECT 1 FROM thread_message_images b WHERE b.message_id=message.id AND b.image_id=NEW.attachment_id))
				OR (NEW.kind='file' AND evidence.source_kind='uploaded_file'
					AND evidence.source_ref=NEW.attachment_id AND EXISTS(SELECT 1 FROM thread_message_attachments b WHERE b.message_id=message.id AND b.attachment_id=NEW.attachment_id))))
		BEGIN SELECT RAISE(ABORT,'operator message attachment evidence binding is invalid'); END;`,
	`CREATE TRIGGER trg_operator_message_attachment_evidence_update_immutable BEFORE UPDATE ON operator_message_attachment_evidence
		BEGIN SELECT RAISE(ABORT,'operator message attachment evidence cannot be updated'); END;`,
	`CREATE TRIGGER trg_operator_message_attachment_evidence_delete_immutable BEFORE DELETE ON operator_message_attachment_evidence
		BEGIN SELECT RAISE(ABORT,'operator message attachment evidence cannot be deleted'); END;`,
	`CREATE TRIGGER trg_operator_steering_update_monotonic
		BEFORE UPDATE ON operator_steering_messages
		WHEN NOT (
			(OLD.status='pending' AND NEW.status IN ('committed','cancelled')
				AND NEW.id IS OLD.id AND NEW.run_id IS OLD.run_id AND NEW.session_id IS OLD.session_id
				AND NEW.sequence IS OLD.sequence AND NEW.content IS OLD.content
				AND NEW.content_sha256 IS OLD.content_sha256 AND NEW.revision IS OLD.revision
				AND NEW.original_content IS OLD.original_content
				AND NEW.original_content_sha256 IS OLD.original_content_sha256
				AND NEW.edited_at IS OLD.edited_at AND NEW.image_count IS OLD.image_count
				AND NEW.attachment_count IS OLD.attachment_count
				AND NEW.requested_by IS OLD.requested_by AND NEW.created_at IS OLD.created_at
				AND (NEW.status='committed' OR EXISTS (
					SELECT 1 FROM operator_steering_cancellations cancellation
					WHERE cancellation.message_id=OLD.id AND cancellation.run_id=OLD.run_id
						AND cancellation.created_at=NEW.cancelled_at
						AND (cancellation.kind='run_terminal' OR EXISTS (
							SELECT 1 FROM operator_steering_cancellation_operations operation
							WHERE operation.cancellation_id=cancellation.id
								AND operation.message_id=OLD.id AND operation.run_id=OLD.run_id)))))
			OR
			(OLD.status='pending' AND NEW.status='pending'
				AND NEW.id IS OLD.id AND NEW.run_id IS OLD.run_id AND NEW.session_id IS OLD.session_id
				AND NEW.sequence IS OLD.sequence AND NEW.revision=OLD.revision+1
				AND NEW.original_content IS OLD.original_content
				AND NEW.original_content_sha256 IS OLD.original_content_sha256
				AND NEW.image_count IS OLD.image_count AND NEW.attachment_count IS OLD.attachment_count
				AND NEW.requested_by IS OLD.requested_by AND NEW.created_at IS OLD.created_at
				AND NEW.session_message_id IS OLD.session_message_id
				AND NEW.committed_at IS OLD.committed_at AND NEW.cancelled_at IS OLD.cancelled_at
				AND NEW.edited_at IS NOT NULL
				AND NOT EXISTS (SELECT 1 FROM operator_steering_deliveries delivery WHERE delivery.message_id=OLD.id AND delivery.status='prepared')
				AND EXISTS (SELECT 1 FROM operator_steering_revisions revision
					WHERE revision.message_id=OLD.id AND revision.run_id=OLD.run_id
						AND revision.session_id=OLD.session_id AND revision.from_revision=OLD.revision
						AND revision.to_revision=NEW.revision AND revision.old_content_sha256=OLD.content_sha256
						AND revision.new_content=NEW.content AND revision.new_content_sha256=NEW.content_sha256
						AND revision.created_at=NEW.edited_at)))
		BEGIN SELECT RAISE(ABORT,'operator steering transition is invalid'); END;`,
}
