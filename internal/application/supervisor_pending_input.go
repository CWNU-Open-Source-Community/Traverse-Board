package application

import (
	"context"
	"encoding/json"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
)

type supervisorPendingInstructionStore interface {
	ClaimSupervisorMidTurnSteering(context.Context, domain.SupervisorCheckpoint) ([]domain.OperatorSteeringMessage, int64, error)
}

type supervisorCurrentSteeringReader interface {
	CurrentSupervisorMidTurnSequence(context.Context, domain.SupervisorCheckpoint) (int64, error)
}

func supervisorCurrentSteeringSequence(ctx context.Context, store any,
	checkpoint domain.SupervisorCheckpoint,
) (int64, error) {
	reader, ok := store.(supervisorCurrentSteeringReader)
	if !ok {
		return 0, nil
	}
	return reader.CurrentSupervisorMidTurnSequence(ctx, checkpoint)
}

// Keep accepted corrections in the protected current-input portion of the
// model context. They are user instructions, not tool evidence or completed
// turns. The normal aggregate context budget rejects an oversized input intact.
func supervisorInputWithPendingInstructions(ctx context.Context, store any,
	turn domain.SupervisorTurn, input string,
) (string, int, int64, error) {
	reader, ok := store.(supervisorPendingInstructionStore)
	if !ok {
		return input, 0, 0, nil
	}
	messages, sequence, err := reader.ClaimSupervisorMidTurnSteering(ctx, turn.Checkpoint)
	if err != nil || len(messages) == 0 {
		return input, 0, sequence, err
	}
	type instruction struct {
		MessageID string `json:"message_id"`
		Sequence  int64  `json:"sequence"`
		Content   string `json:"content"`
	}
	values := make([]instruction, 0, len(messages))
	var bytes int
	var previous int64
	for _, message := range messages {
		if err := message.Validate(); err != nil || message.RunID != turn.Run.ID ||
			message.SessionID != turn.Run.SessionID || message.Status != domain.OperatorSteeringPending ||
			message.DeliveryMode != domain.OperatorSteeringCurrentTurn ||
			message.TargetAttemptID != turn.Checkpoint.AttemptID || !message.Prepared ||
			message.Sequence <= previous {
			return input, 0, 0, apperror.New(apperror.CodeFailedPrecondition,
				"Pending user instruction identity changed before model delivery")
		}
		previous = message.Sequence
		bytes += len(message.Content)
		values = append(values, instruction{message.ID, message.Sequence, message.Content})
	}
	if bytes > domain.MaxPendingOperatorSteeringBytes || len(values) > domain.MaxPendingOperatorSteering {
		return input, 0, 0, apperror.New(apperror.CodeResourceExhausted,
			"Pending user instructions cannot be included completely")
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return input, 0, 0, err
	}
	return input + "\n\nUser corrections for the current task, in submission order:\n" +
		string(encoded) + "\nApply these later corrections and scope restrictions before acting on the earlier request. " +
		"These corrections are accepted for this executing task; their actions may not yet be complete. " +
		"Do not repeat completed actions or perform an action superseded by a later instruction. " +
		"All actions remain subject to the current tool permissions.", len(values), sequence, nil
}
