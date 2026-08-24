package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
)

const ThreadCollectionPath = "/api/v1/threads"

type ThreadCreationControlRequestView struct {
	Version     string `json:"version"`
	Goal        string `json:"goal"`
	WorkspaceID string `json:"workspace_id"`
	Profile     string `json:"profile,omitempty"`
	Surface     string `json:"surface,omitempty"`
	Phase       string `json:"phase,omitempty"`
}

type ThreadCreationControlView struct {
	Thread   ThreadView  `json:"thread"`
	Mission  MissionView `json:"mission"`
	Run      RunView     `json:"run"`
	Session  SessionView `json:"session"`
	Mode     RunModeView `json:"mode"`
	Replayed bool        `json:"replayed"`
}

type ThreadMessageControlRequestView struct {
	Version string `json:"version"`
	Content string `json:"content"`
}

type ThreadMessageControlView struct {
	Version          string                      `json:"version"`
	Thread           ThreadView                  `json:"thread"`
	RunID            string                      `json:"run_id"`
	SessionID        string                      `json:"session_id"`
	PredecessorRunID string                      `json:"predecessor_run_id,omitempty"`
	SuccessorCreated bool                        `json:"successor_created"`
	Steering         OperatorSteeringMessageView `json:"steering"`
	Replayed         bool                        `json:"replayed"`
	ExecutionStarted bool                        `json:"execution_started"`
	ModelCalled      bool                        `json:"model_called"`
	ToolCalled       bool                        `json:"tool_called"`
	CapabilityGrant  bool                        `json:"capability_grant"`
}

type ThreadLifecycleControlRequestView struct {
	Version         string `json:"version"`
	ExpectedVersion int64  `json:"expected_version"`
}

type ThreadLifecycleControlView struct {
	Version         string     `json:"version"`
	Thread          ThreadView `json:"thread"`
	CapabilityGrant bool       `json:"capability_grant"`
}

func (a *API) routeThreads(request *http.Request, segments []string) (any, *Page, error) {
	switch len(segments) {
	case 1:
		return a.threads(request)
	case 2:
		return a.thread(request, segments[1])
	case 3:
		switch segments[2] {
		case "messages":
			return a.threadMessages(request, segments[1])
		case "runs":
			return a.threadRuns(request, segments[1])
		case "export":
			return a.threadExport(request, segments[1])
		}
	}
	return nil, nil, apperror.New(apperror.CodeNotFound,
		"Thread HTTP API endpoint was not found")
}

func (a *API) threads(request *http.Request) (any, *Page, error) {
	values := request.URL.Query()
	if err := validateSingleQueryValues(values, "limit", "cursor", "status",
		"include_deleted"); err != nil {
		return nil, nil, err
	}
	pageRequest, err := parseStableListPage(values, request.URL.Path)
	if err != nil {
		return nil, nil, err
	}
	filter := domain.ThreadFilter{Limit: stableListStoreLimit(pageRequest)}
	if raw, ok := singleQueryValue(values, "status"); ok {
		filter.Status = domain.ThreadStatus(strings.ToLower(raw))
		if !domain.ValidThreadStatus(filter.Status) {
			return nil, nil, apperror.New(apperror.CodeInvalidArgument,
				"invalid Thread status filter")
		}
	}
	if raw, ok := singleQueryValue(values, "include_deleted"); ok {
		filter.IncludeDeleted, err = strconv.ParseBool(raw)
		if err != nil {
			return nil, nil, apperror.New(apperror.CodeInvalidArgument,
				"include_deleted must be true or false")
		}
	}
	items, err := a.store.ListThreadsByCreationPage(request.Context(), filter,
		pageRequest.Anchor.BeforeCreatedAt, pageRequest.Anchor.BeforeID)
	if err != nil {
		return nil, nil, err
	}
	views := make([]ThreadView, len(items))
	for index := range items {
		if items[index].ActiveRunID != "" {
			run, loadErr := a.store.GetRun(request.Context(), items[index].ActiveRunID)
			if loadErr != nil {
				return nil, nil, loadErr
			}
			views[index] = threadView(items[index], run.Status)
		} else {
			views[index] = threadView(items[index])
		}
	}
	views, page := trimStableListPage(views, pageRequest, threadStableListPosition)
	return views, page, nil
}

