package application

import (
	"slices"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
)

func TestDeliveryCheckpointProjectionCanonicalCriteriaAndDependencies(t *testing.T) {
	selection := domain.PlanDeliverySelection{Items: []domain.PlanDeliverySelectionItem{
		{WorkItemID: "work-z"}, {WorkItemID: "work-a"}, {WorkItemID: "work-current"},
	}}
	module := domain.PlanDeliveryModule{Title: "Review", Objective: "Preserve the exact review",
		AcceptanceCriteria: []string{"C observed", "A observed", "B observed"}, Dependencies: []int{1, 2}}
	item := domain.WorkItem{ID: "work-current", Title: "Review", Description: "Preserve the exact review",
		AcceptanceCriteria: []string{"A observed", "B observed", "C observed"}, Dependencies: []string{"work-a", "work-z"}}
	if err := validateDeliveryWorkItemProjection(selection, module, item); err != nil {
		t.Fatalf("canonical criteria and two dependencies rejected: %v", err)
	}
	for name, mutate := range map[string]func(*domain.WorkItem){
		"changed_criterion":    func(v *domain.WorkItem) { v.AcceptanceCriteria = []string{"A observed", "B observed", "C skipped"} },
		"missing_criterion":    func(v *domain.WorkItem) { v.AcceptanceCriteria = []string{"A observed", "B observed"} },
		"different_dependency": func(v *domain.WorkItem) { v.Dependencies = []string{"work-a", "work-other"} },
		"missing_dependency":   func(v *domain.WorkItem) { v.Dependencies = []string{"work-a"} },
	} {
		t.Run(name, func(t *testing.T) {
			changed := item
			mutate(&changed)
			if err := validateDeliveryWorkItemProjection(selection, module, changed); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
				t.Fatalf("different immutable projection accepted: %v", err)
			}
		})
	}
	if !slices.Equal(module.AcceptanceCriteria, []string{"C observed", "A observed", "B observed"}) || !slices.Equal(module.Dependencies, []int{1, 2}) {
		t.Fatal("validation rewrote immutable module presentation order")
	}
}
