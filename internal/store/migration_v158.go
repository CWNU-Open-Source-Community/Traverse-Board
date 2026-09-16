package store

import "strings"

var workspaceImageStatements = append(imageOperatorSteeringMigration(), []string{
	`ALTER TABLE run_supervisor_checkpoints ADD COLUMN pending_image_count INTEGER NOT NULL DEFAULT 0 CHECK(pending_image_count BETWEEN 0 AND 4);`,
	`CREATE TABLE workspace_image_attachments (
		id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL REFERENCES workspaces(id),
		operation_digest TEXT NOT NULL UNIQUE, request_fingerprint TEXT NOT NULL,
		sha256 TEXT NOT NULL CHECK(length(sha256)=64), mime_type TEXT NOT NULL CHECK(mime_type IN ('image/png','image/jpeg','image/webp')),
		byte_size INTEGER NOT NULL CHECK(byte_size BETWEEN 1 AND 5242880),
		width INTEGER NOT NULL CHECK(width BETWEEN 1 AND 8192), height INTEGER NOT NULL CHECK(height BETWEEN 1 AND 8192),
		name TEXT NOT NULL, content BLOB NOT NULL, created_at TEXT NOT NULL,
		CHECK(length(content)=byte_size AND width*height<=16777216)
	);`,
	`CREATE TRIGGER trg_workspace_image_immutable BEFORE UPDATE ON workspace_image_attachments BEGIN SELECT RAISE(ABORT, 'Workspace image is immutable'); END;`,
	`CREATE TRIGGER trg_workspace_image_nodelete BEFORE DELETE ON workspace_image_attachments BEGIN SELECT RAISE(ABORT, 'Workspace image is immutable'); END;`,
	`ALTER TABLE thread_message_intents ADD COLUMN images_json TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(images_json) AND json_type(images_json)='array' AND json_array_length(images_json)<=4);`,
	`CREATE TRIGGER trg_thread_message_images_immutable BEFORE UPDATE OF images_json ON thread_message_intents WHEN NEW.images_json <> OLD.images_json BEGIN SELECT RAISE(ABORT, 'Thread images are immutable'); END;`,
	`CREATE TABLE thread_message_images (
		message_id TEXT NOT NULL REFERENCES operator_steering_messages(id),
		ordinal INTEGER NOT NULL CHECK(ordinal BETWEEN 0 AND 3),
		image_id TEXT NOT NULL REFERENCES workspace_image_attachments(id),
		PRIMARY KEY(message_id,ordinal), UNIQUE(message_id,image_id)
	);`,
	`CREATE TRIGGER trg_thread_image_binding BEFORE INSERT ON thread_message_images WHEN NOT EXISTS (
		SELECT 1 FROM operator_steering_messages m JOIN thread_runs tr ON tr.run_id=m.run_id
		JOIN threads t ON t.id=tr.thread_id JOIN workspace_image_attachments i ON i.workspace_id=t.workspace_id
		WHERE m.id=NEW.message_id AND i.id=NEW.image_id AND m.status='pending' AND NEW.ordinal<m.image_count
	) BEGIN SELECT RAISE(ABORT, 'Thread image workspace binding is invalid'); END;`,
	`CREATE TRIGGER trg_thread_image_immutable BEFORE UPDATE ON thread_message_images BEGIN SELECT RAISE(ABORT, 'Thread image binding is immutable'); END;`,
	`CREATE TRIGGER trg_thread_image_nodelete BEFORE DELETE ON thread_message_images BEGIN SELECT RAISE(ABORT, 'Thread image binding is immutable'); END;`,
}...)

// Keep all pre-image rows and external foreign keys unchanged. SQLite's legacy
// rename mode prevents rewriting references to the temporary old table; only
// its own index/triggers are recreated from their latest existing definitions.
func imageOperatorSteeringMigration() []string {
	create := strings.Replace(operatorSteeringStatements[0], "content TEXT NOT NULL,", "content TEXT NOT NULL, image_count INTEGER NOT NULL DEFAULT 0 CHECK(image_count BETWEEN 0 AND 4),", 1)
	create = strings.Replace(create, "BETWEEN 1 AND 16384", "BETWEEN 0 AND 16384 AND (length(CAST(content AS BLOB)) > 0 OR image_count > 0)", 1)
	statements := []string{`PRAGMA legacy_alter_table=ON;`, `ALTER TABLE operator_steering_messages RENAME TO operator_steering_messages_v157;`, create,
		`INSERT INTO operator_steering_messages (id,run_id,session_id,sequence,status,content,content_sha256,requested_by,session_message_id,created_at,committed_at,cancelled_at) SELECT id,run_id,session_id,sequence,status,content,content_sha256,requested_by,session_message_id,created_at,committed_at,cancelled_at FROM operator_steering_messages_v157;`,
		`DROP TABLE operator_steering_messages_v157;`, operatorSteeringStatements[1]}
	latest := map[string]string{}
	names := []string{"trg_operator_steering_insert_binding", "trg_operator_steering_update_monotonic", "trg_operator_steering_commit_binding", "trg_operator_steering_delete_immutable"}
	for _, group := range [][]string{operatorSteeringStatements, operatorSteeringControlStatements, threadSuccessionStatements} {
		for _, statement := range group {
			for _, name := range names {
				if strings.HasPrefix(strings.TrimSpace(statement), "CREATE TRIGGER "+name+"\n") || strings.HasPrefix(strings.TrimSpace(statement), "CREATE TRIGGER "+name+"\r\n") {
					latest[name] = statement
				}
			}
		}
	}
	for _, name := range names {
		statement := latest[name]
		if statement == "" {
			panic("missing image migration steering trigger: " + name)
		}
		if name == "trg_operator_steering_update_monotonic" {
			statement = strings.Replace(statement, "WHEN NEW.id IS NOT OLD.id", "WHEN NEW.image_count IS NOT OLD.image_count OR NEW.id IS NOT OLD.id", 1)
		}
		statements = append(statements, statement)
	}
	statements = append(statements, `DROP TRIGGER trg_session_message_provenance_insert;`)
	for _, statement := range contextProvenanceStatements {
		if strings.HasPrefix(strings.TrimSpace(statement), "CREATE TRIGGER trg_session_message_provenance_insert") {
			statements = append(statements, strings.Replace(statement, "'workspace_file', 'workspace_listing'", "'workspace_image', 'workspace_file', 'workspace_listing'", 1))
		}
	}
	return append(statements, `PRAGMA legacy_alter_table=OFF;`)
}