func (a *API) thread(request *http.Request, threadID string) (any, *Page, error) {
	if err := rejectQuery(request.URL.Query()); err != nil {
		return nil, nil, err
	}
	threadRecord, err := a.store.GetThread(request.Context(), threadID)
	if err != nil {
		return nil, nil, err
	}
	mission, err := a.store.GetMission(request.Context(), threadRecord.MissionID)
	if err != nil {
		return nil, nil, err
	}
	bindings, err := a.store.ListThreadRuns(request.Context(), threadRecord.ID)
	if err != nil {
		return nil, nil, err
	}
	runs := make([]ThreadRunView, len(bindings))
	var active *RunView
	var activeStatus domain.RunStatus
	var last RunView
	for index, binding := range bindings {
		run, err := a.store.GetRun(request.Context(), binding.RunID)
		if err != nil {
			return nil, nil, err
		}
		view := runView(run)
		runs[index] = ThreadRunView{Run: view, Ordinal: binding.Ordinal,
			PredecessorRunID: binding.PredecessorRunID, CreatedAt: binding.CreatedAt}
		if run.ID == threadRecord.ActiveRunID {
			copy := view
			active = &copy
			activeStatus = run.Status
		}
		if run.ID == threadRecord.LastRunID {
			last = view
		}
	}
	view := threadView(threadRecord, activeStatus)
	return ThreadDetailView{Thread: view, Mission: missionView(mission),
		ActiveRun: active, LastRun: last, Runs: runs}, nil, nil
}

func (a *API) threadRuns(request *http.Request, threadID string) (any, *Page, error) {
	if err := rejectQuery(request.URL.Query()); err != nil {
		return nil, nil, err
	}
	if _, err := a.store.GetThread(request.Context(), threadID); err != nil {
		return nil, nil, err
	}
	bindings, err := a.store.ListThreadRuns(request.Context(), threadID)
	if err != nil {
		return nil, nil, err
	}
	views := make([]ThreadRunView, len(bindings))
	for index, binding := range bindings {
		run, err := a.store.GetRun(request.Context(), binding.RunID)
		if err != nil {
			return nil, nil, err
		}
		views[index] = ThreadRunView{Run: runView(run), Ordinal: binding.Ordinal,
			PredecessorRunID: binding.PredecessorRunID, CreatedAt: binding.CreatedAt}
	}
	return views, nil, nil
}

func (a *API) threadMessages(request *http.Request, threadID string) (any, *Page, error) {
	values := request.URL.Query()
	if err := validateSingleQueryValues(values, "limit", "cursor", "include_compacted"); err != nil {
		return nil, nil, err
	}
	if _, err := a.store.GetThread(request.Context(), threadID); err != nil {
		return nil, nil, err
	}
	pageRequest, err := parsePage(values, request.URL.Path)
	if err != nil {
		return nil, nil, err
	}
	includeCompacted := false
	if raw, ok := singleQueryValue(values, "include_compacted"); ok {
		includeCompacted, err = strconv.ParseBool(raw)
		if err != nil {
			return nil, nil, apperror.New(apperror.CodeInvalidArgument,
				"include_compacted must be true or false")
		}
	}
	messages, err := a.store.ListThreadMessagesPage(request.Context(), threadID,
		includeCompacted, pageRequest.Offset, pageRequest.Limit+1)
	if err != nil {
		return nil, nil, err
	}
	views := make([]ThreadMessageView, len(messages))
	for index := range messages {
		views[index] = threadMessageView(messages[index])
	}
	views, page := trimPage(views, pageRequest)
	return views, page, nil
}

