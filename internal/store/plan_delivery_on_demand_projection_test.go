package store

import (
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
)

func TestPlanDeliveryOnDemandCompletionRejectsChangedSelectedModule(t *testing.T) {
	for _, field := range []string{"criteria", "dependencies"} {
		t.Run(field, func(t *testing.T) {
			st, ctx, run, selected := createStoreDeliveryGateFixture(t, "on-demand-update-"+field, domain.PlanDeliveryManualAcceptanceOnDemand)
			defer st.Close()
			item := selected.WorkItems[0]
			request := application.UpdateWorkItemRequest{ID: item.ID, ExpectedVersion: item.Version}
			if field == "criteria" {
				criteria := []string{"A weaker, different acceptance requirement"}
				request.AcceptanceCriteria = &criteria
			} else {
				item = selected.WorkItems[1]
				dependencies := []string{}
				request.ID, request.ExpectedVersion, request.Dependencies = item.ID, item.Version, &dependencies
			}
			updated, err := workWithStore(st).Update(ctx, request)
			if err != nil {
				t.Fatalf("fixture must use the real existing update path: %v", err)
			}
			started, err := workWithStore(st).Transition(ctx, updated.ID, updated.Version, domain.WorkItemInProgress, "")
			if err != nil {
				t.Fatal(err)
			}
			// The normal operator control endpoint shares the same completion
			// writer as the tool/service path. Removing a manual form must not
			// authorize rewriting the operator's selected module requirements.
			_, err = application.NewPlanDeliveryControlService(st).TransitionWorkItem(ctx, application.ControlPlanDeliveryWorkItemRequest{
				Version: application.PlanDeliveryControlProtocolVersion,
				PlanDeliveryWorkItemTransition: domain.PlanDeliveryWorkItemTransition{RunID: run.ID,
					WorkItemID: started.ID, ExpectedVersion: started.Version, OperationKey: "on-demand-update-complete-" + field,
					RequestedBy: "operator", Target: domain.WorkItemCompleted},
			})
			if apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
				t.Fatalf("changed selected %s completed through the real service: %v", field, err)
			}
			current, err := st.GetWorkItem(ctx, started.ID)
			if err != nil || current.Status != domain.WorkItemInProgress || current.Version != started.Version {
				t.Fatalf("rejected completion changed item: %#v %v", current, err)
			}
		})
	}
}

func TestPlanDeliveryOnDemandSQLCompletionRejectsChangedSelectedModule(t *testing.T) {
	for _, field := range []string{"criteria", "dependencies", "unfinished-dependency"} {
		t.Run(field, func(t *testing.T) {
			st, ctx, _, selected := createStoreDeliveryGateFixture(t, "on-demand-sql-"+field, domain.PlanDeliveryManualAcceptanceOnDemand)
			defer st.Close()
			item := selected.WorkItems[0]
			query := `UPDATE work_items SET status='completed',version=version+1,completed_at=?,updated_at=?,acceptance_json='["A weaker requirement"]' WHERE id=?`
			if field == "dependencies" || field == "unfinished-dependency" {
				item = selected.WorkItems[1]
				if field == "dependencies" {
					if _, err := st.db.ExecContext(ctx, `DELETE FROM work_item_dependencies WHERE work_item_id=?`, item.ID); err != nil {
						t.Fatal(err)
					}
				}
				query = `UPDATE work_items SET status='completed',version=version+1,completed_at=?,updated_at=? WHERE id=?`
			}
			now := ts(time.Now().UTC())
			if _, err := st.db.ExecContext(ctx, query, now, now, item.ID); err == nil {
				t.Fatalf("SQLite accepted changed selected %s as completed", field)
			}
			current, err := st.GetWorkItem(ctx, item.ID)
			if err != nil || current.Status != item.Status || current.Version != item.Version {
				t.Fatalf("rejected native completion changed item: %#v %v", current, err)
			}
		})
	}
}
