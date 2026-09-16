package application

import (
	"context"
	"cyberagent-workbench/internal/domain"
)

func (s *ThreadService) projectMessageContinuation(ctx context.Context, request SubmitThreadMessageRequest, result *SubmitThreadMessageResult) error {
	if reader, ok := s.store.(interface {
		ThreadMessageContinuation(context.Context, domain.ThreadMessageIntentRequest, string) (string, bool, error)
	}); ok {
		predecessor, created, err := reader.ThreadMessageContinuation(ctx, threadMessageIntentRequest(request), result.Message.ID)
		if err != nil {
			return err
		}
		result.PredecessorRunID, result.SuccessorCreated = predecessor, created
		return nil
	}
	// Compatibility for older embedders without the durable intent reader.
	bindings, err := s.store.ListThreadRuns(ctx, request.ThreadID)
	if err != nil {
		return err
	}
	result.PredecessorRunID, result.SuccessorCreated = "", false
	for _, binding := range bindings {
		if binding.RunID == result.Run.ID && binding.PredecessorRunID != "" && result.Message.Sequence == 1 {
			result.PredecessorRunID, result.SuccessorCreated = binding.PredecessorRunID, true
			break
		}
	}
	return nil
}
