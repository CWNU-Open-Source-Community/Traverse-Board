package httpapi

import (
	"context"
	"net/http"

	"cyberagent-workbench/internal/application"
)

const (
	PlanDirectionControlPathTemplate = "/api/v1/runs/{run_id}/plan/direction"
	PlanModeControlPathTemplate      = "/api/v1/runs/{run_id}/plan/enter"
	PlanDeliveryControlPathTemplate  = "/api/v1/runs/{run_id}/plan/deliver"
)

type PlanDeliveryController interface {
	EnterPlan(context.Context, application.ControlPlanModeTransitionRequest) (
		application.ControlPlanModeTransitionResult, error)
	SelectDirection(context.Context, application.ControlPlanDirectionRequest) (
		application.ControlPlanDirectionResult, error)
	EnterDelivery(context.Context, application.ControlPlanDeliveryTransitionRequest) (
		application.ControlPlanDeliveryTransitionResult, error)
}

type PlanModeTransitionControlRequestView struct {
	Version string `json:"version"`
}

type PlanModeTransitionControlView struct {
	Version          string      `json:"version"`
	RunID            string      `json:"run_id"`
	AppliedMode      RunModeView `json:"applied_mode"`
	CurrentMode      RunModeView `json:"current_mode"`
	Replayed         bool        `json:"replayed"`
	ExecutionStarted bool        `json:"execution_started"`
	ModelCalled      bool        `json:"model_called"`
	ToolCalled       bool        `json:"tool_called"`
	CapabilityGrant  bool        `json:"capability_grant"`
}

type PlanDirectionControlRequestView struct {
	Version    string `json:"version"`
	ProposalID string `json:"proposal_id"`
	Direction  int    `json:"direction"`
}

type PlanDirectionControlView struct {
	Version          string `json:"version"`
	RunID            string `json:"run_id"`
	ProposalID       string `json:"proposal_id"`
	SelectionID      string `json:"selection_id"`
	Direction        int    `json:"direction"`
	WorkItemCount    int    `json:"work_item_count"`
	NoteID           string `json:"note_id"`
	Replayed         bool   `json:"replayed"`
	PhaseChanged     bool   `json:"phase_changed"`
	ExecutionStarted bool   `json:"execution_started"`
	ModelCalled      bool   `json:"model_called"`
	ToolCalled       bool   `json:"tool_called"`
	CapabilityGrant  bool   `json:"capability_grant"`
}

type PlanDeliveryTransitionControlRequestView struct {
	Version string `json:"version"`
}

type PlanDeliveryTransitionControlView struct {
	Version          string      `json:"version"`
	RunID            string      `json:"run_id"`
	SelectionID      string      `json:"selection_id"`
	AppliedMode      RunModeView `json:"applied_mode"`
	CurrentMode      RunModeView `json:"current_mode"`
	Replayed         bool        `json:"replayed"`
	ExecutionStarted bool        `json:"execution_started"`
	ModelCalled      bool        `json:"model_called"`
	ToolCalled       bool        `json:"tool_called"`
	CapabilityGrant  bool        `json:"capability_grant"`
}

func matchPlanDirectionControlPath(requestPath string) (string, bool) {
	return matchRunOperationControlPath(requestPath, "/plan/direction")
}

func matchPlanModeControlPath(requestPath string) (string, bool) {
	return matchRunOperationControlPath(requestPath, "/plan/enter")
}

func matchPlanDeliveryControlPath(requestPath string) (string, bool) {
	return matchRunOperationControlPath(requestPath, "/plan/deliver")
}

