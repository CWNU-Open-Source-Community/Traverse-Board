package store

import "strings"

var workspaceFileAttachmentStatements = append(fileAttachmentSteeringMigration(), []string{
	`ALTER TABLE run_supervisor_checkpoints ADD COLUMN pending_attachment_count INTEGER NOT NULL DEFAULT 0 CHECK(pending_attachment_count BETWEEN 0 AND 4);`,
	`CREATE TABLE workspace_file_attachments (
		id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL REFERENCES workspaces(id),
		operation_digest TEXT NOT NULL UNIQUE, request_fingerprint TEXT NOT NULL,
		sha256 TEXT NOT NULL CHECK(length(sha256)=64), mime_type TEXT NOT NULL, name TEXT NOT NULL,
		byte_size INTEGER NOT NULL CHECK(byte_size BETWEEN 0 AND 5242880), content BLOB NOT NULL,
		readability TEXT NOT NULL CHECK(readability IN ('text','partial_text','stored_only')),
		text_content TEXT NOT NULL, text_sha256 TEXT NOT NULL, text_bytes INTEGER NOT NULL CHECK(text_bytes BETWEEN 0 AND 65536),
		redacted INTEGER NOT NULL CHECK(redacted IN (0,1)), reason TEXT NOT NULL, created_at TEXT NOT NULL,
		CHECK(length(content)=byte_size AND length(CAST(text_content AS BLOB))=text_bytes),
		CHECK((readability='stored_only' AND text_bytes=0 AND text_sha256='') OR (readability<>'stored_only' AND length(text_sha256)=64))
	);`,
	`CREATE TRIGGER trg_workspace_file_attachment_immutable BEFORE UPDATE ON workspace_file_attachments BEGIN SELECT RAISE(ABORT,'Uploaded file is immutable'); END;`,
	`CREATE TRIGGER trg_workspace_file_attachment_nodelete BEFORE DELETE ON workspace_file_attachments BEGIN SELECT RAISE(ABORT,'Uploaded file is immutable'); END;`,
	`ALTER TABLE thread_message_intents ADD COLUMN attachments_json TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(attachments_json) AND json_type(attachments_json)='array' AND json_array_length(attachments_json)<=4);`,
	`CREATE TRIGGER trg_thread_message_attachments_immutable BEFORE UPDATE OF attachments_json ON thread_message_intents WHEN NEW.attachments_json<>OLD.attachments_json BEGIN SELECT RAISE(ABORT,'Thread uploaded files are immutable'); END;`,
	`CREATE TABLE thread_message_attachments (
		message_id TEXT NOT NULL REFERENCES operator_steering_messages(id),
		ordinal INTEGER NOT NULL CHECK(ordinal BETWEEN 0 AND 3),
		attachment_id TEXT NOT NULL REFERENCES workspace_file_attachments(id),
		PRIMARY KEY(message_id,ordinal),UNIQUE(message_id,attachment_id)
	);`,
	`CREATE TRIGGER trg_thread_attachment_binding BEFORE INSERT ON thread_message_attachments WHEN NOT EXISTS (
		SELECT 1 FROM operator_steering_messages m JOIN thread_runs tr ON tr.run_id=m.run_id
		JOIN threads t ON t.id=tr.thread_id JOIN workspace_file_attachments f ON f.workspace_id=t.workspace_id
		WHERE m.id=NEW.message_id AND f.id=NEW.attachment_id AND m.status='pending' AND NEW.ordinal<m.attachment_count
	) BEGIN SELECT RAISE(ABORT,'Thread uploaded file workspace binding is invalid'); END;`,
	`CREATE TRIGGER trg_thread_attachment_immutable BEFORE UPDATE ON thread_message_attachments BEGIN SELECT RAISE(ABORT,'Thread uploaded file binding is immutable'); END;`,
	`CREATE TRIGGER trg_thread_attachment_nodelete BEFORE DELETE ON thread_message_attachments BEGIN SELECT RAISE(ABORT,'Thread uploaded file binding is immutable'); END;`,
}...)

// Preserve every v159 row and foreign key while extending the existing empty
// user-input constraint. Counts remain separate: a document is never an image.
func fileAttachmentSteeringMigration() []string {
	previous := imageOperatorSteeringMigration()
	create := previous[2]
	create = strings.Replace(create, "content TEXT NOT NULL,", "content TEXT NOT NULL, attachment_count INTEGER NOT NULL DEFAULT 0 CHECK(attachment_count BETWEEN 0 AND 4),", 1)
	create = strings.Replace(create, "OR image_count > 0)", "OR image_count > 0 OR attachment_count > 0)", 1)
	statements := []string{`PRAGMA legacy_alter_table=ON;`, `ALTER TABLE operator_steering_messages RENAME TO operator_steering_messages_v159;`, create,
		`INSERT INTO operator_steering_messages (id,run_id,session_id,sequence,status,content,content_sha256,requested_by,session_message_id,created_at,committed_at,cancelled_at,image_count) SELECT id,run_id,session_id,sequence,status,content,content_sha256,requested_by,session_message_id,created_at,committed_at,cancelled_at,image_count FROM operator_steering_messages_v159;`,
		`DROP TABLE operator_steering_messages_v159;`, operatorSteeringStatements[1]}
	for _, statement := range previous {
		trim := strings.TrimSpace(statement)
		if strings.HasPrefix(trim, "CREATE TRIGGER trg_operator_steering_") {
			if strings.HasPrefix(trim, "CREATE TRIGGER trg_operator_steering_update_monotonic") {
				statement = strings.Replace(statement, "WHEN NEW.image_count", "WHEN NEW.attachment_count IS NOT OLD.attachment_count OR NEW.image_count", 1)
			}
			statements = append(statements, statement)
		}
		if strings.HasPrefix(trim, "CREATE TRIGGER trg_session_message_provenance_insert") {
			statements = append(statements, `DROP TRIGGER trg_session_message_provenance_insert;`, strings.Replace(statement, "'workspace_image',", "'uploaded_file', 'workspace_image',", 1))
		}
	}
	return append(statements, `PRAGMA legacy_alter_table=OFF;`)
}
