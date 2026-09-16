package application

import (
	"encoding/json"
	"strings"
	"testing"

	"cyberagent-workbench/internal/domain"
)

func TestStandardCodeSupervisorGuidanceProjectsOnlySelectedManualAcceptance(t *testing.T) {
	for _, test := range []struct {
		name     string
		selected bool
		mode     domain.PlanDeliveryManualAcceptance
		want     string
	}{
		{name: "unselected"},
		{name: "legacy selected", selected: true, want: "manual_acceptance=required"},
		{name: "required", selected: true, mode: domain.PlanDeliveryManualAcceptanceRequired, want: "manual_acceptance=required"},
		{name: "on demand", selected: true, mode: domain.PlanDeliveryManualAcceptanceOnDemand, want: "manual_acceptance=on_demand"},
	} {
		t.Run(test.name, func(t *testing.T) {
			machine, state := newStandardCodeSupervisorTestMachine(domain.StandardCodeSupervisorExecute, domain.ExecutionPhaseDeliver)
			if test.selected {
				selection := domain.PlanDeliverySelection{ManualAcceptance: test.mode}
				machine.effectiveManualAcceptance = selection.EffectiveManualAcceptance()
			} else {
				machine.snapshot.PlanSelectionID = ""
			}
			before, err := json.Marshal(machine.snapshot)
			if err != nil {
				t.Fatal(err)
			}
			request := &llmRequestProjection{}
			machine.addRequestState(request)
			guidance := request.Guidance
			if test.want == "" {
				if strings.Contains(guidance, "manual_acceptance=") || strings.Contains(guidance, "The operator selected") {
					t.Fatalf("unselected Plan was assigned an operator policy: %s", guidance)
				}
			} else if !strings.Contains(guidance, test.want) {
				t.Fatalf("current operator policy absent from model guidance: %s", guidance)
			}
			if test.mode == domain.PlanDeliveryManualAcceptanceOnDemand {
				for _, fact := range []string{"Manual Delivery checkpoints are optional", "work items and their dependencies must still be completed", "real checks must pass", "current verified delivery report is still required", "does not grant execution permission"} {
					if !strings.Contains(guidance, fact) {
						t.Errorf("on-demand guidance omits %q: %s", fact, guidance)
					}
				}
			}
			if test.want == "manual_acceptance=required" && !strings.Contains(guidance, "Keep the existing manual Delivery checkpoint requirements") {
				t.Fatalf("strict policy was relaxed: %s", guidance)
			}
			after, err := json.Marshal(machine.snapshot)
			if err != nil || string(after) != string(before) || len(state.ledger) != 0 {
				t.Fatalf("read-only guidance changed sealed state: before=%s after=%s ledger=%+v err=%v", before, after, state.ledger, err)
			}
		})
	}
}