func (a *API) threadExport(request *http.Request, threadID string) (any, *Page, error) {
	if err := rejectQuery(request.URL.Query()); err != nil {
		return nil, nil, err
	}
	value, err := a.store.ExportThread(request.Context(), threadID)
	if err != nil {
		return nil, nil, err
	}
	bindingsByRun := make(map[string]domain.ThreadRun, len(value.Bindings))
	for _, binding := range value.Bindings {
		bindingsByRun[binding.RunID] = binding
	}
	runs := make([]ThreadRunView, len(value.Runs))
	for index, run := range value.Runs {
		binding := bindingsByRun[run.ID]
		runs[index] = ThreadRunView{Run: runView(run), Ordinal: binding.Ordinal,
			PredecessorRunID: binding.PredecessorRunID, CreatedAt: binding.CreatedAt}
	}
	sessions := make([]SessionView, len(value.Sessions))
	for index, linkedSession := range value.Sessions {
		sessions[index] = SessionView{ID: linkedSession.ID,
			WorkspaceID: linkedSession.WorkspaceID, Title: linkedSession.Title,
			Route: linkedSession.Route, Status: linkedSession.Status,
			CreatedAt: linkedSession.CreatedAt, UpdatedAt: linkedSession.UpdatedAt}
	}
	messages := make([]ThreadMessageView, len(value.Messages))
	for index := range value.Messages {
		messages[index] = threadMessageView(value.Messages[index])
	}
	eventViews := make([]ThreadEventView, len(value.Events))
	for index, event := range value.Events {
		eventViews[index] = ThreadEventView{ID: event.ID, ThreadID: event.ThreadID,
			RunID: event.RunID, Type: event.Type, Source: event.Source,
			Payload: json.RawMessage(event.PayloadJSON), CreatedAt: event.CreatedAt}
	}
	auditViews := make([]ThreadRunAuditEventView, len(value.AuditEvents))
	for index, event := range value.AuditEvents {
		auditViews[index] = ThreadRunAuditEventView{EventID: event.EventID,
			Version: event.Version, RunID: event.RunID, MissionID: event.MissionID,
			Sequence: event.Sequence, Type: event.Type, Source: event.Source,
			SubjectID: event.SubjectID, Payload: json.RawMessage(event.PayloadJSON),
			CreatedAt: event.CreatedAt}
	}
	activeStatus := domain.RunStatus("")
	for _, run := range value.Runs {
		if run.ID == value.Thread.ActiveRunID {
			activeStatus = run.Status
			break
		}
	}
	return ThreadExportView{ProtocolVersion: value.ProtocolVersion,
		ExportedAt: value.ExportedAt, Thread: threadView(value.Thread, activeStatus),
		Mission: missionView(value.Mission), Runs: runs, Sessions: sessions, Messages: messages,
		Events: eventViews, AuditEvents: auditViews}, nil, nil
}

func matchThreadMutationPath(requestPath string) (string, string, bool) {
	const prefix = ThreadCollectionPath + "/"
	if !strings.HasPrefix(requestPath, prefix) {
		return "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(requestPath, prefix), "/")
	if len(parts) != 2 || parts[0] == "" {
		return "", "", false
	}
	switch parts[1] {
	case "messages", "archive", "restore", "delete":
		return parts[0], parts[1], true
	default:
		return "", "", false
	}
}

func (a *API) serveThreadCreationControl(writer http.ResponseWriter, request *http.Request,
	requestID string,
) {
	if !a.runCreationEnabled || !a.sessionMessageEnabled {
		a.writeError(writer, requestID, apperror.New(apperror.CodeNotFound,
			"HTTP API endpoint was not found"), http.StatusNotFound)
		return
	}
	if !a.authorizeThreadControl(writer, request, requestID) {
		return
	}
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
			"Thread creation endpoint only supports POST"), http.StatusMethodNotAllowed)
		return
	}
	operationKey, err := runCreationIdempotencyKey(request.Header)
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	var view ThreadCreationControlRequestView
	if !a.decodeThreadControlBody(writer, request, requestID, "Thread creation", &view) {
		return
	}
	if view.Version != domain.ThreadCreationProtocolVersion {
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
			"unsupported Thread creation version"), 0)
		return
	}
	result, err := application.NewControlledRunCreationService(a.store).
		WithLifecycleHooks(a.lifecycleHooks).Create(request.Context(),
		application.ControlledRunCreationRequest{Version: domain.RunCreationProtocolVersion,
			Goal: view.Goal, WorkspaceID: view.WorkspaceID, Profile: view.Profile,
			Surface: view.Surface, Phase: view.Phase, OperationKey: operationKey,
			RequestedBy: "http_thread_operator"})
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	threadRecord, err := a.store.GetThreadByRun(request.Context(), result.Run.ID)
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	a.writeSuccessStatus(writer, requestID, ThreadCreationControlView{
		Thread: threadView(threadRecord, result.Run.Status), Mission: missionView(result.Mission),
		Run: runView(result.Run), Session: sessionView(result.Session),
		Mode: runModeView(result.Mode), Replayed: result.Replayed,
	}, nil, http.StatusAccepted)
}

