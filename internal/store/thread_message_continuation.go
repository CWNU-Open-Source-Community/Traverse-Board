package store

import (
	"context"
	"database/sql"
	"errors"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
)

// ThreadMessageContinuation reads the creation receipt for this exact request.
// The Run's ancestry alone does not mean every message created that Run.
func (s *SQLiteStore) ThreadMessageContinuation(ctx context.Context, request domain.ThreadMessageIntentRequest, messageID string) (string, bool, error) {
	key, fingerprint, _, err := threadMessageIntentIdentity(request)
	if err != nil {
		return "", false, err
	}
	var predecessor, createdKey, createdFingerprint string
	var sequence int64
	var eventID sql.NullInt64
	err = s.db.QueryRowContext(ctx, `SELECT COALESCE(binding.predecessor_run_id,''),message.sequence,event.id,
  COALESCE(json_extract(event.payload_json,'$.message_operation_key_digest'),''),
  COALESCE(json_extract(event.payload_json,'$.message_request_fingerprint'),'')
 FROM thread_message_intents intent JOIN operator_steering_messages message ON message.id=intent.message_id AND message.run_id=intent.run_id
 JOIN thread_runs binding ON binding.thread_id=intent.thread_id AND binding.run_id=message.run_id AND binding.session_id=message.session_id
 LEFT JOIN thread_events event ON event.thread_id=binding.thread_id AND event.run_id=binding.run_id
  AND event.type='thread.run_successor_created' AND event.source='thread_continuation'
  AND json_extract(event.payload_json,'$.successor_run_id')=binding.run_id
  AND json_extract(event.payload_json,'$.predecessor_run_id')=binding.predecessor_run_id
 WHERE intent.operation_key_digest=? AND intent.request_fingerprint=? AND intent.thread_id=? AND intent.rejected=0 AND message.id=?`,
		key, fingerprint, request.ThreadID, messageID).Scan(&predecessor, &sequence, &eventID, &createdKey, &createdFingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, apperror.New(apperror.CodeConflict, "Thread continuation does not match its durable message intent")
	}
	if err != nil {
		return "", false, err
	}
	if predecessor == "" {
		return "", false, nil
	}
	if !eventID.Valid {
		return "", false, apperror.New(apperror.CodeConflict, "Thread successor creation receipt is missing")
	}
	if createdKey != "" || createdFingerprint != "" {
		if createdKey == "" || createdFingerprint == "" {
			return "", false, apperror.New(apperror.CodeConflict, "Thread successor creation request is incomplete")
		}
		if createdKey != key {
			return "", false, nil
		}
		if createdFingerprint != fingerprint {
			return "", false, apperror.New(apperror.CodeConflict, "Thread successor creation request changed")
		}
		return predecessor, true, nil
	}
	// Existing receipts predate explicit request attribution. Preserve the old
	// first-message convention without changing any historical event or message.
	if sequence == 1 {
		return predecessor, true, nil
	}
	return "", false, nil
}
