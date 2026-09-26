package application_test

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/store"
)

type ordinaryMoneyLifecycleProvider struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	unblock sync.Once
	calls   atomic.Int64
	text    string
	usage   llm.Usage
	unknown bool
	finish  llm.FinishReason
}

func (*ordinaryMoneyLifecycleProvider) Name() string { return "usage-test" }
func (*ordinaryMoneyLifecycleProvider) ListModels(context.Context) ([]llm.ModelInfo, error) {
	return []llm.ModelInfo{{ID: "model", Provider: "usage-test"}}, nil
}
func (p *ordinaryMoneyLifecycleProvider) Chat(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
	return nil, nil // The ordinary production path must use StreamChat.
}
func (p *ordinaryMoneyLifecycleProvider) StreamChat(_ context.Context, _ llm.ChatRequest) (<-chan llm.ChatChunk, error) {
	p.calls.Add(1)
	if p.started != nil {
		p.once.Do(func() { close(p.started) })
	}
	if p.release != nil {
		// An already accepted transport may ignore cancellation. Deliver its
		// buffered terminal only after the test has cancelled the real Run.
		<-p.release
	}
	chunks := make(chan llm.ChatChunk, 2)
	if !p.unknown {
		chunks <- llm.ChatChunk{Text: p.text}
		chunks <- llm.FinalChatChunk(&llm.ChatResponse{
			Text: p.text, Provider: p.Name(), Model: "model", Usage: p.usage, FinishReason: p.finish,
		})
	}
	close(chunks)
	return chunks, nil
}
func (*ordinaryMoneyLifecycleProvider) SupportsTools(string) bool    { return false }
func (*ordinaryMoneyLifecycleProvider) SupportsVision(string) bool   { return false }
func (*ordinaryMoneyLifecycleProvider) SupportsJSONMode(string) bool { return false }

