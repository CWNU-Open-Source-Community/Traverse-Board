package store

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/contextmgr"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/session"
)

type supervisorCompactionReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type supervisorCompactionSnapshot struct {
	Checkpoint            domain.SupervisorCheckpoint
	Lease                 domain.RunExecutionLease
	LeaseSequence         int64
	SessionID             string
	WorkspaceID           string
	RunConfigJSON         string
	MissionGoal           string
	OperatorInputSequence int64
	Summary               contextmgr.Summary
	HasSummary            bool
	Messages              []session.Message
}

func (s supervisorCompactionSnapshot) sameSources(other supervisorCompactionSnapshot) bool {
	return s.Checkpoint == other.Checkpoint && sameRunExecutionLease(s.Lease, other.Lease) &&
		s.LeaseSequence == other.LeaseSequence && s.SessionID == other.SessionID && s.WorkspaceID == other.WorkspaceID &&
		s.HasSummary == other.HasSummary && s.Summary == other.Summary && reflect.DeepEqual(s.Messages, other.Messages) &&
		s.RunConfigJSON == other.RunConfigJSON && s.MissionGoal == other.MissionGoal && s.OperatorInputSequence == other.OperatorInputSequence
}

func (s supervisorCompactionSnapshot) contextMessages() []contextmgr.Message {
	history := make([]contextmgr.Message, 0, len(s.Messages))
	for _, message := range s.Messages {
		// scanSessionMessage validated the original body, digest and provenance.
		// Avoid spending excerpt space on a second serialized evidence envelope.
		history = append(history, contextmgr.Message{Role: message.Role, Content: message.Content,
			CreatedAt: message.CreatedAt, SourceMessageID: message.ID,
			SourceKind: message.Provenance.SourceKind, SourceRef: message.Provenance.SourceRef,
			ContentSHA256:         message.Provenance.ContentSHA256,
			InstructionAuthorized: message.Provenance.InstructionAuthorized})
	}
	return history
}

// The same read is used in a short deferred snapshot and again in the commit
// transaction. Neither phase invokes the candidate strategy. Lease renewal is
// allowed; expiration, release, takeover and changed input are rejected.
func readSupervisorCompactionSnapshot(ctx context.Context, reader supervisorCompactionReader,
	checkpoint domain.SupervisorCheckpoint,
) (supervisorCompactionSnapshot, error) {
	var snapshot supervisorCompactionSnapshot
	current, err := scanSupervisorCheckpoint(reader.QueryRowContext(ctx, supervisorCheckpointSelect, checkpoint.RunID))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return snapshot, err
	}
	if errors.Is(err, sql.ErrNoRows) || current.Phase != checkpoint.Phase || current.NextTurn != checkpoint.NextTurn ||
		current.AttemptID != checkpoint.AttemptID || current.PendingInput != checkpoint.PendingInput ||
		current.PendingImageCount != checkpoint.PendingImageCount || current.PendingAttachmentCount != checkpoint.PendingAttachmentCount ||
		current.RepairPhase != checkpoint.RepairPhase || current.LeaseID != checkpoint.LeaseID ||
		current.LeaseGeneration != checkpoint.LeaseGeneration || checkpoint.LeaseID == "" || checkpoint.LeaseGeneration <= 0 {
		return snapshot, apperror.New(apperror.CodeConflict, "Supervisor context compaction checkpoint changed")
	}
	snapshot.Checkpoint = current
	snapshot.Lease, err = scanRunExecutionLease(reader.QueryRowContext(ctx, runExecutionLeaseSelect, checkpoint.RunID))
	if err != nil {
		return snapshot, err
	}
	if snapshot.Lease.LeaseID != checkpoint.LeaseID || snapshot.Lease.Generation != checkpoint.LeaseGeneration ||
		!snapshot.Lease.ActiveAt(time.Now().UTC()) {
		return snapshot, apperror.New(apperror.CodeConflict, "Supervisor context compaction execution lease changed")
	}
	// A takeover fences unresolved model starts from older owners. Preserve
	// those gaps as history; only the current generation must have settled.
	if err := reader.QueryRowContext(ctx, `SELECT sequence FROM run_events WHERE run_id=?
		AND source='execution_lease_store' AND type IN (?,?)
		AND json_extract(payload_json,'$.generation')=? ORDER BY sequence DESC LIMIT 1`,
		checkpoint.RunID, events.RunExecutionLeaseAcquiredEvent, events.RunExecutionLeaseTakenOverEvent,
		checkpoint.LeaseGeneration).Scan(&snapshot.LeaseSequence); err != nil {
		return snapshot, apperror.Wrap(apperror.CodeConflict,
			"Supervisor context compaction lease acquisition is not recorded", err)
	}
	var activeModels int
	if err := reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_events started
		WHERE started.run_id=? AND started.type=? AND started.source='model_gateway'
		AND json_extract(started.payload_json,'$.turn')=?
		AND json_extract(started.payload_json,'$.attempt_id')=? AND started.sequence>?
		AND NOT EXISTS (SELECT 1 FROM run_events terminal WHERE terminal.run_id=started.run_id
			AND terminal.source=started.source AND terminal.subject_id=started.subject_id
			AND terminal.type IN (?,?))`, checkpoint.RunID, events.ModelStartedEvent,
		checkpoint.NextTurn, checkpoint.AttemptID, snapshot.LeaseSequence, events.ModelCompletedEvent, events.ModelFailedEvent).
		Scan(&activeModels); err != nil {
		return snapshot, err
	}
	if activeModels != 0 {
		return snapshot, apperror.New(apperror.CodeConflict, "Supervisor context compaction requires a settled model attempt")
	}
	// New input is durable before it becomes a Session message. Cancellation
	// and delivery changes also invalidate a candidate based on the old queue.
	if err := reader.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0) FROM run_events WHERE run_id=? AND type IN (?,?,?,?,?)`, checkpoint.RunID,
		events.OperatorSteeringQueuedEvent, events.OperatorSteeringPreparedEvent, events.OperatorSteeringCommittedEvent, events.OperatorSteeringSupersededEvent, events.OperatorSteeringCancelledEvent).Scan(&snapshot.OperatorInputSequence); err != nil {
		return snapshot, err
	}
	if err := reader.QueryRowContext(ctx, `SELECT run.session_id, mission.workspace_id, run.config_json, mission.goal
		FROM runs run JOIN missions mission ON mission.id=run.mission_id
		JOIN sessions linked ON linked.id=run.session_id AND linked.workspace_id=mission.workspace_id
		WHERE run.id=? AND run.status=?`, checkpoint.RunID, domain.RunRunning).
		Scan(&snapshot.SessionID, &snapshot.WorkspaceID, &snapshot.RunConfigJSON, &snapshot.MissionGoal); err != nil {
		return snapshot, apperror.Wrap(apperror.CodeFailedPrecondition,
			"context compaction requires the running Run's bound Session and Workspace", err)
	}
	rows, err := reader.QueryContext(ctx, `SELECT id, session_id, role, content, provenance_version,
		source_kind, source_ref, content_sha256, instruction_authorized, token_estimate,
		compacted, created_at FROM session_messages WHERE session_id=? AND compacted=0 ORDER BY id`, snapshot.SessionID)
	if err != nil {
		return snapshot, err
	}
	for rows.Next() {
		message, err := scanSessionMessage(rows)
		if err != nil {
			_ = rows.Close()
			return snapshot, err
		}
		snapshot.Messages = append(snapshot.Messages, message)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return snapshot, err
	}
	snapshot.Summary, snapshot.HasSummary, err = latestContextSummary(ctx, reader, snapshot.SessionID)
	return snapshot, err
}
