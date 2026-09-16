package application_test

import (
	"context"
	"path/filepath"
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

func TestFailSupervisorTurnPausesRunAndUnlocksNetworkAuthority(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "cyberagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	service := application.NewRunService(st)
	_, run, err := service.Create(ctx, application.CreateRunRequest{
		Goal: "turn failure pause", Profile: "review", Budget: domain.Budget{MaxTurns: 8},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Start(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	lease := acquireTestRunExecutionLease(t, ctx, st, run.ID)
	turn, err := st.BeginSupervisorTurn(ctx, lease, "operator follow-up")
	if err != nil {
		t.Fatal(err)
	}
	if domain.CanExpandRunNetworkAuthority(domain.RunRunning) {
		t.Fatal("network authority expansion must stay blocked while the run is running")
	}
	failed, err := st.FailSupervisorTurn(ctx, turn.Checkpoint, "simulated model failure", time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Phase != domain.SupervisorTurnFailed || failed.LastError != "simulated model failure" {
		t.Fatalf("failure checkpoint was not durable: %#v", failed)
	}
	paused, err := st.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if paused.Status != domain.RunPaused {
		t.Fatalf("failed turn must pause the run; status=%s", paused.Status)
	}
	if !domain.CanExpandRunNetworkAuthority(paused.Status) {
		t.Fatalf("network authority expansion must be allowed on paused status=%s", paused.Status)
	}
	root, found, err := st.GetRootAgent(ctx, run.ID)
	if err != nil || !found {
		t.Fatalf("root agent missing after turn failure: found=%t err=%v", found, err)
	}
	if root.Status != domain.AgentWaiting || root.StatusReason != "turn failed; awaiting operator input" {
		t.Fatalf("root agent should wait for operator input after turn failure: %#v", root)
	}
	if _, _, err := st.ReleaseRunExecutionLease(ctx, lease); err != nil {
		t.Fatal(err)
	}
	supervisor := application.NewRunSupervisor(st, llm.NewDefaultRouter(), policy.NewDefaultChecker())
	if _, err := supervisor.Step(ctx, run.ID); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("paused run must reject automatic step retry: code=%s err=%v", apperror.CodeOf(err), err)
	}
	execution, err := supervisor.Execute(ctx, run.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if execution.StopReason != "run_paused" || execution.RunStatus != domain.RunPaused || len(execution.Steps) != 0 {
		t.Fatalf("execute must remain parked on a failed paused run: %#v", execution)
	}
	if _, err := service.Resume(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	continued, err := st.BeginSupervisorTurn(ctx, acquireTestRunExecutionLease(t, ctx, st, run.ID),
		"new operator input after failure")
	if err != nil {
		t.Fatal(err)
	}
	if continued.Checkpoint.Phase != domain.SupervisorTurnStarted ||
		continued.Checkpoint.NextTurn != turn.Checkpoint.NextTurn ||
		continued.Checkpoint.AttemptID == turn.Checkpoint.AttemptID {
		t.Fatalf("resume must open a new attempt on the same run for the failed turn: %#v",
			continued.Checkpoint)
	}
	if continued.Checkpoint.PendingInput != "new operator input after failure" {
		t.Fatalf("resume must bind the new operator input: %#v", continued.Checkpoint)
	}
}

func TestRunSupervisorStepFailurePausesRunThenOperatorResumeContinues(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "cyberagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	provider := &lifecycleProvider{
		failures: []error{apperror.New(apperror.CodeUnavailable, "provider unreachable")},
		responses: []string{
			"",
			rootActionResponse(domain.RootActionWait, "recovered after operator resume", "", "operator boundary"),
		},
	}
	router := llm.NewRouter(llm.ModelRef{Provider: provider.Name(), Model: "model"})
	router.RegisterProvider(provider)
	run := newStartedRunForProvider(t, st, provider.Name(), domain.Budget{MaxTurns: 8})
	supervisor := application.NewRunSupervisor(st, router, policy.NewDefaultChecker())
	result, err := supervisor.Step(ctx, run.ID)
	if err == nil {
		t.Fatalf("expected model failure; result=%#v", result)
	}
	paused, err := st.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if paused.Status != domain.RunPaused {
		t.Fatalf("step failure must pause the run for the operator; status=%s", paused.Status)
	}
	eventsAfterFailure, err := st.ListRunEvents(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if countEventType(eventsAfterFailure, events.AgentTurnFailedEvent) != 1 {
		t.Fatalf("expected exactly one turn-failure event: %#v", eventsAfterFailure)
	}
	if _, err := application.NewRunService(st).Resume(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	recovered, err := supervisor.StepWithInput(ctx, run.ID, "please retry the original request")
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != application.LifecycleTurnCompleted ||
		recovered.RunStatus != domain.RunPaused ||
		recovered.Action.Kind != domain.RootActionWait ||
		recovered.Turn != 1 ||
		recovered.Checkpoint.NextTurn != 2 {
		t.Fatalf("operator resume must continue the same run: %#v", recovered)
	}
	if provider.calls != 2 {
		t.Fatalf("expected one failed and one recovered model call; calls=%d", provider.calls)
	}
}
