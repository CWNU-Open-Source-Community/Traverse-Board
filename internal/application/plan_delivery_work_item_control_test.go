package application_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/store"
)

func newPlanWorkControlFixture(t *testing.T) (*store.SQLiteStore, string, domain.Run, []domain.WorkItem) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "plan-work-control.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	// Keep immutable Plan order deliberately different from the canonical
	// WorkItem order, matching the real manual-checkpoint UI journey.
	payload := strings.Replace(planDeliveryTestPayload, `["Focused tests pass"]`,
		`["人工填写的检查和交接原文在重开后可读取。","记录检查点后待办仍未完成，只有明确完成操作才改变待办状态。","失败说明与未覆盖事项保留，不宣称 Shell 或自动测试通过。"]`, 1)
	run, proposal := createPausedPlanProposalWithPayload(t, t.Context(), st, payload)
	selected, err := application.NewPlanDeliveryService(st).Select(t.Context(), application.SelectPlanDeliveryDirectionRequest{
		ProposalID: proposal.ID, Direction: 2, OperationKey: "plan-work-select-0001", RequestedBy: "operator",
	})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Equal(selected.WorkItems[0].AcceptanceCriteria, proposal.Spec.Directions[1].Modules[0].AcceptanceCriteria) {
		t.Fatal("fixture did not exercise canonical WorkItem criteria ordering")
	}
	if _, err := application.NewPlanDeliveryControlService(st).EnterDelivery(t.Context(), application.ControlPlanDeliveryTransitionRequest{
		Version: application.PlanDeliveryControlProtocolVersion, RunID: run.ID, OperationKey: "plan-work-deliver-0001", RequestedBy: "operator",
	}); err != nil {
		t.Fatal(err)
	}
	return st, path, run, selected.WorkItems
}

func planWorkTransition(runID, itemID, key string, version int64, target domain.WorkItemStatus) application.ControlPlanDeliveryWorkItemRequest {
	return application.ControlPlanDeliveryWorkItemRequest{Version: application.PlanDeliveryControlProtocolVersion,
		PlanDeliveryWorkItemTransition: domain.PlanDeliveryWorkItemTransition{RunID: runID, WorkItemID: itemID,
			OperationKey: key, RequestedBy: "operator", ExpectedVersion: version, Target: target}}
}

