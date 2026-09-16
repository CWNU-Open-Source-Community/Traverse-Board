package domain

import (
	"errors"
	"strconv"
	"strings"

	"cyberagent-workbench/internal/runmutation"
)

type PlanDeliveryWorkItemTransition struct {
	RunID, WorkItemID, OperationKey, RequestedBy string
	ExpectedVersion                              int64
	Target                                       WorkItemStatus
}

func (r PlanDeliveryWorkItemTransition) Validate() error {
	for _, id := range []string{r.RunID, r.WorkItemID, r.RequestedBy} {
		if !ValidAgentID(id) || id != strings.TrimSpace(id) || strings.ContainsRune(id, 0) {
			return errors.New("Plan Delivery control identity is invalid")
		}
	}
	if _, err := NormalizeAgentOperationKey(r.OperationKey); err != nil {
		return err
	}
	if r.ExpectedVersion <= 0 || (r.Target != WorkItemInProgress && r.Target != WorkItemCompleted) {
		return errors.New("Plan Delivery transition requires an expected version and start or complete action")
	}
	return nil
}

func (r PlanDeliveryWorkItemTransition) Fingerprint() string {
	return runmutation.Fingerprint("plan_delivery_control.v1", "work_item_transition", r.RunID,
		r.WorkItemID, strconv.FormatInt(r.ExpectedVersion, 10), string(r.Target), r.RequestedBy)
}

// A shared Run/key identity prevents a retry key from authorizing another
// selected item or a different start/checkpoint/complete action.
func PlanDeliveryControlEventID(runID, operationKey string) string {
	return "plan-delivery-op-" + runmutation.Fingerprint("plan_delivery_control.v1", "operation", runID, operationKey)
}
