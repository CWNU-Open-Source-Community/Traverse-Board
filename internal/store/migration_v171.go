package store

// A correction remains an operator message. Its exact active Supervisor
// attempt is separate from the one-original-message delivery ledger.
var midTurnSteeringStatements = []string{
	`ALTER TABLE operator_steering_messages ADD COLUMN delivery_mode TEXT NOT NULL DEFAULT 'next_turn'
		CHECK(delivery_mode IN ('next_turn','steer'));`,
	`ALTER TABLE operator_steering_messages ADD COLUMN target_attempt_id TEXT NOT NULL DEFAULT '';`,
	`CREATE TABLE operator_steering_midturn_claims (
		message_id TEXT PRIMARY KEY REFERENCES operator_steering_messages(id) ON DELETE RESTRICT,
		run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE RESTRICT,
		attempt_id TEXT NOT NULL,
		claimed_at TEXT NOT NULL,
		CHECK(length(attempt_id) BETWEEN 1 AND 256 AND attempt_id=trim(attempt_id))
	);`,
	`CREATE INDEX idx_operator_steering_midturn_claims_attempt
		ON operator_steering_midturn_claims(run_id,attempt_id);`,
	`CREATE TRIGGER trg_operator_steering_midturn_binding
		BEFORE INSERT ON operator_steering_messages
		WHEN NEW.delivery_mode='steer' AND (NEW.target_attempt_id='' OR NOT EXISTS (
			SELECT 1 FROM run_supervisor_checkpoints checkpoint
			JOIN runs run ON run.id=checkpoint.run_id
			WHERE run.id=NEW.run_id AND run.session_id=NEW.session_id
			AND run.status='running' AND checkpoint.phase='turn_started'
			AND checkpoint.attempt_id=NEW.target_attempt_id))
		BEGIN SELECT RAISE(ABORT,'midturn steering admission binding is invalid'); END;`,
	`CREATE TRIGGER trg_operator_steering_nextturn_binding
		BEFORE INSERT ON operator_steering_messages
		WHEN NEW.delivery_mode='next_turn' AND NEW.target_attempt_id!=''
		BEGIN SELECT RAISE(ABORT,'next-turn message cannot target a Supervisor attempt'); END;`,
	`CREATE TRIGGER trg_operator_steering_midturn_claim_insert
		BEFORE INSERT ON operator_steering_midturn_claims
		WHEN NOT EXISTS (SELECT 1 FROM operator_steering_messages message
			JOIN run_supervisor_checkpoints checkpoint ON checkpoint.run_id=message.run_id
			WHERE message.id=NEW.message_id AND message.run_id=NEW.run_id
			AND message.delivery_mode='steer' AND message.target_attempt_id=NEW.attempt_id
			AND message.status='pending' AND checkpoint.phase='turn_started'
			AND checkpoint.attempt_id=NEW.attempt_id)
		BEGIN SELECT RAISE(ABORT,'midturn steering claim binding is invalid'); END;`,
	`CREATE TRIGGER trg_operator_steering_midturn_claim_immutable
		BEFORE UPDATE ON operator_steering_midturn_claims
		BEGIN SELECT RAISE(ABORT,'midturn steering claim is immutable'); END;`,
	`CREATE TRIGGER trg_operator_steering_midturn_claim_delete
		BEFORE DELETE ON operator_steering_midturn_claims
		BEGIN SELECT RAISE(ABORT,'midturn steering claim cannot be deleted'); END;`,
}