func newOrdinaryMoneyLifecycleFixture(t *testing.T, p *ordinaryMoneyLifecycleProvider) (string, *store.SQLiteStore, domain.Run, *application.RunSupervisor) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ordinary-money.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if p.release != nil {
		t.Cleanup(func() { p.unblock.Do(func() { close(p.release) }) })
	}
	service := application.NewRunService(st)
	_, run, err := service.Create(t.Context(), application.CreateRunRequest{
		Goal: "ordinary monetary lifecycle fixture", Profile: "code", ModelRoute: "usage-test/model",
		Budget: domain.Budget{MaxTurns: 3, MaxCostUSD: 0.05},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Start(t.Context(), run.ID); err != nil {
		t.Fatal(err)
	}
	importSupervisorPriceSnapshot(t, t.Context(), st)
	router := llm.NewRouter(llm.ModelRef{Provider: p.Name(), Model: "model"})
	router.RegisterProvider(p)
	window := llm.DefaultContextWindow()
	window.DefaultOutputTokens, window.MaxOutputTokens = 256, 512
	if err := router.SetContextWindow(llm.ModelRef{Provider: p.Name(), Model: "model"}, window); err != nil {
		t.Fatal(err)
	}
	supervisor := application.NewRunSupervisor(st, router, policy.NewDefaultChecker()).
		WithMonetaryBudget(application.NewMonetaryBudgetService(st))
	return path, st, run, supervisor
}

func TestOrdinaryModelProtocolFailureSettlesKnownMonetaryUsage(t *testing.T) {
	// Empty output cannot enter protocol repair, but the accepted response
	// still reports billable usage. It must not become a free failed request.
	p := &ordinaryMoneyLifecycleProvider{usage: llm.Usage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5}}
	_, st, run, supervisor := newOrdinaryMoneyLifecycleFixture(t, p)
	_, stepErr := supervisor.Step(t.Context(), run.ID)
	if stepErr == nil {
		t.Fatal("empty output unexpectedly completed the user reply")
	}
	usage, err := st.GetMonetaryUsage(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	cp, found, err := st.GetSupervisorCheckpoint(t.Context(), run.ID)
	if err != nil || !found || cp.InputTokens != 2 || cp.OutputTokens != 3 {
		t.Fatalf("the known provider usage was not retained: %+v found=%t err=%v", cp, found, err)
	}
	if p.calls.Load() != 1 || usage.SettledMicros != 8 || usage.ReservedMicros <= 8 || usage.ReleasedMicros != usage.ReservedMicros-8 {
		t.Fatalf("protocol failure released a billed call: calls=%d ledger=%+v step=%v", p.calls.Load(), usage, stepErr)
	}
	assertOrdinaryMoneyNoAssistant(t, st, run)
	t.Logf("protocol_failure provider_calls=1 known_tokens=5 settled_micros=8 reserve=%d", usage.ReservedMicros)
}

func TestOrdinaryModelIncompleteTerminalSettlesUsageWithoutSuccess(t *testing.T) {
	for _, finish := range []llm.FinishReason{llm.FinishReasonLength, llm.FinishReasonPause, llm.FinishReasonRefusal} {
		t.Run(string(finish), func(t *testing.T) {
			p := &ordinaryMoneyLifecycleProvider{finish: finish,
				text:  `{"version":"root_lifecycle.v1","action":"continue","message":"Do not accept this incomplete response"}`,
				usage: llm.Usage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5}}
			_, st, run, supervisor := newOrdinaryMoneyLifecycleFixture(t, p)
			if _, err := supervisor.Step(t.Context(), run.ID); err == nil {
				t.Fatal("incomplete response completed")
			}
			usage := readOrdinaryMoneyUsage(t, st, run.ID)
			cp, found, err := st.GetSupervisorCheckpoint(t.Context(), run.ID)
			if err != nil || !found || cp.TotalTokens != 5 || usage.SettledMicros != 8 || p.calls.Load() != 1 {
				t.Fatalf("lost incomplete usage: checkpoint=%+v money=%+v calls=%d err=%v", cp, usage, p.calls.Load(), err)
			}
			assertOrdinaryMoneyNoAssistant(t, st, run)
			ledger, err := st.ListRunEvents(t.Context(), run.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range ledger {
				if event.Type == events.ModelCompletedEvent {
					t.Fatal("incomplete response emitted model.completed")
				}
			}
		})
	}
}

func TestOrdinaryModelCancelledUnknownReservationSurvivesReopen(t *testing.T) {
	p := &ordinaryMoneyLifecycleProvider{started: make(chan struct{}), release: make(chan struct{}), unknown: true}
	path, st, run, supervisor := newOrdinaryMoneyLifecycleFixture(t, p)
	done := startOrdinaryMoneyStep(t, supervisor, run.ID, p)
	before := readOrdinaryMoneyUsage(t, st, run.ID)
	if before.ReservedMicros <= 0 {
		t.Fatal("provider was entered without a reservation")
	}
	if _, err := application.NewRunService(st).Cancel(t.Context(), run.ID); err != nil {
		t.Fatal(err)
	}
	stopped := readOrdinaryMoneyUsage(t, st, run.ID)
	p.unblock.Do(func() { close(p.release) })
	stepErr := awaitOrdinaryMoneyStep(t, done)
	after := readOrdinaryMoneyUsage(t, st, run.ID)
	if stepErr == nil || p.calls.Load() != 1 {
		t.Fatalf("cancelled unknown request resumed: calls=%d err=%v", p.calls.Load(), stepErr)
	}
	assertOrdinaryMoneyNoAssistant(t, st, run)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	recovered := readOrdinaryMoneyUsage(t, reopened, run.ID)
	// An unresolved sent call may remain reserved or a terminal unknown may
	// conservatively settle its ceiling; neither may release it as free.
	for name, usage := range map[string]domain.MonetaryUsage{"after_stop": stopped, "after_return": after, "reopen": recovered} {
		if usage.ReservedMicros != before.ReservedMicros || usage.ReleasedMicros != 0 {
			t.Errorf("%s erased unknown provider exposure: initial=%+v actual=%+v", name, before, usage)
		}
	}
	if p.calls.Load() != 1 {
		t.Fatal("read-only recovery repeated the provider call")
	}
	t.Logf("unknown_cancel provider_calls=1 reserve=%d settled=%d released=%d reopen_settled=%d", before.ReservedMicros, after.SettledMicros, after.ReleasedMicros, recovered.SettledMicros)
}

func TestOrdinaryModelLateKnownUsageAfterRunCancelIsAccountingOnly(t *testing.T) {
	p := &ordinaryMoneyLifecycleProvider{
		started: make(chan struct{}), release: make(chan struct{}),
		text:  `{"version":"root_lifecycle.v1","action":"continue","message":"late response must never be committed"}`,
		usage: llm.Usage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5},
	}
	path, st, run, supervisor := newOrdinaryMoneyLifecycleFixture(t, p)
	done := startOrdinaryMoneyStep(t, supervisor, run.ID, p)
	if _, err := application.NewRunService(st).Cancel(t.Context(), run.ID); err != nil {
		t.Fatal(err)
	}
	stoppedCP, found, err := st.GetSupervisorCheckpoint(t.Context(), run.ID)
	if err != nil || !found {
		t.Fatalf("missing cancelled checkpoint: %t %v", found, err)
	}
	p.unblock.Do(func() { close(p.release) })
	stepErr := awaitOrdinaryMoneyStep(t, done)
	after := readOrdinaryMoneyUsage(t, st, run.ID)
	actualRun, err := st.GetRun(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	cp, found, err := st.GetSupervisorCheckpoint(t.Context(), run.ID)
	if err != nil || !found {
		t.Fatalf("missing final checkpoint: %t %v", found, err)
	}
	// Existing failure finalization may seal turn_started as turn_failed.
	// It must never start a new turn, become idle/successful, or revive the Run.
	phaseStopped := cp.Phase == stoppedCP.Phase || cp.Phase == domain.SupervisorTurnFailed
	if stepErr == nil || actualRun.Status != domain.RunCancelled || !phaseStopped || cp.AttemptID != stoppedCP.AttemptID || cp.NextTurn != stoppedCP.NextTurn || cp.LeaseID != stoppedCP.LeaseID || cp.LeaseGeneration != stoppedCP.LeaseGeneration {
		t.Fatalf("late response revived execution: run=%s stopped=%+v after=%+v err=%v", actualRun.Status, stoppedCP, cp, stepErr)
	}
	assertOrdinaryMoneyNoAssistant(t, st, run)
	if p.calls.Load() != 1 || cp.InputTokens != 2 || cp.OutputTokens != 3 || after.SettledMicros != 8 || after.ReleasedMicros != after.ReservedMicros-8 {
		t.Errorf("late final usage was dropped with its forbidden reply: calls=%d checkpoint=%+v ledger=%+v", p.calls.Load(), cp, after)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	recovered := readOrdinaryMoneyUsage(t, reopened, run.ID)
	if recovered.SettledMicros != 8 || recovered.ReservedMicros != after.ReservedMicros || recovered.ReleasedMicros != recovered.ReservedMicros-8 {
		t.Errorf("late receipt was not stable on reopen: before=%+v recovered=%+v", after, recovered)
	}
	t.Logf("late_known provider_calls=%d run=%s input=%d output=%d settled=%d assistant_messages=0", p.calls.Load(), actualRun.Status, cp.InputTokens, cp.OutputTokens, recovered.SettledMicros)
}

func TestOrdinaryModelLateKnownAfterActiveCallStopDoesNotCommitReply(t *testing.T) {
	p := &ordinaryMoneyLifecycleProvider{
		started: make(chan struct{}), release: make(chan struct{}),
		text:  `{"version":"root_lifecycle.v1","action":"continue","message":"stopped late reply must stay uncommitted"}`,
		usage: llm.Usage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5},
	}
	_, st, run, supervisor := newOrdinaryMoneyLifecycleFixture(t, p)
	done := startOrdinaryMoneyStep(t, supervisor, run.ID, p)
	stopped, err := supervisor.CancelActiveCall(t.Context(), application.ActiveCallCancelRequest{
		RunID: run.ID, Reason: "explicit ordinary model Stop fixture",
	})
	if err != nil || !stopped.Found || !stopped.Signaled || !stopped.AuditRecorded {
		t.Fatalf("normal Stop did not signal the actual model call: %+v %v", stopped, err)
	}
	p.unblock.Do(func() { close(p.release) })
	stepErr := awaitOrdinaryMoneyStep(t, done)
	usage := readOrdinaryMoneyUsage(t, st, run.ID)
	cp, found, err := st.GetSupervisorCheckpoint(t.Context(), run.ID)
	if err != nil || !found {
		t.Fatalf("missing Stop checkpoint: found=%t err=%v", found, err)
	}
	if stepErr == nil || cp.NextTurn != 1 || cp.Phase == domain.SupervisorIdle || cp.Phase == domain.SupervisorRunCompleted {
		t.Errorf("buffered final reply bypassed explicit Stop: checkpoint=%+v err=%v", cp, stepErr)
	}
	if p.calls.Load() != 1 || cp.InputTokens != 2 || cp.OutputTokens != 3 || usage.SettledMicros != 8 {
		t.Errorf("Stop discarded known final usage: calls=%d checkpoint=%+v ledger=%+v", p.calls.Load(), cp, usage)
	}
	assertOrdinaryMoneyNoAssistant(t, st, run)
	t.Logf("active_call_stop provider_calls=1 known_tokens=5 settled_micros=%d assistant_messages=0", usage.SettledMicros)
}

type ordinaryMoneyTerminalWriteFault struct {
	*store.SQLiteStore
	failProtocol      bool
	commitBeforeError bool
	accountedCP       domain.SupervisorCheckpoint
}

func (s *ordinaryMoneyTerminalWriteFault) RecordSupervisorModelCompleted(ctx context.Context, cp domain.SupervisorCheckpoint, attempt llm.ModelAttempt, response llm.ChatResponse) (domain.SupervisorCheckpoint, error) {
	if !s.failProtocol {
		if s.commitBeforeError {
			updated, err := s.SQLiteStore.RecordSupervisorModelCompleted(ctx, cp, attempt, response)
			if err != nil {
				return updated, err
			}
			s.accountedCP = updated
			return domain.SupervisorCheckpoint{}, apperror.New(apperror.CodeUnavailable, "fixture loses acknowledgement after real completion commit")
		}
		return domain.SupervisorCheckpoint{}, apperror.New(apperror.CodeUnavailable, "fixture rejects completion publication before commit")
	}
	return s.SQLiteStore.RecordSupervisorModelCompleted(ctx, cp, attempt, response)
}

func (s *ordinaryMoneyTerminalWriteFault) RecordSupervisorProtocolFailure(ctx context.Context, cp domain.SupervisorCheckpoint, attempt llm.ModelAttempt, response llm.ChatResponse, reason string, repair bool) (domain.SupervisorCheckpoint, error) {
	if s.failProtocol {
		if s.commitBeforeError {
			updated, err := s.SQLiteStore.RecordSupervisorProtocolFailure(ctx, cp, attempt, response, reason, repair)
			if err != nil {
				return updated, err
			}
			s.accountedCP = updated
			return domain.SupervisorCheckpoint{}, apperror.New(apperror.CodeUnavailable, "fixture loses acknowledgement after real failed terminal commit")
		}
		return domain.SupervisorCheckpoint{}, apperror.New(apperror.CodeUnavailable, "fixture rejects protocol terminal before commit")
	}
	return s.SQLiteStore.RecordSupervisorProtocolFailure(ctx, cp, attempt, response, reason, repair)
}

func (s *ordinaryMoneyTerminalWriteFault) RecordSupervisorModelFailedWithUsage(ctx context.Context, cp domain.SupervisorCheckpoint, attempt llm.ModelAttempt, usage llm.Usage, tools int) (domain.SupervisorCheckpoint, error) {
	updated, err := s.SQLiteStore.RecordSupervisorModelFailedWithUsage(ctx, cp, attempt, usage, tools)
	if err == nil {
		s.accountedCP = updated
	}
	return updated, err
}

func TestOrdinaryModelRejectedPublicationKeepsKnownUsageOnce(t *testing.T) {
	for _, scenario := range []struct {
		name                string
		protocol, committed bool
	}{
		{name: "normal_completion"},
		{name: "invalid_response_terminal", protocol: true},
		{name: "completed_acknowledgement_lost", committed: true},
		{name: "failed_acknowledgement_lost", protocol: true, committed: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			protocol := scenario.protocol
			p := &ordinaryMoneyLifecycleProvider{
				started: make(chan struct{}), release: make(chan struct{}),
				text:  `{"version":"root_lifecycle.v1","action":"continue","message":"unpublished reply"}`,
				usage: llm.Usage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5},
			}
			if protocol {
				p.text = ""
			}
			_, st, run, _ := newOrdinaryMoneyLifecycleFixture(t, p)
			fault := &ordinaryMoneyTerminalWriteFault{SQLiteStore: st, failProtocol: protocol, commitBeforeError: scenario.committed}
			router := llm.NewRouter(llm.ModelRef{Provider: p.Name(), Model: "model"})
			router.RegisterProvider(p)
			window := llm.DefaultContextWindow()
			window.DefaultOutputTokens, window.MaxOutputTokens = 256, 512
			if err := router.SetContextWindow(llm.ModelRef{Provider: p.Name(), Model: "model"}, window); err != nil {
				t.Fatal(err)
			}
			supervisor := application.NewRunSupervisor(fault, router, policy.NewDefaultChecker())
			done := startOrdinaryMoneyStep(t, supervisor, run.ID, p)
			// Give the actual measured request a nonzero duration, so a second
			// charge of the same elapsed time cannot pass accidentally at 0 ms.
			time.Sleep(25 * time.Millisecond)
			p.unblock.Do(func() { close(p.release) })
			stepErr := awaitOrdinaryMoneyStep(t, done)
			cp, found, err := st.GetSupervisorCheckpoint(t.Context(), run.ID)
			if err != nil || !found {
				t.Fatalf("missing failure checkpoint: %t %v", found, err)
			}
			usage := readOrdinaryMoneyUsage(t, st, run.ID)
			if stepErr == nil || p.calls.Load() != 1 || cp.Phase != domain.SupervisorTurnFailed {
				t.Fatalf("failed publication was accepted or repeated: calls=%d cp=%+v err=%v", p.calls.Load(), cp, stepErr)
			}
			if cp.InputTokens != 2 || cp.OutputTokens != 3 || usage.SettledMicros != 8 {
				t.Errorf("publication failure discarded known accounting: cp=%+v ledger=%+v", cp, usage)
			}
			if fault.accountedCP.RunID == "" || fault.accountedCP.ExecutionMillis <= 0 || cp.ExecutionMillis != fault.accountedCP.ExecutionMillis {
				t.Errorf("failure finalization counted request time twice or skipped its receipt: accounted=%+v final=%+v", fault.accountedCP, cp)
			}
			records, err := st.ListRunEvents(t.Context(), run.ID)
			if err != nil {
				t.Fatal(err)
			}
			completed := countEventType(records, events.ModelCompletedEvent)
			failed := countEventType(records, events.ModelFailedEvent)
			wantCompleted := scenario.committed && !scenario.protocol
			if completed+failed != 1 || (wantCompleted && completed != 1) || (!wantCompleted && failed != 1) {
				t.Errorf("one provider response acquired conflicting terminal receipts: completed=%d failed=%d", completed, failed)
			}
			assertOrdinaryMoneyNoAssistant(t, st, run)
			t.Logf("publication_failure protocol=%t committed=%t provider_calls=1 settled=%d accounted_elapsed=%d final_elapsed=%d", protocol, scenario.committed, usage.SettledMicros, fault.accountedCP.ExecutionMillis, cp.ExecutionMillis)
		})
	}
}

