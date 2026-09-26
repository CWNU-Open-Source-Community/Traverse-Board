package store

import (
	"context"
	"errors"
	"testing"

	"cyberagent-workbench/internal/contextmgr"
	"cyberagent-workbench/internal/llm"
)

func TestSupervisorTruncatedCompactionRetainsFailedUsageOnce(t *testing.T) {
	st, turn, _ := newSupervisorCompactionTest(t)
	result, updated, err := st.CompactSupervisorContextGenerated(t.Context(), turn.Checkpoint, 2,
		supervisorSummaryGeneratorFunc(func(ctx context.Context, request contextmgr.SummaryGenerationRequest) (contextmgr.SummaryGenerationResponse, error) {
			attempt := compactionTestAttempt(t, st, turn.Checkpoint, request.SourceSHA256)
			attempt.Outcome, attempt.FailureReason, attempt.ErrorText = llm.OutcomePermanent, llm.ProviderFailureOutputLimit, "output truncated"
			usage := llm.Usage{InputTokens: 30, OutputTokens: 12, TotalTokens: 42}
			cp, err := st.RecordSupervisorCompactionFailedWithUsage(ctx, turn.Checkpoint, attempt, usage)
			if err != nil || cp.TotalTokens != 42 {
				t.Fatalf("failed usage not recorded: %+v %v", cp, err)
			}
			replay, err := st.RecordSupervisorCompactionFailedWithUsage(ctx, cp, attempt, usage)
			if err != nil || replay.TotalTokens != 42 {
				t.Fatalf("failed usage repeated: %+v %v", replay, err)
			}
			if _, err := st.RecordSupervisorCompactionFailedWithUsage(ctx, cp, attempt, llm.Usage{InputTokens: 31, OutputTokens: 12, TotalTokens: 43}); err == nil {
				t.Fatal("changed terminal usage accepted")
			}
			return contextmgr.SummaryGenerationResponse{}, errors.New("generation_provider_failure: output truncated")
		}))
	if err != nil || !result.Compacted || result.Generated || updated.TotalTokens != 42 {
		t.Fatalf("fallback/usage mismatch result=%+v checkpoint=%+v err=%v", result, updated, err)
	}
}
