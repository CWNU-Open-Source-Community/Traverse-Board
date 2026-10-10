package httpapi

import (
	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"net/http"
	"strings"
)

const TaskConfigurationPreviewPath = "/api/v1/task-configuration/preview"
const RunTaskConfigurationPathTemplate = "/api/v1/runs/{run_id}/task-configuration"

func (a *API) serveTaskConfiguration(w http.ResponseWriter, r *http.Request, requestID, runID string) {
	method := http.MethodGet
	if runID == "" {
		method = http.MethodPost
	}
	if !a.authorized(r, a.tokenHash) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="CyberAgent API"`)
		a.writeError(w, requestID, apperror.New(apperror.CodePolicyDenied, "valid read bearer authorization is required"), http.StatusUnauthorized)
		return
	}
	if r.Method != method {
		w.Header().Set("Allow", method)
		a.writeError(w, requestID, apperror.New(apperror.CodeInvalidArgument, "configuration method is invalid"), http.StatusMethodNotAllowed)
		return
	}
	if runID == "" {
		var input application.TaskConfigurationRequest
		if !a.decodeThreadControlBody(w, r, requestID, "Task configuration preview", &input) {
			return
		}
		view, err := application.NewTaskConfigurationService(a.store).Preview(r.Context(), input)
		if err != nil {
			a.writeError(w, requestID, err, 0)
			return
		}
		a.writeSuccess(w, requestID, view, nil)
		return
	}
	if err := rejectQuery(r.URL.Query()); err != nil {
		a.writeError(w, requestID, err, 0)
		return
	}
	if r.ContentLength != 0 || len(r.TransferEncoding) > 0 {
		a.writeError(w, requestID, apperror.New(apperror.CodeInvalidArgument, "configuration reads cannot contain a body"), 0)
		return
	}
	if err := validatePathIdentity(runID); err != nil {
		a.writeError(w, requestID, err, 0)
		return
	}
	run, err := a.store.GetRun(r.Context(), runID)
	if err != nil {
		a.writeError(w, requestID, err, 0)
		return
	}
	mission, err := a.store.GetMission(r.Context(), run.MissionID)
	if err != nil {
		a.writeError(w, requestID, err, 0)
		return
	}
	view, err := application.PinnedTaskConfiguration(run, mission.WorkspaceID, mission.Profile)
	if err != nil {
		a.writeError(w, requestID, err, 0)
		return
	}
	a.writeSuccess(w, requestID, view, nil)
}

func matchRunTaskConfigurationPath(path string) (string, bool) {
	parts := strings.Split(strings.TrimPrefix(path, "/api/v1/"), "/")
	if len(parts) == 3 && parts[0] == "runs" && parts[1] != "" && parts[2] == "task-configuration" {
		return parts[1], true
	}
	return "", false
}