func startOrdinaryMoneyStep(t *testing.T, supervisor *application.RunSupervisor, runID string, p *ordinaryMoneyLifecycleProvider) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		_, err := supervisor.Step(t.Context(), runID)
		done <- err
	}()
	select {
	case <-p.started:
	case err := <-done:
		t.Fatalf("ordinary step exited before provider entry: %v", err)
	case <-time.After(8 * time.Second):
		t.Fatal("ordinary provider was not reached")
	}
	return done
}

func awaitOrdinaryMoneyStep(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(8 * time.Second):
		t.Fatal("ordinary monetary step did not settle")
		return nil
	}
}

func readOrdinaryMoneyUsage(t *testing.T, st *store.SQLiteStore, runID string) domain.MonetaryUsage {
	t.Helper()
	usage, err := st.GetMonetaryUsage(t.Context(), runID)
	if err != nil {
		t.Fatal(err)
	}
	return usage
}

func assertOrdinaryMoneyNoAssistant(t *testing.T, st *store.SQLiteStore, run domain.Run) {
	t.Helper()
	messages, err := st.ListSessionMessages(t.Context(), run.SessionID, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range messages {
		if message.Role == "assistant" {
			t.Fatalf("failed/stopped model response became an assistant item: %+v", message)
		}
	}
}
