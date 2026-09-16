package store

import (
	"encoding/json"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
)

func TestThreadPlanOnDemandContinuationPreservesOriginalEventsAcrossTwoEpochs(t *testing.T) {
	st, ctx, old, selection := createStoreDeliveryGateFixture(t, "on-demand-two-epochs", domain.PlanDeliveryManualAcceptanceOnDemand)
	defer st.Close()
	originals := make([]string, len(selection.WorkItems))
	for i, item := range selection.WorkItems {
		started, err := workWithStore(st).Transition(ctx, item.ID, 0, domain.WorkItemInProgress, "")
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			_, err = application.NewPlanDeliveryControlService(st).TransitionWorkItem(ctx, application.ControlPlanDeliveryWorkItemRequest{
				Version: application.PlanDeliveryControlProtocolVersion,
				PlanDeliveryWorkItemTransition: domain.PlanDeliveryWorkItemTransition{RunID: old.ID, WorkItemID: started.ID,
					ExpectedVersion: started.Version, OperationKey: "on-demand-complete-" + item.ID,
					RequestedBy: "operator", Target: domain.WorkItemCompleted},
			})
		} else {
			_, err = workWithStore(st).Transition(ctx, started.ID, started.Version, domain.WorkItemCompleted, "")
		}
		if err != nil {
			t.Fatal(err)
		}
		if err = st.db.QueryRowContext(ctx, `SELECT completion_event_id FROM plan_on_demand_completion_events WHERE run_id=? AND work_item_id=?`, old.ID, item.ID).Scan(&originals[i]); err != nil {
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
			t.Fatalf("on-demand completion gate: %v", err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		selected, found, err := st.GetPlanDeliverySelectionByRun(ctx, next.ID)
		if err != nil || !found || selected.EffectiveManualAcceptance() != domain.PlanDeliveryManualAcceptanceOnDemand {
			t.Fatalf("selection=%#v found=%t err=%v", selected, found, err)
		}
		sources, err := st.ListThreadPlanCompletionSources(ctx, next.ID)
		if err != nil || len(sources) != len(originals) {
			t.Fatalf("completion sources=%#v err=%v", sources, err)
		}
		for i, selectedItem := range selected.Items {
			item, err := st.GetWorkItem(ctx, selectedItem.WorkItemID)
			if err != nil || item.Status != domain.WorkItemCompleted || item.Version != 1 {
				t.Fatalf("inherited item=%#v err=%v", item, err)
			}
			var source *domain.ThreadPlanCompletionSource
			for j := range sources {
				if sources[j].WorkItemID == item.ID {
					source = &sources[j]
				}
			}
			if source == nil || source.SourceRunID != old.ID || source.SourceWorkItemID != selection.WorkItems[i].ID ||
				source.CompletionEventID != originals[i] || source.CheckpointID != "" || source.HandoffNoteID != "" || source.CompletedAt.IsZero() {
				t.Fatalf("completion source did not retain the original event: %#v", source)
			}
		}
		for _, table := range []string{"delivery_checkpoints", "standard_code_deliveries", "command_runtime_jobs"} {
			var count int
			if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE run_id=?`, next.ID).Scan(&count); err != nil || count != 0 {
				t.Fatalf("copied %s: count=%d err=%v", table, count, err)
			}
		}
		if snapshot, found, err := st.GetStandardCodeSupervisorSnapshot(ctx, next.ID); err != nil || found || snapshot.CanDeliver() {
			t.Fatalf("inherited automatic verification: %#v found=%t err=%v", snapshot, found, err)
		}
		if _, err = application.NewRunService(st).Start(ctx, next.ID); err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		if _, err = st.db.ExecContext(ctx, `UPDATE runs SET status='completed',finished_at=?,updated_at=? WHERE id=?`, ts(now), ts(now), next.ID); err != nil {
			t.Fatalf("ordinary Code database completion gate: %v", err)
		}
		predecessor, err = st.GetRun(ctx, next.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	checkpoints, err := st.ListDeliveryCheckpoints(ctx, old.ID, 20)
	if err != nil || len(checkpoints) != 0 {
		t.Fatalf("invented original manual checks: %#v %v", checkpoints, err)
	}
}

func TestThreadPlanOnDemandContinuationRejectsUnprovenOrChangedCompletion(t *testing.T) {
	for _, scenario := range []string{"missing-event", "changed-version", "changed-criteria", "changed-dependencies"} {
		t.Run(scenario, func(t *testing.T) {
			st, ctx, old, selection := createStoreDeliveryGateFixture(t, "on-demand-"+scenario, domain.PlanDeliveryManualAcceptanceOnDemand)
			defer st.Close()
			item := selection.WorkItems[0]
			if scenario == "missing-event" {
				now := time.Now().UTC()
				// A bare completed projection is not a durable completion receipt.
				if _, err := st.db.ExecContext(ctx, `UPDATE work_items SET status='completed',version=version+1,completed_at=?,updated_at=? WHERE id=?`, ts(now), ts(now), item.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := workWithStore(st).Transition(ctx, item.ID, 0, domain.WorkItemCompleted, ""); err != nil {
					t.Fatal(err)
				}
				var err error
				switch scenario {
				case "changed-version":
					_, err = st.db.ExecContext(ctx, `UPDATE work_items SET version=version+1 WHERE id=?`, item.ID)
				case "changed-criteria":
					_, err = st.db.ExecContext(ctx, `UPDATE work_items SET acceptance_json='["A different requirement"]' WHERE id=?`, item.ID)
				case "changed-dependencies":
					_, err = st.db.ExecContext(ctx, `DELETE FROM work_item_dependencies WHERE work_item_id=?`, selection.WorkItems[1].ID)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			var err error
			old, err = application.NewRunService(st).Cancel(ctx, old.ID)
			if err != nil {
				t.Fatal(err)
			}
			tx, next, mode := newThreadPlanCandidateTx(t, ctx, st, old)
			if err = continueThreadPlanTx(ctx, tx, old, next, mode, next.CreatedAt); err == nil {
				_ = tx.Rollback()
				t.Fatal("unproven/changed completion was inherited")
			}
			_ = tx.Rollback()
			if _, err = st.GetRun(ctx, next.ID); err == nil {
				t.Fatal("failed handoff published its candidate")
			}
		})
	}
}

func TestThreadPlanOnDemandContinuationRejectsWrongItemEventAtDatabaseBoundary(t *testing.T) {
	st, ctx, old, selection := createStoreDeliveryGateFixture(t, "on-demand-wrong-event", domain.PlanDeliveryManualAcceptanceOnDemand)
	defer st.Close()
	for _, item := range selection.WorkItems {
		if _, err := workWithStore(st).Transition(ctx, item.ID, 0, domain.WorkItemCompleted, ""); err != nil {
			t.Fatal(err)
		}
	}
	_, _, foreignRun, foreignSelection := populateStoreDeliveryGateFixture(t, st, "on-demand-foreign-event", domain.PlanDeliveryManualAcceptanceOnDemand)
	if _, err := workWithStore(st).Transition(ctx, foreignSelection.WorkItems[0].ID, 0, domain.WorkItemCompleted, ""); err != nil {
		t.Fatal(err)
	}
	var foreignEventID string
	if err := st.db.QueryRowContext(ctx, `SELECT completion_event_id FROM plan_on_demand_completion_events WHERE run_id=? AND work_item_id=?`, foreignRun.ID, foreignSelection.WorkItems[0].ID).Scan(&foreignEventID); err != nil {
		t.Fatal(err)
	}
	old, err := application.NewRunService(st).Cancel(ctx, old.ID)
	if err != nil {
		t.Fatal(err)
	}
	tx, next, mode := newThreadPlanCandidateTx(t, ctx, st, old)
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "SAVEPOINT continuation"); err != nil {
		t.Fatal(err)
	}
	if err = continueThreadPlanTx(ctx, tx, old, next, mode, next.CreatedAt); err != nil {
		t.Fatal(err)
	}
	var payloadJSON, subject string
	if err = tx.QueryRowContext(ctx, `SELECT payload_json,subject_id FROM run_events WHERE run_id=? AND type=?`, next.ID, threadPlanContinuationEvent).Scan(&payloadJSON, &subject); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, "ROLLBACK TO continuation"); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err = json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		t.Fatal(err)
	}
	items := payload["items"].([]any)
	for _, wrongID := range []any{items[1].(map[string]any)["completion_event_id"], foreignEventID} {
		items[0].(map[string]any)["completion_event_id"] = wrongID
		event, err := events.New(next.ID, next.MissionID, threadPlanContinuationEvent, "thread_plan_continuation", subject, payload)
		if err != nil {
			t.Fatal(err)
		}
		event.CreatedAt = next.CreatedAt
		if _, err = insertRunEventTx(ctx, tx, event); err == nil {
			t.Fatal("database accepted another item's or Thread's completion as provenance")
		}
	}
}
