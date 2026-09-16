package application_test

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/pricing"
	"cyberagent-workbench/internal/store"
)

// Exercise the constructor used by Desktop/API, with no test-only budget
// injection. Each input uses the actual Thread submit/prepare/handoff path.
func TestThreadDefaultMonetaryGateAcrossTurnsReopenAndModelSwitch(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "thread-money.db")
	st := openHistoryRecallStore(t, path)
	t.Cleanup(func() { _ = st.Close() })
	run := createMonetaryThread(t, st)
	snapshot := importSupervisorPriceSnapshot(t, ctx, st)
	snapshot.ID = "thread-model-switch-prices"
	snapshot.Entries = append(snapshot.Entries, pricing.Entry{Provider: "selected-provider", Model: "selected-model",
		InputPerMillionMicros: 2000000, OutputPerMillionMicros: 4000000})
	snapshot.Fingerprint = pricing.Fingerprint(snapshot)
	if _, _, err := st.ImportPriceSnapshot(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	first := newHistoryProtocolProvider(t, "usage-test", "model")
	first.handler = monetaryThreadReply
	turns := historyRecallTurns(st, first)
	for i := 1; i <= 3; i++ {
		submitHistoryRecall(t, turns, run.ID, fmt.Sprintf("thread-money-turn-%04d", i), fmt.Sprintf("Continue the same task, correction %d", i))
		assertMonetaryThreadLedger(t, st, run.ID, int64(i)*8)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st = openHistoryRecallStore(t, path)
	turns = historyRecallTurns(st, first)
	// The exact prior request is replayed after reopening, without a new call.
	submitHistoryRecall(t, turns, run.ID, "thread-money-turn-0003", "Continue the same task, correction 3")
	assertMonetaryThreadLedger(t, st, run.ID, 24)
	if len(first.Requests()) != 3 {
		t.Fatal("reopen replay called the model again")
	}
	submitHistoryRecall(t, turns, run.ID, "thread-money-turn-0004", "Continue after reopening")
	assertMonetaryThreadLedger(t, st, run.ID, 32)

	registry := newMutableThreadModelRouteRegistry()
	if _, err := application.NewThreadModelRouteService(st, registry).Change(ctx, application.ChangeThreadModelRouteRequest{
		Version: domain.ThreadModelRouteControlProtocolVersion, ThreadID: domain.InitialThreadID(run.ID),
		Action: domain.ThreadModelRouteSelect, Provider: "selected-provider", Model: "selected-model",
		OperationKey: "money-switch-model", RequestedBy: "test_operator",
	}); err != nil {
		t.Fatal(err)
	}
	second := newHistoryProtocolProvider(t, "selected-provider", "selected-model")
	second.handler = monetaryThreadReply
	nextTurns := historyRecallTurns(st, second).WithModelRouteRegistry(registry)
	result, err := nextTurns.Execute(ctx, application.ExecuteThreadTurnRequest{
		Version: domain.ThreadMessageProtocolVersion, ThreadID: domain.InitialThreadID(run.ID),
		OperationKey: "money-after-model-switch", Content: "Continue with the selected model", RequestedBy: "test_operator",
	})
	assertHistoryRecallSettled(t, result, err)
	if !result.Submission.SuccessorCreated || result.Submission.Run.ID == run.ID {
		t.Fatal("model switch did not use the normal successor")
	}
	assertMonetaryThreadLedger(t, st, run.ID, 32)
	assertMonetaryThreadLedger(t, st, result.Submission.Run.ID, 16)
	if len(first.Requests()) != 4 || len(second.Requests()) != 1 {
		t.Fatal("unexpected model call count")
	}
	t.Log("four ordinary turns, exact replay after SQLite reopen, and one model-switch successor: settled 32 + 16 micro-USD")
}

func TestThreadDefaultMonetaryGateRejectsMissingPricesBeforeProvider(t *testing.T) {
	st := openHistoryRecallStore(t, filepath.Join(t.TempDir(), "missing-price.db"))
	defer st.Close()
	run := createMonetaryThread(t, st)
	p := newHistoryProtocolProvider(t, "usage-test", "model")
	p.handler = monetaryThreadReply
	result, err := historyRecallTurns(st, p).Execute(t.Context(), application.ExecuteThreadTurnRequest{
		Version: domain.ThreadMessageProtocolVersion, ThreadID: domain.InitialThreadID(run.ID),
		OperationKey: "money-missing-price", Content: "This must not call the model without prices", RequestedBy: "test_operator",
	})
	if len(p.Requests()) != 0 {
		t.Fatal("the default Handoff bypassed its configured monetary cap")
	}
	if err == nil && result.Execution != nil && result.Execution.Handoff.Result != nil &&
		result.Execution.Handoff.Result.Status == domain.RunExecutionHandoffCompleted {
		t.Fatal("missing prices were reported as completed execution")
	}
	t.Logf("missing price stopped before provider; error=%v", err)
}

func createMonetaryThread(t *testing.T, st *store.SQLiteStore) domain.Run {
	t.Helper()
	if err := st.SaveWorkspace(t.Context(), store.WorkspaceRecord{ID: "ws-money", Name: "money",
		RootPath: t.TempDir(), CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	_, run, err := application.NewRunService(st).Create(t.Context(), application.CreateRunRequest{
		Goal: "Preserve this task across replies and accurately account for each model call", Profile: "code",
		Surface: "code", Phase: "deliver", WorkspaceID: "ws-money", ModelRoute: "usage-test/model",
		Interactive: true, NetworkMode: "disabled", Budget: domain.Budget{MaxTurns: 50, MaxCostUSD: 0.2},
	})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func monetaryThreadReply(_ llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{Text: rootActionResponse(domain.RootActionFinish, "Reply complete", "End this reply", ""),
		Usage: llm.Usage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5}}, nil
}

func assertMonetaryThreadLedger(t *testing.T, st *store.SQLiteStore, runID string, spent int64) {
	t.Helper()
	usage, err := st.GetMonetaryUsage(t.Context(), runID)
	if err != nil || usage.SettledMicros != spent || usage.ReservedMicros != usage.SettledMicros+usage.ReleasedMicros ||
		usage.RemainingMicros != usage.CapMicros-spent {
		t.Fatalf("wrong ordinary Thread ledger: %+v expected spent=%d err=%v", usage, spent, err)
	}
}
