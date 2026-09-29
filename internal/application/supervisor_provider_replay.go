package application

import (
	"context"
	"errors"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
)

// Private continuation data is only loaded for this fenced native tool segment.
// It never becomes a session message, event payload, or public round DTO.
type supervisorProviderReplayStore interface {
	LoadSupervisorProviderReplay(context.Context, domain.SupervisorCheckpoint) (map[int]*llm.ProviderReplay, error)
}

type supervisorContextRecoveryStore interface {
	ClaimSupervisorContextRecovery(context.Context, domain.SupervisorCheckpoint, llm.ModelAttempt) (bool, error)
}

type supervisorContextRecoveryInputStore interface {
	CheckSupervisorContextRecoveryInput(context.Context, domain.SupervisorCheckpoint, llm.ModelAttempt) error
}

func (s *RunSupervisor) attachSupervisorProviderReplay(ctx context.Context, checkpoint domain.SupervisorCheckpoint,
	request llm.ChatRequest, rounds []domain.SupervisorToolRound,
) (llm.ChatRequest, error) {
	store, ok := s.store.(supervisorProviderReplayStore)
	if !ok || len(rounds) == 0 {
		return request, nil
	}
	states, err := store.LoadSupervisorProviderReplay(ctx, checkpoint)
	if err != nil {
		return llm.ChatRequest{}, err
	}
	start := len(request.Messages) - 2*len(rounds)
	if start < 0 {
		return llm.ChatRequest{}, errors.New("provider replay has no native tool segment")
	}
	for i, round := range rounds {
		replay := states[round.Round]
		if replay == nil {
			continue
		}
		message := &request.Messages[start+2*i]
		if message.Role != "assistant" {
			return llm.ChatRequest{}, errors.New("provider replay has no assistant tool batch")
		}
		if err := replay.ValidateToolCalls(message.ToolCalls); err != nil {
			return llm.ChatRequest{}, err
		}
		message.Replay = replay.Clone()
		message.Content = replay.AssistantText()
	}
	return request, nil
}

func (s *RunSupervisor) requestWithSupervisorToolRounds(ctx context.Context, checkpoint domain.SupervisorCheckpoint,
	base llm.ChatRequest, rounds []domain.SupervisorToolRound,
) (llm.ChatRequest, error) {
	request, err := supervisorRequestWithToolRounds(base, rounds)
	if err != nil {
		return llm.ChatRequest{}, err
	}
	return s.attachSupervisorProviderReplay(ctx, checkpoint, request, rounds)
}