func TestPlanDeliveryWorkControlRequiresCheckpointsAndReplaysOriginalIntent(t *testing.T) {
	st, _, run, items := newPlanWorkControlFixture(t)
	ctx := t.Context()
	service := application.NewPlanDeliveryControlService(st)
	start := planWorkTransition(run.ID, items[0].ID, "plan-work-start-0001", items[0].Version, domain.WorkItemInProgress)
	if _, err := service.TransitionWorkItem(ctx, planWorkTransition(run.ID, items[1].ID, "plan-work-dependency-0001", items[1].Version, domain.WorkItemInProgress)); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("dependency gate: %v", err)
	}
	started, err := service.TransitionWorkItem(ctx, start)
	if err != nil || started.Replayed || started.AppliedVersion != 2 {
		t.Fatalf("start=%#v err=%v", started, err)
	}
	noOp := start
	noOp.OperationKey, noOp.ExpectedVersion = "plan-work-noop-new-key", 2
	if _, err := service.TransitionWorkItem(ctx, noOp); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("new key silently accepted a no-op: %v", err)
	}
	complete := planWorkTransition(run.ID, items[0].ID, "plan-work-complete-0001", 2, domain.WorkItemCompleted)
	if _, err := service.TransitionWorkItem(ctx, complete); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("completed without checkpoint: %v", err)
	}
	request := application.ControlPlanDeliveryCheckpointRequest{Version: application.PlanDeliveryControlProtocolVersion,
		RecordDeliveryCheckpointRequest: application.RecordDeliveryCheckpointRequest{RunID: run.ID, WorkItemID: items[0].ID,
			ExpectedWorkItemVersion: 2, OperationKey: "plan-work-checkpoint-0001", RequestedBy: "operator",
			FocusedVerification: "operator observed the focused check", DiffAudit: "operator reviewed the diff",
			SecurityAudit: "operator reviewed the boundary", HandoffSummary: "manual attestation; no automated result is implied"}}
	wrongKey := request
	wrongKey.OperationKey = start.OperationKey
	if _, err := service.RecordCheckpoint(ctx, wrongKey); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("start key reused for checkpoint: %v", err)
	}
	checkpoint, err := service.RecordCheckpoint(ctx, request)
	if err != nil || checkpoint.Replayed {
		t.Fatalf("checkpoint=%#v err=%v", checkpoint, err)
	}
	if checkpoint.Checkpoint.AcceptanceFingerprint != domain.DeliveryAcceptanceFingerprint(items[0].AcceptanceCriteria) ||
		!strings.Contains(checkpoint.Note.Content, "- "+strings.Join(items[0].AcceptanceCriteria, "\n- ")+"\n") {
		t.Fatal("checkpoint or handoff did not preserve all canonical acceptance criteria")
	}
	wrongComplete := complete
	wrongComplete.OperationKey = request.OperationKey
	if _, err := service.TransitionWorkItem(ctx, wrongComplete); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("checkpoint key reused for complete: %v", err)
	}
	completed, err := service.TransitionWorkItem(ctx, complete)
	if err != nil || completed.CurrentWorkItem.Status != domain.WorkItemCompleted {
		t.Fatalf("complete=%#v err=%v", completed, err)
	}
	// Persisted replay must remain available after status, mode and Run lifecycle move on.
	if _, err := application.NewRunService(st).ChangePhase(ctx, application.ChangeRunPhaseRequest{RunID: run.ID, Phase: "plan", OperationKey: "plan-work-plan-again-0001", RequestedBy: "operator", Reason: "revisit later work"}); err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewRunService(st).Cancel(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	for _, previous := range []application.ControlPlanDeliveryWorkItemRequest{start, complete} {
		replayed, err := service.TransitionWorkItem(ctx, previous)
		if err != nil || !replayed.Replayed || replayed.AppliedStatus != previous.Target || replayed.AppliedVersion != previous.ExpectedVersion+1 || replayed.CurrentWorkItem.Version != 3 {
			t.Fatalf("terminal work replay=%#v err=%v", replayed, err)
		}
	}
	replayed, err := service.RecordCheckpoint(ctx, request)
	if err != nil || !replayed.Replayed || replayed.Checkpoint != checkpoint.Checkpoint ||
		replayed.Note.ID != checkpoint.Note.ID || replayed.Note.Content != checkpoint.Note.Content {
		t.Fatalf("terminal checkpoint replay=%#v err=%v", replayed, err)
	}
	changed := request
	changed.HandoffSummary = "a different handoff"
	if _, err := service.RecordCheckpoint(ctx, changed); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("changed handoff replay: %v", err)
	}
	changed = request
	changed.ExpectedWorkItemVersion++
	if _, err := service.RecordCheckpoint(ctx, changed); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("changed version replay: %v", err)
	}
	changed = request
	changed.RequestedBy = "other-operator"
	if _, err := service.RecordCheckpoint(ctx, changed); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("changed actor replay: %v", err)
	}
	wrongComplete = complete
	wrongComplete.WorkItemID = items[1].ID
	if _, err := service.TransitionWorkItem(ctx, wrongComplete); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("changed item replay: %v", err)
	}
	if _, err := service.TransitionWorkItem(ctx, planWorkTransition(run.ID, items[1].ID, "plan-work-terminal-new-0001", items[1].Version, domain.WorkItemInProgress)); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
		t.Fatalf("terminal fresh mutation: %v", err)
	}
	checkpoints, _ := st.ListDeliveryCheckpoints(ctx, run.ID, 20)
	if len(checkpoints) != 1 {
		t.Fatalf("replay duplicated checkpoints: %d", len(checkpoints))
	}
}

type checkpointReplayInterleaveStore struct {
	*store.SQLiteStore
	beforeRead  func()
	beforeWrite func()
}

func (s *checkpointReplayInterleaveStore) GetRun(ctx context.Context, id string) (domain.Run, error) {
	if s.beforeRead != nil {
		hook := s.beforeRead
		s.beforeRead = nil
		hook()
	}
	return s.SQLiteStore.GetRun(ctx, id)
}

func (s *checkpointReplayInterleaveStore) RecordDeliveryCheckpoint(ctx context.Context, operation domain.DeliveryCheckpointOperation,
	checkpoint domain.DeliveryCheckpoint, note domain.Note, checkpointEvent events.Event, noteEvent events.Event,
) (domain.DeliveryCheckpoint, bool, error) {
	if s.beforeWrite != nil {
		hook := s.beforeWrite
		s.beforeWrite = nil
		hook()
	}
	return s.SQLiteStore.RecordDeliveryCheckpoint(ctx, operation, checkpoint, note, checkpointEvent, noteEvent)
}