func (a *API) servePlanModeControl(writer http.ResponseWriter,
	request *http.Request, requestID string, runID string,
) {
	if !a.authorizeRunOperation(writer, request, requestID,
		a.planDeliveryControlEnabled, "Enter Plan") {
		return
	}
	if err := validatePathIdentity(runID); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if err := validateJSONContentType(request.Header); err != nil {
		a.writeError(writer, requestID, err, http.StatusUnsupportedMediaType)
		return
	}
	operationKey, body, err := a.readRunOperationRequest(request, "Enter Plan control")
	if err != nil {
		a.writeError(writer, requestID, err, runOperationErrorStatus(err))
		return
	}
	var view PlanModeTransitionControlRequestView
	if err := decodeStrictRunOperation(body, &view, "Enter Plan control"); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	result, err := a.planDeliveryController.EnterPlan(request.Context(),
		application.ControlPlanModeTransitionRequest{
			Version: view.Version, RunID: runID, OperationKey: operationKey,
			RequestedBy: "http_plan_operator",
		})
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	a.writeSuccessStatus(writer, requestID, PlanModeTransitionControlView{
		Version: application.PlanDeliveryControlProtocolVersion, RunID: runID,
		AppliedMode: runModeView(result.AppliedMode),
		CurrentMode: runModeView(result.CurrentMode), Replayed: result.Replayed,
	}, nil, http.StatusAccepted)
}

func (a *API) servePlanDirectionControl(writer http.ResponseWriter,
	request *http.Request, requestID string, runID string,
) {
	if !a.authorizeRunOperation(writer, request, requestID,
		a.planDeliveryControlEnabled, "Plan direction") {
		return
	}
	if err := validatePathIdentity(runID); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if err := validateJSONContentType(request.Header); err != nil {
		a.writeError(writer, requestID, err, http.StatusUnsupportedMediaType)
		return
	}
	operationKey, body, err := a.readRunOperationRequest(request, "Plan direction control")
	if err != nil {
		a.writeError(writer, requestID, err, runOperationErrorStatus(err))
		return
	}
	var view PlanDirectionControlRequestView
	if err := decodeStrictRunOperation(body, &view, "Plan direction control"); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	result, err := a.planDeliveryController.SelectDirection(request.Context(),
		application.ControlPlanDirectionRequest{
			Version: view.Version, RunID: runID, ProposalID: view.ProposalID,
			Direction: view.Direction, OperationKey: operationKey,
			RequestedBy: "http_plan_operator",
		})
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	a.writeSuccessStatus(writer, requestID, PlanDirectionControlView{
		Version: application.PlanDeliveryControlProtocolVersion, RunID: result.Selection.RunID,
		ProposalID: result.Selection.ProposalID, SelectionID: result.Selection.ID,
		Direction: result.Selection.DirectionOrdinal, WorkItemCount: len(result.WorkItems),
		NoteID: result.Selection.NoteID, Replayed: result.Replayed,
	}, nil, http.StatusAccepted)
}

func (a *API) servePlanDeliveryControl(writer http.ResponseWriter,
	request *http.Request, requestID string, runID string,
) {
	if !a.authorizeRunOperation(writer, request, requestID,
		a.planDeliveryControlEnabled, "Plan-to-Deliver") {
		return
	}
	if err := validatePathIdentity(runID); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if err := validateJSONContentType(request.Header); err != nil {
		a.writeError(writer, requestID, err, http.StatusUnsupportedMediaType)
		return
	}
	operationKey, body, err := a.readRunOperationRequest(request, "Plan-to-Deliver control")
	if err != nil {
		a.writeError(writer, requestID, err, runOperationErrorStatus(err))
		return
	}
	var view PlanDeliveryTransitionControlRequestView
	if err := decodeStrictRunOperation(body, &view, "Plan-to-Deliver control"); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	result, err := a.planDeliveryController.EnterDelivery(request.Context(),
		application.ControlPlanDeliveryTransitionRequest{
			Version: view.Version, RunID: runID, OperationKey: operationKey,
			RequestedBy: "http_plan_operator",
		})
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	a.writeSuccessStatus(writer, requestID, PlanDeliveryTransitionControlView{
		Version: application.PlanDeliveryControlProtocolVersion, RunID: runID,
		SelectionID: result.SelectionID, AppliedMode: runModeView(result.AppliedMode),
		CurrentMode: runModeView(result.CurrentMode), Replayed: result.Replayed,
	}, nil, http.StatusAccepted)
}
