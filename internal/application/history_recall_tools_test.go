package application

import (
	"encoding/json"
	"testing"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/toolgateway"
)

func TestHistoryRecallToolsAdvertisedOnlyWhenAvailable(t *testing.T) {
	for _, phase := range []domain.ExecutionPhase{domain.ExecutionPhasePlan, domain.ExecutionPhaseDeliver} {
		for _, available := range []bool{false, true} {
			options := supervisorToolOptions{HistoryRecall: available}
			specs := supervisorStructuredToolSpecs(domain.ExecutionSurfaceCode, phase, domain.RunExecutionPermissionConservative, false, false, options)
			count := 0
			for _, spec := range specs {
				if toolgateway.IsHistoryRecallTool(toolgateway.ToolName(spec.Name)) {
					count++
				}
			}
			if (available && count != 2) || (!available && count != 0) {
				t.Fatalf("phase=%s available=%t count=%d", phase, available, count)
			}
			_, err := prepareSupervisorToolCalls([]llm.ToolCall{{ID: "recall-call", Name: "history_search", Arguments: json.RawMessage(`{"query":"原始限制"}`)}},
				"run-current", 1, 1, domain.ExecutionSurfaceCode, phase, domain.RunExecutionPermissionConservative, false, false, options)
			if (err == nil) != available {
				t.Fatalf("available=%t accepted=%t: %v", available, err == nil, err)
			}
		}
	}
}
