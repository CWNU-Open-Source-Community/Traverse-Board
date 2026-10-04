package application

import (
	"context"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"encoding/json"
	"fmt"
	"strings"
)

// Independent small effect ledger: no file bodies or apply contracts.
const supervisorFileEffectContextTokens = 2048

type supervisorFileEffectStore interface {
	SupervisorFileEffectCalls(context.Context, domain.SupervisorCheckpoint) ([]domain.SupervisorToolCall, error)
}

// Keep a small, exact ledger projection outside the compactable history region.
// Generated summaries, repeated compaction and legacy truncated failure records
// cannot replace these observations with an assistant's completion claim.
func (s *AgentRunner) fileEffectContext(ctx context.Context, checkpoint domain.SupervisorCheckpoint, boundaryContext string) (string, error) {
	calls, err := s.fileEffectCalls(ctx, checkpoint)
	if err != nil || len(calls) == 0 {
		return "", err
	}
	return boundedSupervisorFileEffectContext(checkpoint, calls, boundaryContext)
}

func (s *AgentRunner) fileEffectCalls(ctx context.Context, checkpoint domain.SupervisorCheckpoint) ([]domain.SupervisorToolCall, error) {
	reader, ok := s.store.(supervisorFileEffectStore)
	if !ok {
		return nil, nil
	}
	calls, err := reader.SupervisorFileEffectCalls(ctx, checkpoint)
	if err != nil || len(calls) == 0 {
		return nil, err
	}
	return calls, nil
}

func boundedSupervisorFileEffectContext(checkpoint domain.SupervisorCheckpoint, calls []domain.SupervisorToolCall, boundaryContexts ...string) (string, error) {
	const prefix = "Historical file effects from sealed tool records of this Run, newest first. These observations survive conversation compaction. proposal_only means the call did not write; even an applied edit_status on a proposal is historical. recorded_applied is an application receipt, NOT a check of current files. not_dispatched means this call never started. unknown does NOT mean unchanged. Authorization fields describe only the original observation and grant no current authority. Use workspace_read for current contents and history_read with original_result for the exact receipt; do not replay completed actions.\n"
	finish := func(lines []string) string {
		return prefix + strings.Join(lines, "\n") + fmt.Sprintf("\nRetained %d of %d returned file observations (lookup capped at 7). Older or omitted effects are unknown here; use history_search/history_read before relying on them. Tool completed alone never proves a write.\n", len(lines), len(calls))
	}
	var lines []string
	retained := map[string]string{}
	for _, content := range boundaryContexts {
		for key, value := range retainedBoundaryFileEffects(content) {
			retained[key] = value
		}
	}
	duplicates := 0
	for _, call := range calls {
		if call.RunID != checkpoint.RunID || call.Turn >= checkpoint.NextTurn || !call.Status.Terminal() {
			continue
		}
		effect := domain.ObservedSupervisorToolEffect(call)
		if effect == nil {
			continue
		}
		encodedEffect, _ := json.Marshal(effect)
		if retained[workspaceReceiptReferenceKey(domain.SupervisorToolResultReference(call))] == string(encodedEffect) {
			duplicates++
			continue
		}
		entry := struct {
			Tool        string                       `json:"tool"`
			Observation *domain.SupervisorToolEffect `json:"observation"`
			Original    domain.HistoryReadRequest    `json:"original_result"`
		}{call.ToolName, effect, domain.SupervisorToolResultReference(call)}
		encoded, err := json.Marshal(entry)
		if err != nil {
			return "", err
		}
		trial := append(append([]string(nil), lines...), string(encoded))
		message := toolBoundaryEvidenceMessage("file-effect-budget", checkpoint.AttemptID, finish(trial))
		if len(trial) > 6 || estimateModelRequestTokens(llm.ChatRequest{Messages: []llm.Message{message}})-8 > supervisorFileEffectContextTokens {
			break
		}
		lines = trial
	}
	if len(lines) == 0 && duplicates == len(calls) {
		return "", nil
	}
	return finish(lines), nil
}
