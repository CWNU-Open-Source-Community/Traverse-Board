package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
)

const ThreadPlanControlPathTemplate = "/api/v1/threads/{thread_id}/plan"

type ThreadPlanController interface {
	ControlPlan(context.Context, application.ThreadPlanControlRequest) (application.ThreadPlanControlResult, error)
	InspectPlan(context.Context, application.ThreadPlanControlRequest) (application.ThreadPlanControlResult, error)
}

type ThreadPlanControlRequestView struct {
	Version          string `json:"version"`
	Action           string `json:"action"`
	RunID            string `json:"run_id"`
	ProposalID       string `json:"proposal_id,omitempty"`
	Direction        int    `json:"direction,omitempty"`
	ManualAcceptance string `json:"manual_acceptance,omitempty"`
	Content          string `json:"content,omitempty"`
}

type ThreadPlanControlView struct {
	Version          string                        `json:"version"`
	ThreadID         string                        `json:"thread_id"`
	RunID            string                        `json:"run_id"`
	Action           string                        `json:"action"`
	State            string                        `json:"state"`
	ProposalID       string                        `json:"proposal_id,omitempty"`
	SelectionID      string                        `json:"selection_id,omitempty"`
	Direction        int                           `json:"direction,omitempty"`
	ManualAcceptance string                        `json:"manual_acceptance,omitempty"`
	AppliedMode      *RunModeView                  `json:"applied_mode,omitempty"`
	CurrentMode      *RunModeView                  `json:"current_mode,omitempty"`
	TurnRequest      *ThreadRequestObservationView `json:"turn_request,omitempty"`
	ExecutionStarted bool                          `json:"execution_started"`
	ModelCalled      bool                          `json:"model_called"`
	ToolCalled       bool                          `json:"tool_called"`
	CapabilityGrant  bool                          `json:"capability_grant"`
}

func matchThreadPlanControlPath(path string) (string, bool) {
	const prefix, suffix = "/api/v1/threads/", "/plan"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return "", false
	}
	id := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	return id, id != "" && !strings.Contains(id, "/")
}

func (a *API) threadPlanController() (ThreadPlanController, error) {
	controller, ok := a.threadTurnController.(ThreadPlanController)
	if !ok || !a.planDeliveryControlEnabled || !a.runExecutionEnabled || !a.runLifecycleEnabled || !a.sessionMessageEnabled || !a.runCreationEnabled {
		return nil, apperror.New(apperror.CodeNotFound, "Thread Plan control is unavailable")
	}
	return controller, nil
}

func (a *API) threadPlanObservation(request *http.Request, threadID string) (any, *Page, error) {
	controller, err := a.threadPlanController()
	if err != nil {
		return nil, nil, err
	}
	if err := validateSingleQueryValues(request.URL.Query(), "run_id", "action"); err != nil {
		return nil, nil, err
	}
	key, err := sessionControlIdempotencyKey(request.Header, "Thread Plan observation")
	if err != nil {
		return nil, nil, err
	}
	value, err := controller.InspectPlan(request.Context(), application.ThreadPlanControlRequest{
		Version: application.PlanDeliveryControlProtocolVersion, ThreadID: threadID,
		RunID: request.URL.Query().Get("run_id"), Action: request.URL.Query().Get("action"), OperationKey: key, RequestedBy: "http_thread_operator"})
	if err != nil {
		return nil, nil, err
	}
	return threadPlanControlView(value), nil, nil
}

func (a *API) serveThreadPlanControl(writer http.ResponseWriter, request *http.Request, requestID, threadID string) {
	controller, err := a.threadPlanController()
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if !a.authorizeThreadControl(writer, request, requestID) {
		return
	}
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", "GET, POST")
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument, "Thread Plan accepts GET or POST"), http.StatusMethodNotAllowed)
		return
	}
	if err := validatePathIdentity(threadID); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	key, err := sessionControlIdempotencyKey(request.Header, "Thread Plan control")
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	var view ThreadPlanControlRequestView
	if !a.decodeThreadControlBody(writer, request, requestID, "Thread Plan control", &view) {
		return
	}
	responseControl := http.NewResponseController(writer)
	if err := responseControl.SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		a.writeError(writer, requestID, apperror.New(apperror.CodeUnavailable, "Thread Plan response deadline could not be configured"), 0)
		return
	}
	value, err := controller.ControlPlan(request.Context(), application.ThreadPlanControlRequest{
		Version: view.Version, ThreadID: threadID, RunID: view.RunID, Action: view.Action,
		ProposalID: view.ProposalID, Direction: view.Direction, ManualAcceptance: domain.PlanDeliveryManualAcceptance(view.ManualAcceptance),
		Content: view.Content, OperationKey: key, RequestedBy: "http_thread_operator"})
	_ = responseControl.SetWriteDeadline(time.Now().Add(30 * time.Second))
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	a.writeSuccessStatus(writer, requestID, threadPlanControlView(value), nil, http.StatusAccepted)
}

func threadPlanControlView(value application.ThreadPlanControlResult) ThreadPlanControlView {
	view := ThreadPlanControlView{Version: application.PlanDeliveryControlProtocolVersion,
		ThreadID: value.ThreadID, RunID: value.RunID, Action: value.Action, State: value.State,
		ProposalID: value.ProposalID, SelectionID: value.SelectionID, Direction: value.Direction,
		ManualAcceptance: string(value.ManualAcceptance), ExecutionStarted: value.ExecutionStarted, ModelCalled: value.ModelCalled, ToolCalled: value.ToolCalled}
	if value.AppliedMode != nil {
		mode := runModeView(*value.AppliedMode)
		view.AppliedMode = &mode
	}
	if value.CurrentMode != nil {
		mode := runModeView(*value.CurrentMode)
		view.CurrentMode = &mode
	}
	if value.TurnRequest != nil {
		observation := threadRequestObservationView(*value.TurnRequest)
		view.TurnRequest = &observation
	}
	return view
}
