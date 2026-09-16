package application

import (
	"context"
	"encoding/json"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
)

type supervisorPendingInstructionStore interface {
	PendingOperatorInstructions(context.Context, domain.SupervisorCheckpoint) ([]domain.OperatorSteeringMessage, error)
}

// Keep accepted corrections in the protected current-input portion of the
// model context. They are user instructions, not tool evidence or completed
// turns. The normal aggregate context budget rejects an oversized input intact.
func supervisorInputWithPendingInstructions(ctx context.Context, store any,
	turn domain.SupervisorTurn, input string,
) (string, int, error) {
	reader, ok := store.(supervisorPendingInstructionStore)
	if !ok || !turn.OperatorSteering {
		return input, 0, nil
	}
	messages, err := reader.PendingOperatorInstructions(ctx, turn.Checkpoint)
	if err != nil || len(messages) == 0 {
		return input, 0, err
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
			message.Sequence <= previous {
			return input, 0, apperror.New(apperror.CodeFailedPrecondition,
				"Pending user instruction identity changed before model delivery")
		}
		previous = message.Sequence
		bytes += len(message.Content)
		values = append(values, instruction{message.ID, message.Sequence, message.Content})
	}
	if bytes > domain.MaxPendingOperatorSteeringBytes || len(values) > domain.MaxPendingOperatorSteering {
		return input, 0, apperror.New(apperror.CodeResourceExhausted,
			"Pending user instructions cannot be included completely")
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return input, 0, err
	}
	return input + "\n\nLater user instructions already accepted in this conversation, in submission order:\n" +
		string(encoded) + "\nApply these later corrections and scope restrictions before acting on the earlier request. " +
		"Their queue entries are still pending; seeing them here does not mean their work has completed. " +
		"Do not repeat completed actions or perform an action superseded by a later instruction. " +
		"All actions remain subject to the current tool permissions.", len(values), nil
}