func (a *API) serveThreadMutationControl(writer http.ResponseWriter, request *http.Request,
	requestID, threadID, action string,
) {
	if !a.runCreationEnabled || !a.sessionMessageEnabled {
		a.writeError(writer, requestID, apperror.New(apperror.CodeNotFound,
			"HTTP API endpoint was not found"), http.StatusNotFound)
		return
	}
	if !a.authorizeThreadControl(writer, request, requestID) {
		return
	}
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
			"Thread control endpoints only support POST"), http.StatusMethodNotAllowed)
		return
	}
	if err := validatePathIdentity(threadID); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	operationKey, err := sessionControlIdempotencyKey(request.Header, "Thread "+action)
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if action == "messages" {
		var view ThreadMessageControlRequestView
		if !a.decodeThreadControlBody(writer, request, requestID, "Thread message", &view) {
			return
		}
		result, err := application.NewThreadService(a.store).Submit(request.Context(),
			application.SubmitThreadMessageRequest{Version: view.Version, ThreadID: threadID,
				Content: view.Content, OperationKey: operationKey,
				RequestedBy: "http_thread_operator"})
		if err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		a.writeSuccessStatus(writer, requestID, ThreadMessageControlView{
			Version: domain.ThreadMessageProtocolVersion,
			Thread:  threadView(result.Thread, result.Run.Status),
			RunID:   result.Run.ID, SessionID: result.Session.ID,
			PredecessorRunID: result.PredecessorRunID,
			SuccessorCreated: result.SuccessorCreated,
			Steering:         operatorSteeringMessageView(result.Message), Replayed: result.Replayed,
		}, nil, http.StatusAccepted)
		return
	}
	var view ThreadLifecycleControlRequestView
	if !a.decodeThreadControlBody(writer, request, requestID, "Thread lifecycle", &view) {
		return
	}
	if view.Version != domain.ThreadLifecycleProtocolVersion {
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
			"unsupported Thread lifecycle version"), 0)
		return
	}
	threadRecord, err := application.NewThreadService(a.store).Transition(request.Context(),
		threadID, domain.ThreadLifecycleAction(action), view.ExpectedVersion,
		"http_thread_operator", operationKey)
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	activeStatus := domain.RunStatus("")
	if threadRecord.ActiveRunID != "" {
		activeRun, loadErr := a.store.GetRun(request.Context(), threadRecord.ActiveRunID)
		if loadErr != nil {
			a.writeError(writer, requestID, loadErr, 0)
			return
		}
		activeStatus = activeRun.Status
	}
	a.writeSuccessStatus(writer, requestID, ThreadLifecycleControlView{
		Version: domain.ThreadLifecycleProtocolVersion,
		Thread:  threadView(threadRecord, activeStatus),
	}, nil, http.StatusOK)
}

func (a *API) decodeThreadControlBody(writer http.ResponseWriter, request *http.Request,
	requestID, label string, target any,
) bool {
	if err := rejectQuery(request.URL.Query()); err != nil {
		a.writeError(writer, requestID, err, 0)
		return false
	}
	if err := validateJSONContentType(request.Header); err != nil {
		a.writeError(writer, requestID, err, http.StatusUnsupportedMediaType)
		return false
	}
	body, err := readBoundedRequestBody(request, MaxSessionMessageRequestBodyBytes)
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return false
	}
	if !utf8.Valid(body) {
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
			label+" body must be valid UTF-8 JSON"), 0)
		return false
	}
	if err := rejectDuplicateJSONObjectFields(body, label); err != nil {
		a.writeError(writer, requestID, err, 0)
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		a.writeError(writer, requestID, apperror.Wrap(apperror.CodeInvalidArgument,
			fmt.Sprintf("%s body must be one JSON object", label), err), 0)
		return false
	}
	if err := ensureJSONEOF(decoder); err != nil {
		a.writeError(writer, requestID, err, 0)
		return false
	}
	return true
}

func (a *API) authorizeThreadControl(writer http.ResponseWriter, request *http.Request,
	requestID string,
) bool {
	if a.authorized(request, a.controlTokenHash) {
		return true
	}
	writer.Header().Set("WWW-Authenticate", `Bearer realm="CyberAgent Control API"`)
	a.writeError(writer, requestID, apperror.New(apperror.CodePolicyDenied,
		"valid control bearer authorization is required"), http.StatusUnauthorized)
	return false
}
