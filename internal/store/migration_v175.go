package store

import "strings"

func specialistTaskBriefStatements() []string {
	statements := []string{
		`CREATE TABLE specialist_task_briefs (
   agent_attempt_id TEXT PRIMARY KEY REFERENCES agent_attempts(id) ON DELETE CASCADE,
   run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
   agent_id TEXT NOT NULL,
   parent_agent_id TEXT NOT NULL,
   turn_number INTEGER NOT NULL CHECK(turn_number > 0),
   brief_json TEXT NOT NULL CHECK(json_valid(brief_json) AND length(CAST(brief_json AS BLOB)) BETWEEN 2 AND 131072),
   fingerprint TEXT NOT NULL CHECK(length(fingerprint)=64 AND fingerprint NOT GLOB '*[^0-9a-f]*'),
   prepared_at TEXT NOT NULL,
   FOREIGN KEY(run_id, agent_id) REFERENCES agent_nodes(run_id,id) ON DELETE CASCADE,
   FOREIGN KEY(run_id, parent_agent_id) REFERENCES agent_nodes(run_id,id) ON DELETE CASCADE,
   CHECK(json_extract(brief_json,'$.version')='specialist_task_brief.v1'),
   CHECK(json_extract(brief_json,'$.run_id')=run_id),
   CHECK(json_extract(brief_json,'$.agent_id')=agent_id),
   CHECK(json_extract(brief_json,'$.parent_agent_id')=parent_agent_id),
   CHECK(json_extract(brief_json,'$.fingerprint')=fingerprint)
  );`,
		`CREATE INDEX idx_specialist_task_brief_child ON specialist_task_briefs(run_id,agent_id,turn_number);`,
		`CREATE TRIGGER trg_specialist_task_brief_insert BEFORE INSERT ON specialist_task_briefs
   WHEN NOT EXISTS (
    SELECT 1 FROM agent_attempts a JOIN agent_nodes c ON c.id=a.agent_id
    JOIN agent_nodes p ON p.id=a.parent_agent_id JOIN runs r ON r.id=a.run_id
    JOIN run_execution_leases l ON l.run_id=a.run_id
    WHERE a.id=NEW.agent_attempt_id AND a.run_id=NEW.run_id AND a.agent_id=NEW.agent_id
     AND a.parent_agent_id=NEW.parent_agent_id AND a.turn_number=NEW.turn_number
     AND a.status='running' AND a.usage_recorded_at IS NULL AND r.status='running'
     AND c.role='specialist' AND c.status='running' AND c.active_attempt_id=a.id AND c.parent_id=p.id
     AND p.role='root' AND p.run_id=r.id AND p.status IN ('ready','running','waiting')
     AND l.status='active' AND l.lease_id=a.lease_id AND l.generation=a.lease_generation
     AND julianday(l.expires_at)>julianday('now'))
   BEGIN SELECT RAISE(ABORT,'Specialist task brief requires its active attempt and lease'); END;`,
		`CREATE TRIGGER trg_specialist_task_brief_immutable BEFORE UPDATE ON specialist_task_briefs
   BEGIN SELECT RAISE(ABORT,'Specialist task brief is immutable'); END;`,
		`CREATE TRIGGER trg_specialist_task_brief_delete BEFORE DELETE ON specialist_task_briefs
   WHEN EXISTS(SELECT 1 FROM runs WHERE id=OLD.run_id)
   BEGIN SELECT RAISE(ABORT,'Specialist task brief cannot be deleted while its Run exists'); END;`,
		`CREATE TRIGGER trg_specialist_instruction_source_immutable
   BEFORE UPDATE OF id,run_id,sender_agent_id,recipient_agent_id,sequence,kind,semantic,payload_json ON agent_messages
   WHEN json_valid(OLD.payload_json) AND json_extract(OLD.payload_json,'$.version') IN ('specialist_instruction.v1','specialist_instruction.v2')
   BEGIN SELECT RAISE(ABORT,'Specialist instruction source is immutable'); END;`,
		`CREATE TRIGGER trg_specialist_instruction_source_delete BEFORE DELETE ON agent_messages
   WHEN EXISTS(SELECT 1 FROM runs WHERE id=OLD.run_id) AND json_valid(OLD.payload_json)
    AND json_extract(OLD.payload_json,'$.version') IN ('specialist_instruction.v1','specialist_instruction.v2')
   BEGIN SELECT RAISE(ABORT,'Specialist instruction and retirement sources must be retained'); END;`,
		`DROP TRIGGER trg_specialist_context_delivery_insert;`,
		`DROP TRIGGER trg_specialist_context_delivery_commit;`,
	}
	// Forward replacement of only the live delivery guards. v27 bytes/checksum
	// remain unchanged, including the existing delegation application's v1 guard.
	oldPredicate := `AND json_extract(message.payload_json, '$.version') = 'specialist_instruction.v1'
				AND json_type(message.payload_json, '$.instruction') = 'text'
				AND length(trim(json_extract(message.payload_json, '$.instruction'))) BETWEEN 1 AND 1200
				AND (SELECT COUNT(*) FROM json_each(message.payload_json)) = 2
				AND NOT EXISTS (
					SELECT 1 FROM json_each(message.payload_json) field
					WHERE field.key NOT IN ('version', 'instruction')
				)`
	newPredicate := `AND (` + oldPredicate[4:] + ` OR (
    json_extract(message.payload_json,'$.version')='specialist_instruction.v2'
    AND json_type(message.payload_json,'$.instruction')='text'
    AND ((json_extract(message.payload_json,'$.operation')='append'
      AND length(trim(json_extract(message.payload_json,'$.instruction'))) BETWEEN 1 AND 1200
      AND (SELECT COUNT(*) FROM json_each(message.payload_json))=3
      AND NOT EXISTS(SELECT 1 FROM json_each(message.payload_json) WHERE key NOT IN ('version','instruction','operation')))
     OR (json_extract(message.payload_json,'$.operation') IN ('replace','withdraw')
      AND (SELECT COUNT(*) FROM json_each(message.payload_json))=5
      AND NOT EXISTS(SELECT 1 FROM json_each(message.payload_json) WHERE key NOT IN ('version','instruction','operation','target_message_id','target_payload_sha256'))
      AND json_type(message.payload_json,'$.target_message_id')='text'
      AND length(json_extract(message.payload_json,'$.target_message_id'))>0
      AND json_type(message.payload_json,'$.target_payload_sha256')='text'
      AND length(json_extract(message.payload_json,'$.target_payload_sha256'))=64
      AND json_extract(message.payload_json,'$.target_payload_sha256') NOT GLOB '*[^0-9a-f]*'
      AND ((json_extract(message.payload_json,'$.operation')='replace' AND length(trim(json_extract(message.payload_json,'$.instruction'))) BETWEEN 1 AND 1200)
       OR (json_extract(message.payload_json,'$.operation')='withdraw' AND json_extract(message.payload_json,'$.instruction')=''))
      AND EXISTS(SELECT 1 FROM agent_messages target WHERE target.id=json_extract(message.payload_json,'$.target_message_id')
       AND target.run_id=message.run_id AND target.sender_agent_id=message.sender_agent_id
       AND target.recipient_agent_id=message.recipient_agent_id AND target.sequence<message.sequence
       AND target.kind='instruction' AND target.semantic='message')))
   ))`
	for _, statement := range specialistContextDeliveryStatements {
		if strings.HasPrefix(statement, "CREATE TRIGGER trg_specialist_context_delivery_insert\n") ||
			strings.HasPrefix(statement, "CREATE TRIGGER trg_specialist_context_delivery_commit\n") {
			if !strings.Contains(statement, oldPredicate) {
				panic("v175 delivery predicate did not match its immutable v27 source")
			}
			statements = append(statements, strings.Replace(statement, oldPredicate, newPredicate, 1))
		}
	}
	return statements
}