func TestPlanDeliveryCheckpointConfirmsConcurrentSuccessAfterRunResumes(t *testing.T) {
	for _, stage := range []string{"after_initial_lookup", "before_store_writer"} {
		t.Run(stage, func(t *testing.T) {
			st, path, run, items := newPlanWorkControlFixture(t)
			ctx := t.Context()
			if _, err := application.NewPlanDeliveryControlService(st).TransitionWorkItem(ctx,
				planWorkTransition(run.ID, items[0].ID, "interleave-start-0001", 1, domain.WorkItemInProgress)); err != nil {
				t.Fatal(err)
			}
			second, err := store.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer second.Close()
			request := application.RecordDeliveryCheckpointRequest{RunID: run.ID, WorkItemID: items[0].ID, ExpectedWorkItemVersion: 2,
				OperationKey: "interleave-checkpoint-0001", RequestedBy: "operator", FocusedVerification: "operator observation",
				DiffAudit: "operator diff review", SecurityAudit: "operator security review", HandoffSummary: "operator handoff"}
			var committed application.RecordDeliveryCheckpointResult
			hook := func() {
				committed, err = application.NewDeliveryCheckpointService(second).Record(ctx, request)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := application.NewRunService(second).Resume(ctx, run.ID); err != nil {
					t.Fatal(err)
				}
			}
			interleaved := &checkpointReplayInterleaveStore{SQLiteStore: st}
			if stage == "after_initial_lookup" {
				interleaved.beforeRead = hook
			} else {
				interleaved.beforeWrite = hook
			}
			confirmed, err := application.NewDeliveryCheckpointService(interleaved).Record(ctx, request)
			if err != nil || !confirmed.Replayed || confirmed.Checkpoint.ID != committed.Checkpoint.ID || confirmed.Note.ID != committed.Note.ID {
				t.Fatalf("concurrent commit became an unresolved rejection: %#v err=%v", confirmed, err)
			}
			checkpoints, err := st.ListDeliveryCheckpoints(ctx, run.ID, 10)
			if err != nil || len(checkpoints) != 1 {
				t.Fatalf("checkpoints=%d err=%v", len(checkpoints), err)
			}
		})
	}
}

func TestPlanDeliveryWorkControlCrossStoreReplayAndEventRollback(t *testing.T) {
	st, path, run, items := newPlanWorkControlFixture(t)
	second, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	request := planWorkTransition(run.ID, items[0].ID, "plan-work-shared-start-0001", 1, domain.WorkItemInProgress)
	raw, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Exec(`CREATE TRIGGER fail_plan_work_event BEFORE INSERT ON run_events WHEN NEW.source = 'plan_delivery_control' BEGIN SELECT RAISE(ABORT, 'injected Plan event failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewPlanDeliveryControlService(st).TransitionWorkItem(t.Context(), request); err == nil {
		t.Fatal("injected event failure was ignored")
	}
	item, err := st.GetWorkItem(t.Context(), items[0].ID)
	if err != nil || item.Version != 1 || item.Status != domain.WorkItemPending {
		t.Fatalf("partial WorkItem update=%#v err=%v", item, err)
	}
	if _, err := raw.Exec(`DROP TRIGGER fail_plan_work_event`); err != nil {
		t.Fatal(err)
	}
	stores := []*store.SQLiteStore{st, second}
	const callers = 6
	results := make([]application.ControlPlanDeliveryWorkItemResult, callers)
	errs := make([]error, callers)
	var wait sync.WaitGroup
	start := make(chan struct{})
	for i := range results {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			<-start
			results[i], errs[i] = application.NewPlanDeliveryControlService(stores[i%2]).TransitionWorkItem(context.Background(), request)
		}(i)
	}
	close(start)
	wait.Wait()
	fresh := 0
	for i, result := range results {
		if errs[i] != nil || result.AppliedVersion != 2 || result.CurrentWorkItem.Version != 2 {
			t.Fatalf("caller %d=%#v err=%v", i, result, errs[i])
		}
		if !result.Replayed {
			fresh++
		}
	}
	if fresh != 1 {
		t.Fatalf("fresh transitions=%d", fresh)
	}
	timeline, err := st.ListRunEvents(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range timeline {
		if event.Type == events.WorkItemChangedEvent && event.Source == "plan_delivery_control" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("transition receipts=%d", count)
	}
}
