package httpapi

import (
	"net/http"
	"strings"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
)

const (
	PlanDeliveryWorkItemStartPathTemplate      = "/api/v1/runs/{run_id}/plan/work-items/{work_item_id}/start"
	PlanDeliveryWorkItemCheckpointPathTemplate = "/api/v1/runs/{run_id}/plan/work-items/{work_item_id}/checkpoint"
	PlanDeliveryWorkItemCompletePathTemplate   = "/api/v1/runs/{run_id}/plan/work-items/{work_item_id}/complete"
)

func planDeliveryWorkItemIdempotencyParameter() openAPIParameter {
	return openAPIParameter{Name: "Idempotency-Key", In: "header", Required: true,
		Description: "Opaque retry key shared across this Run's start/checkpoint/complete actions; reuse only with the original action, item, expected version and evidence",
		Schema:      map[string]any{"type": "string", "minLength": domain.MinAgentOperationKeyBytes, "maxLength": domain.MaxAgentOperationKeyBytes, "pattern": `^\S+$`}}
}

type PlanDeliveryWorkItemControlRequestView struct {
	Version                 string `json:"version"`
	ExpectedWorkItemVersion int64  `json:"expected_work_item_version"`
}

type PlanDeliveryCheckpointControlRequestView struct {
	Version                 string `json:"version"`
	ExpectedWorkItemVersion int64  `json:"expected_work_item_version"`
	FocusedVerification     string `json:"focused_verification"`
	DiffAudit               string `json:"diff_audit"`
	SecurityAudit           string `json:"security_audit"`
	HandoffSummary          string `json:"handoff_summary"`
	FunctionalVerification  string `json:"functional_verification,omitempty"`
	RobustnessAudit         string `json:"robustness_audit,omitempty"`
}

type PlanDeliveryWorkItemControlView struct {
	Version          string       `json:"version"`
	RunID            string       `json:"run_id"`
	WorkItemID       string       `json:"work_item_id"`
	AppliedStatus    string       `json:"applied_status"`
	AppliedVersion   int64        `json:"applied_version"`
	CurrentWorkItem  WorkItemView `json:"current_work_item"`
	Replayed         bool         `json:"replayed"`
	ExecutionStarted bool         `json:"execution_started"`
	ModelCalled      bool         `json:"model_called"`
	ToolCalled       bool         `json:"tool_called"`
	CapabilityGrant  bool         `json:"capability_grant"`
}

type PlanDeliveryCheckpointControlView struct {
	Version          string                 `json:"version"`
	RunID            string                 `json:"run_id"`
	Checkpoint       DeliveryCheckpointView `json:"checkpoint"`
	Note             NoteView               `json:"note"`
	CurrentWorkItem  WorkItemView           `json:"current_work_item"`
	Replayed         bool                   `json:"replayed"`
	ExecutionStarted bool                   `json:"execution_started"`
	ModelCalled      bool                   `json:"model_called"`
	ToolCalled       bool                   `json:"tool_called"`
	CapabilityGrant  bool                   `json:"capability_grant"`
}

func matchPlanDeliveryWorkItemControlPath(path string) (string, string, string, bool) {
	parts := strings.Split(strings.TrimPrefix(path, "/api/v1/runs/"), "/")
	if !strings.HasPrefix(path, "/api/v1/runs/") || len(parts) != 5 || parts[0] == "" ||
		parts[1] != "plan" || parts[2] != "work-items" || parts[3] == "" ||
		(parts[4] != "start" && parts[4] != "checkpoint" && parts[4] != "complete") {
		return "", "", "", false
	}
	return parts[0], parts[3], parts[4], true
}

func (a *API) servePlanDeliveryWorkItemControl(writer http.ResponseWriter, request *http.Request, requestID, runID, itemID, action string) {
	if !a.authorizeRunOperation(writer, request, requestID, a.planDeliveryControlEnabled, "Plan Delivery WorkItem") {
		return
	}
	for _, id := range []string{runID, itemID} {
		if err := validatePathIdentity(id); err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
	}
	if err := validateJSONContentType(request.Header); err != nil {
		a.writeError(writer, requestID, err, http.StatusUnsupportedMediaType)
		return
	}
	limit := int64(MaxRunOperationControlBodyBytes)
	if action == "checkpoint" {
		limit = 64 * 1024
	}
	key, body, err := a.readRunOperationRequestWithLimit(request, "Plan Delivery WorkItem", limit)
	if err != nil {
		a.writeError(writer, requestID, err, runOperationErrorStatus(err))
		return
	}
	if action == "checkpoint" {
		var view PlanDeliveryCheckpointControlRequestView
		if err := decodeStrictRunOperation(body, &view, "Plan Delivery checkpoint"); err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		result, err := a.planDeliveryController.RecordCheckpoint(request.Context(), application.ControlPlanDeliveryCheckpointRequest{
			Version: view.Version, RecordDeliveryCheckpointRequest: application.RecordDeliveryCheckpointRequest{
				RunID: runID, WorkItemID: itemID, ExpectedWorkItemVersion: view.ExpectedWorkItemVersion,
				OperationKey: key, RequestedBy: "http_plan_operator", FocusedVerification: view.FocusedVerification,
				DiffAudit: view.DiffAudit, SecurityAudit: view.SecurityAudit, HandoffSummary: view.HandoffSummary,
				FunctionalVerification: view.FunctionalVerification, RobustnessAudit: view.RobustnessAudit,
			},
		})
		if err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		a.writeSuccessStatus(writer, requestID, PlanDeliveryCheckpointControlView{
			Version: application.PlanDeliveryControlProtocolVersion, RunID: runID, Replayed: result.Replayed,
			Checkpoint: deliveryCheckpointView(result.Checkpoint, domain.DeliveryCheckpointReady(result.Checkpoint, result.CurrentWorkItem, result.CurrentMode)),
			Note:       noteView(result.Note), CurrentWorkItem: workItemView(result.CurrentWorkItem),
		}, nil, http.StatusAccepted)
		return
	}
	var view PlanDeliveryWorkItemControlRequestView
	if err := decodeStrictRunOperation(body, &view, "Plan Delivery WorkItem"); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	target := domain.WorkItemInProgress
	if action == "complete" {
		target = domain.WorkItemCompleted
	}
	result, err := a.planDeliveryController.TransitionWorkItem(request.Context(), application.ControlPlanDeliveryWorkItemRequest{
		Version: view.Version, PlanDeliveryWorkItemTransition: domain.PlanDeliveryWorkItemTransition{
			RunID: runID, WorkItemID: itemID, OperationKey: key, RequestedBy: "http_plan_operator", Target: target, ExpectedVersion: view.ExpectedWorkItemVersion,
		},
	})
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	a.writeSuccessStatus(writer, requestID, PlanDeliveryWorkItemControlView{Version: application.PlanDeliveryControlProtocolVersion,
		RunID: runID, WorkItemID: itemID, AppliedStatus: string(result.AppliedStatus), AppliedVersion: result.AppliedVersion,
		CurrentWorkItem: workItemView(result.CurrentWorkItem), Replayed: result.Replayed,
	}, nil, http.StatusAccepted)
}
