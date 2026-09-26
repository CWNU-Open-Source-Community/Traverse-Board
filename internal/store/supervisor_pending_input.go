package store

import (
	"context"
	"cyberagent-workbench/internal/domain"
)

// PendingOperatorInstructions is retained for older store callers. It no
// longer projects next-turn queue entries into the executing model request.
func (s *SQLiteStore) PendingOperatorInstructions(ctx context.Context,
	checkpoint domain.SupervisorCheckpoint,
) ([]domain.OperatorSteeringMessage, error) {
	messages, _, err := s.ClaimSupervisorMidTurnSteering(ctx, checkpoint)
	return messages, err
}
