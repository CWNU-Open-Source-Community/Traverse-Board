package application_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/store"
)

type outputPolicyReloadStore struct {
	*store.SQLiteStore
	router *llm.Router
	ref    llm.ModelRef
	starts int
}

func (s *outputPolicyReloadStore) RecordSupervisorModelStarted(ctx context.Context, checkpoint domain.SupervisorCheckpoint, attempt llm.ModelAttempt) (bool, error) {
	inserted, err := s.SQLiteStore.RecordSupervisorModelStarted(ctx, checkpoint, attempt)
	if inserted && err == nil {
		s.starts++
		s.router.ClearHarnessQualification(s.ref)
	}
	return inserted, err
}

func TestOutputPolicyRejectsReloadWithoutSendingOrCharging(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "not-sent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := t.Context()
	svc := application.NewRunService(st)
	_, run, err := svc.Create(ctx, application.CreateRunRequest{Goal: "reload before dispatch", Profile: "code", ModelRoute: "usage-test/model", Budget: domain.Budget{MaxTurns: 3, MaxCostUSD: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Start(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	importSupervisorPriceSnapshot(t, ctx, st)
	p := &fixedUsageProvider{}
	ref := llm.ModelRef{Provider: p.Name(), Model: "model"}
	router := llm.NewRouter(ref)
	router.RegisterProvider(p)
	wrapper := &outputPolicyReloadStore{SQLiteStore: st, router: router, ref: ref}
	supervisor := application.NewAgentRunner(wrapper, router, policy.NewDefaultChecker()).WithMonetaryBudget(application.NewMonetaryBudgetService(st))
	_, err = supervisor.Step(ctx, run.ID)
	if err == nil {
		t.Fatal("stale request unexpectedly succeeded")
	}
	if wrapper.starts != 1 || p.lastMaxTokens != 0 {
		t.Fatalf("local rejection dispatched or retried: starts=%d max=%d", wrapper.starts, p.lastMaxTokens)
	}
	u, err := st.GetMonetaryUsage(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if u.ReservedMicros <= 0 || u.SettledMicros != 0 || u.ReleasedMicros != u.ReservedMicros || u.RemainingMicros != u.CapMicros {
		t.Fatalf("not-sent attempt charged money: %+v", u)
	}
}

func TestOutputPolicyUsesModelAllowanceInSupervisor(t *testing.T) {
	for _, cost := range []float64{0, 1} {
		t.Run(map[bool]string{true: "cost", false: "uncapped"}[cost > 0], func(t *testing.T) {
			st, err := store.Open(filepath.Join(t.TempDir(), "default.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			ctx := t.Context()
			svc := application.NewRunService(st)
			_, run, err := svc.Create(ctx, application.CreateRunRequest{Goal: "use model output", Profile: "code", ModelRoute: "usage-test/model", Budget: domain.Budget{MaxTurns: 3, MaxCostUSD: cost}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := svc.Start(ctx, run.ID); err != nil {
				t.Fatal(err)
			}
			if cost > 0 {
				importSupervisorPriceSnapshot(t, ctx, st)
			}
			p := &fixedUsageProvider{}
			ref := llm.ModelRef{Provider: p.Name(), Model: "model"}
			router := llm.NewRouter(ref)
			router.RegisterProvider(p)
			w := llm.DefaultContextWindow()
			w.WindowTokens = 1_000_000
			w.DefaultOutputTokens = 8192
			w.MaxOutputTokens = 393216
			w.Source = "provider_model_metadata"
			if err := router.SetContextWindow(ref, w); err != nil {
				t.Fatal(err)
			}
			supervisor := application.NewAgentRunner(st, router, policy.NewDefaultChecker()).WithMonetaryBudget(application.NewMonetaryBudgetService(st))
			if _, err := supervisor.Step(ctx, run.ID); err != nil {
				t.Fatal(err)
			}
			if p.lastMaxTokens != 8192 {
				t.Fatalf("Supervisor lost model default: %d", p.lastMaxTokens)
			}
		})
	}
}

func TestOutputPolicySummaryReloadRecordsNoDispatch(t *testing.T) {
	st := openHistoryRecallStore(t, filepath.Join(t.TempDir(), "summary-reload.db"))
	defer st.Close()
	_, run := createHistoryRecallRun(t, st, "summary-reload", "Summary source stays intact.\n")
	p := newGeneratedProtocolProvider()
	trackGeneratedFixture(t, p)
	turns, _ := generatedThreadServices(st, p, nil)
	for i := 1; i <= 11; i++ {
		submitHistoryRecall(t, turns, run.ID, fmt.Sprintf("summary-reload-%02d", i), generatedFixtureInput(i))
	}
	if p.generatedCount() != 0 {
		t.Fatal("fixture unexpectedly generated early")
	}
	router := generatedFixtureRouter(p)
	wrapper := &outputPolicyReloadStore{SQLiteStore: st, router: router, ref: llm.ModelRef{Provider: p.Name(), Model: "model"}}
	_, err := application.NewThreadService(st).Submit(t.Context(), application.SubmitThreadMessageRequest{Version: domain.ThreadMessageProtocolVersion, ThreadID: domain.InitialThreadID(run.ID), OperationKey: "summary-reload-12", RequestedBy: "test_operator", Content: generatedFixtureInput(12)})
	if err != nil {
		t.Fatal(err)
	}
	handoff := application.NewRunExecutionHandoffService(wrapper, router, policy.NewDefaultChecker())
	_, _ = handoff.Execute(t.Context(), application.ExecuteRunHandoffRequest{Version: domain.RunExecutionHandoffProtocolVersion, RunID: run.ID, MaxSteps: 1, OperationKey: "summary-reload-execute", RequestedBy: "test_operator"})
	if p.generatedCount() != 0 {
		t.Fatal("stale summary reached provider")
	}
	events, err := st.ListRunEvents(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Type != "model.failed" {
			continue
		}
		var receipt struct {
			Purpose      string `json:"purpose"`
			Dispatch     string `json:"dispatch"`
			UsageUnknown bool   `json:"usage_unknown"`
		}
		if err := json.Unmarshal([]byte(event.PayloadJSON), &receipt); err != nil {
			t.Fatal(err)
		}
		if receipt.Purpose == llm.ModelPurposeContextCompaction {
			found = receipt.Dispatch == "not_sent" && !receipt.UsageUnknown
		}
	}
	if !found {
		t.Fatal("summary rejection lacks durable not-sent receipt")
	}
}
