package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/httpapi"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/session"
)

func TestThreadPlanContinuationPreservesCompletedProgressAcrossTwoEpochs(t *testing.T) {
	st, ctx, old, selection := createStoreDeliveryGateFixture(t, "thread-two-epochs")
	defer st.Close()
	originals := make([]string, len(selection.WorkItems))
	for i, item := range selection.WorkItems {
		started, err := workWithStore(st).Transition(ctx, item.ID, 0, domain.WorkItemInProgress, "")
		if err != nil {
			t.Fatal(err)
		}
		checkpoint := recordStoreDeliveryCheckpoint(t, ctx, st, started.ID, "thread-origin-cp-"+started.ID, i == len(selection.WorkItems)-1)
		originals[i] = checkpoint.ID
		if _, err = workWithStore(st).Transition(ctx, item.ID, 0, domain.WorkItemCompleted, ""); err != nil {
			t.Fatal(err)
		}
	}
	var err error
	old, err = application.NewRunService(st).Cancel(ctx, old.ID)
	if err != nil {
		t.Fatal(err)
	}
	predecessor := old
	for generation := 0; generation < 2; generation++ {
		tx, next, mode := newThreadPlanCandidateTx(t, ctx, st, predecessor)
		if err = continueThreadPlanTx(ctx, tx, predecessor, next, mode, next.CreatedAt); err != nil {
			_ = tx.Rollback()
			t.Fatalf("generation %d: %v", generation, err)
		}
		if err = requireDeliverySelectionCompletionTx(ctx, tx, next.ID); err != nil {
			_ = tx.Rollback()
			t.Fatalf("historical manual gate: %v", err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		selected, found, err := st.GetPlanDeliverySelectionByRun(ctx, next.ID)
		if err != nil || !found || selected.DirectionOrdinal != selection.Selection.DirectionOrdinal {
			t.Fatalf("selection=%#v found=%t err=%v", selected, found, err)
		}
		for i, selectedItem := range selected.Items {
			item, err := st.GetWorkItem(ctx, selectedItem.WorkItemID)
			if err != nil || item.Status != domain.WorkItemCompleted || item.ID == selection.WorkItems[i].ID {
				t.Fatalf("item=%#v err=%v", item, err)
			}
			var originalID string
			if err = st.db.QueryRowContext(ctx, `SELECT checkpoint_id FROM thread_plan_completed_sources WHERE run_id=? AND work_item_id=?`, next.ID, item.ID).Scan(&originalID); err != nil || originalID != originals[i] {
				t.Fatalf("original=%q want=%q err=%v", originalID, originals[i], err)
			}
		}
		checkpoints, err := st.ListDeliveryCheckpoints(ctx, next.ID, 20)
		if err != nil || len(checkpoints) != 0 {
			t.Fatalf("copied manual checkpoints: %#v %v", checkpoints, err)
		}
		if snapshot, found, err := st.GetStandardCodeSupervisorSnapshot(ctx, next.ID); err != nil || found || snapshot.CanDeliver() {
			t.Fatalf("inherited automated pass: %#v found=%t err=%v", snapshot, found, err)
		}
		api, err := httpapi.New(st, httpapi.Config{AccessToken: "thread-plan-read-test-token-0123456789", AppVersion: "test"})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8765/api/v1/runs/"+next.ID, nil)
		request.RemoteAddr = "127.0.0.1:45000"
		request.Header.Set("Authorization", "Bearer thread-plan-read-test-token-0123456789")
		response := httptest.NewRecorder()
		api.ServeHTTP(response, request)
		var envelope struct {
			Data httpapi.RunDetailView `json:"data"`
		}
		if err = json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || response.Code != 200 {
			t.Fatalf("HTTP projection status=%d body=%s err=%v", response.Code, response.Body.String(), err)
		}
		plan := envelope.Data.PlanDelivery
		if plan == nil || plan.ReadyCheckpoints != len(originals) || len(plan.Checkpoints) != 0 || len(plan.ContinuedCompletions) != len(originals) || plan.CapabilityGrant {
			t.Fatalf("HTTP conflated inherited manual progress with new checkpoints: %#v", plan)
		}
		for _, source := range plan.ContinuedCompletions {
			if source.SourceRunID != old.ID || source.HandoffNoteID == "" || source.CompletedAt.IsZero() {
				t.Fatalf("HTTP lost original provenance: %#v", source)
			}
		}
		// Exercise the database completion guard too, without pretending this is a
		// Standard Code automatic-verification fixture (the run is ordinary Code).
		if _, err = application.NewRunService(st).Start(ctx, next.ID); err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		if _, err = st.db.ExecContext(ctx, `UPDATE runs SET status='completed',finished_at=?,updated_at=? WHERE id=?`, ts(now), ts(now), next.ID); err != nil {
			t.Fatalf("database manual completion gate: %v", err)
		}
		predecessor, err = st.GetRun(ctx, next.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	checkpoints, err := st.ListDeliveryCheckpoints(ctx, old.ID, 20)
	if err != nil || len(checkpoints) != len(originals) {
		t.Fatalf("original history changed: %#v %v", checkpoints, err)
	}
}

func TestThreadPlanContinuationRejectsUnverifiableCompletionAndChangedCriteria(t *testing.T) {
	for _, scenario := range []string{"missing-checkpoint", "changed-criteria", "changed-dependencies"} {
		t.Run(scenario, func(t *testing.T) {
			st, ctx, old, selection := createStoreDeliveryGateFixture(t, "thread-"+scenario)
			defer st.Close()
			item := selection.WorkItems[0]
			if scenario == "missing-checkpoint" {
				// Reproduce a legacy/corrupt completed projection. Never manufacture a
				// successful checkpoint to make this state look accepted.
				if _, err := st.db.ExecContext(ctx, `DROP TRIGGER trg_delivery_work_item_completion_guard`); err != nil {
					t.Fatal(err)
				}
				now := time.Now().UTC()
				if _, err := st.db.ExecContext(ctx, `UPDATE work_items SET status='completed',version=version+1,completed_at=?,updated_at=? WHERE id=?`, ts(now), ts(now), item.ID); err != nil {
					t.Fatal(err)
				}
			} else if scenario == "changed-dependencies" {
				if _, err := st.db.ExecContext(ctx, `DELETE FROM work_item_dependencies WHERE work_item_id=?`, selection.WorkItems[1].ID); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := st.db.ExecContext(ctx, `UPDATE work_items SET acceptance_json='["Different requirement"]' WHERE id=?`, item.ID); err != nil {
					t.Fatal(err)
				}
			}
			var err error
			old, err = application.NewRunService(st).Cancel(ctx, old.ID)
			if err != nil {
				t.Fatal(err)
			}
			tx, next, mode := newThreadPlanCandidateTx(t, ctx, st, old)
			err = continueThreadPlanTx(ctx, tx, old, next, mode, next.CreatedAt)
			if err == nil {
				_ = tx.Rollback()
				t.Fatal("unverifiable source was continued")
			}
			_ = tx.Rollback()
			if _, err = st.GetRun(ctx, next.ID); err == nil {
				t.Fatal("failed continuation published a successor")
			}
			thread, err := st.GetThreadByRun(ctx, old.ID)
			if err != nil || thread.LastRunID != old.ID || thread.ActiveRunID != "" {
				t.Fatalf("failed continuation moved Thread: %#v %v", thread, err)
			}
		})
	}
}

func TestThreadPlanContinuationLeavesUnfinishedItemsPendingAndKeepsGate(t *testing.T) {
	st, ctx, old, _ := createStoreDeliveryGateFixture(t, "thread-pending")
	defer st.Close()
	var err error
	old, err = application.NewRunService(st).Cancel(ctx, old.ID)
	if err != nil {
		t.Fatal(err)
	}
	tx, next, mode := newThreadPlanCandidateTx(t, ctx, st, old)
	defer tx.Rollback()
	if err = continueThreadPlanTx(ctx, tx, old, next, mode, next.CreatedAt); err != nil {
		t.Fatal(err)
	}
	if err = requireDeliverySelectionCompletionTx(ctx, tx, next.ID); err == nil {
		t.Fatal("unfinished manual gate was bypassed")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE runs SET status='completed',finished_at=?,updated_at=? WHERE id=?`, ts(next.CreatedAt), ts(next.CreatedAt), next.ID); err == nil {
		t.Fatal("database allowed unfinished inherited plan")
	}
}

func TestThreadPlanContinuationKeepsUnselectedProposalSelectable(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "unselected-plan.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := t.Context()
	runs := application.NewRunService(st)
	_, old, err := runs.Create(ctx, application.CreateRunRequest{Goal: "continue a still-unselected Plan", Profile: "review", Phase: "plan",
		ModelRoute: "store-plan/model", Budget: domain.Budget{MaxTurns: 4, MaxTokens: 1000, MaxToolCalls: 4}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runs.Start(ctx, old.ID); err != nil {
		t.Fatal(err)
	}
	provider := &storePlanProvider{responses: []*llm.ChatResponse{
		{Provider: "store-plan", Model: "model", ToolCalls: []llm.ToolCall{{ID: "unselected-plan-call", Name: "plan_delivery_propose", Arguments: json.RawMessage(storePlanDeliveryPayload)}}},
		{Provider: "store-plan", Model: "model", Text: storeRootWaitResponse(t)},
	}}
	router := llm.NewRouter(llm.ModelRef{Provider: provider.Name(), Model: "model"})
	router.RegisterProvider(provider)
	if _, err = application.NewRunSupervisor(st, router, policy.NewDefaultChecker()).Step(ctx, old.ID); err != nil {
		t.Fatal(err)
	}
	proposals, err := st.ListPlanDeliveryProposals(ctx, old.ID, 10)
	if err != nil || len(proposals) != 1 {
		t.Fatalf("source proposal: %#v %v", proposals, err)
	}
	old, err = runs.Cancel(ctx, old.ID)
	if err != nil {
		t.Fatal(err)
	}
	tx, next, mode := newThreadPlanCandidateTx(t, ctx, st, old)
	if err = continueThreadPlanTx(ctx, tx, old, next, mode, next.CreatedAt); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	current, err := st.ListPlanDeliveryProposals(ctx, next.ID, 10)
	if err != nil || len(current) != 1 || current[0].ID == proposals[0].ID {
		t.Fatalf("continued proposal: %#v %v", current, err)
	}
	if _, selected, err := st.GetPlanDeliverySelectionByRun(ctx, next.ID); err != nil || selected {
		t.Fatalf("continuation invented a direction choice: %t %v", selected, err)
	}
	if _, err = runs.Start(ctx, next.ID); err != nil {
		t.Fatal(err)
	}
	provider.responses = []*llm.ChatResponse{{Provider: "store-plan", Model: "model", Text: storeRootWaitResponse(t)}}
	if _, err = application.NewRunSupervisor(st, router, policy.NewDefaultChecker()).Step(ctx, next.ID); err != nil {
		t.Fatal(err)
	}
	result, err := application.NewPlanDeliveryService(st).Select(ctx, application.SelectPlanDeliveryDirectionRequest{
		ProposalID: current[0].ID, Direction: 1, OperationKey: "select-continued-proposal-0001", RequestedBy: "operator"})
	if err != nil || result.Selection.DirectionOrdinal != 1 {
		t.Fatalf("ordinary explicit choice no longer works: %#v %v", result, err)
	}
}

func newThreadPlanCandidateTx(t *testing.T, ctx context.Context, st *SQLiteStore, predecessor domain.Run) (*sql.Tx, domain.Run, domain.RunModeSnapshot) {
	t.Helper()
	mission, err := st.GetMission(ctx, predecessor.MissionID)
	if err != nil {
		t.Fatal(err)
	}
	currentMode, err := st.GetRunMode(ctx, predecessor.ID)
	if err != nil {
		t.Fatal(err)
	}
	linked, err := st.GetSession(ctx, predecessor.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	thread, err := st.GetThreadByRun(ctx, predecessor.ID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	next := predecessor
	next.ID, next.SessionID = idgen.New("run"), idgen.New("session")
	next.Status, next.StartedAt, next.FinishedAt = domain.RunCreated, nil, nil
	next.CreatedAt, next.UpdatedAt = now, now
	linked.ID, linked.Status, linked.CreatedAt, linked.UpdatedAt = next.SessionID, session.StatusActive, now, now
	mode, err := domain.NewInitialRunModeSnapshot(idgen.New("mode"), next, mission, currentMode.Surface, currentMode.Phase, "thread_service", "continue approved Plan", now)
	if err != nil {
		t.Fatal(err)
	}
	event, err := events.New(next.ID, next.MissionID, events.RunCreatedEvent, "thread_continuation", next.ID, map[string]any{"predecessor_run_id": predecessor.ID})
	if err != nil {
		t.Fatal(err)
	}
	event.CreatedAt = now
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = createRunGraphTx(ctx, tx, mission, next, mode, linked, true, false, []events.Event{event}); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO thread_runs(thread_id,run_id,session_id,ordinal,predecessor_run_id,created_at)
  SELECT ?,?,?,MAX(ordinal)+1,?,? FROM thread_runs WHERE thread_id=?`, thread.ID, next.ID, next.SessionID, predecessor.ID, ts(now), thread.ID); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	return tx, next, mode
}
