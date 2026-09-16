package store

import (
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
)

func TestDeliveryCheckpointStoreProjectionCanonicalOrderKeepsExactFingerprints(t *testing.T) {
	selection := domain.PlanDeliverySelection{Items: []domain.PlanDeliverySelectionItem{
		{WorkItemID: "work-z"}, {WorkItemID: "work-a"}, {WorkItemID: "work-current"},
	}}
	module := domain.PlanDeliveryModule{Title: "Review", Objective: "Preserve the exact review",
		AcceptanceCriteria: []string{"C observed", "A observed", "B observed"}, Dependencies: []int{1, 2}}
	item := domain.WorkItem{ID: "work-current", Title: "Review", Description: "Preserve the exact review",
		AcceptanceCriteria: []string{"A observed", "B observed", "C observed"}, Dependencies: []string{"work-a", "work-z"}}
	proposal := domain.PlanDeliveryProposal{ID: "proposal-original", RunID: "run-original"}
	mode := domain.RunModeSnapshot{ID: "mode-original"}
	checkpoint := domain.DeliveryCheckpoint{ModeSnapshotID: mode.ID,
		AcceptanceFingerprint: domain.DeliveryAcceptanceFingerprint(item.AcceptanceCriteria),
		SourceFingerprint:     domain.DeliverySourceFingerprint(proposal, selection, module, item)}
	if err := validateDeliveryCheckpointProjection(checkpoint, proposal, selection, module, item, mode); err != nil {
		t.Fatalf("canonical criteria and two dependencies rejected by store: %v", err)
	}
	for _, tamper := range []string{"criterion", "dependency", "acceptance_fingerprint", "source_fingerprint", "mode"} {
		t.Run(tamper, func(t *testing.T) {
			changedItem, changedCheckpoint := item, checkpoint
			switch tamper {
			case "criterion":
				changedItem.AcceptanceCriteria = []string{"A observed", "B observed", "C skipped"}
			case "dependency":
				changedItem.Dependencies = []string{"work-a", "work-other"}
			case "acceptance_fingerprint":
				changedCheckpoint.AcceptanceFingerprint = "different"
			case "source_fingerprint":
				changedCheckpoint.SourceFingerprint = "different"
			case "mode":
				changedCheckpoint.ModeSnapshotID = "mode-later"
			}
			if tamper == "criterion" || tamper == "dependency" {
				// Even internally consistent hashes cannot substitute different
				// content or dependencies for the immutable selected module.
				changedCheckpoint.AcceptanceFingerprint = domain.DeliveryAcceptanceFingerprint(changedItem.AcceptanceCriteria)
				changedCheckpoint.SourceFingerprint = domain.DeliverySourceFingerprint(proposal, selection, module, changedItem)
			}
			if err := validateDeliveryCheckpointProjection(changedCheckpoint, proposal, selection, module, changedItem, mode); apperror.CodeOf(err) != apperror.CodeFailedPrecondition {
				t.Fatalf("store accepted altered %s: %v", tamper, err)
			}
		})
	}
}
