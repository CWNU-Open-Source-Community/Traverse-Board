package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
)

const (
	ThreadApplicationServicesPathTemplate    = "/api/v1/threads/{thread_id}/application-services"
	ThreadApplicationServicePathTemplate     = ThreadApplicationServicesPathTemplate + "/{job_id}"
	ThreadApplicationServiceStopPathTemplate = ThreadApplicationServicePathTemplate + "/stop"
)

type ThreadApplicationServiceController interface {
	List(context.Context, string, int) (application.ThreadApplicationServicesView, error)
	Get(context.Context, string, string) (application.ThreadApplicationServiceDetailView, error)
	Stop(context.Context, string, string, application.ThreadApplicationServiceStopRequest) (application.ThreadApplicationServiceStopView, error)
}

type ThreadApplicationServiceStopRequestView struct {
	Version       string `json:"version"`
	ExpectedRunID string `json:"expected_run_id"`
}

func matchThreadApplicationServicesPath(path string) (threadID, jobID string, stop, matched bool) {
	parts := strings.Split(strings.TrimPrefix(path, "/api/v1/"), "/")
	if len(parts) < 3 || parts[0] != "threads" || parts[1] == "" || parts[2] != "application-services" {
		return
	}
	if len(parts) == 3 {
		return parts[1], "", false, true
	}
	if len(parts) == 4 && parts[3] != "" {
		return parts[1], parts[3], false, true
	}
	if len(parts) == 5 && parts[3] != "" && parts[4] == "stop" {
		return parts[1], parts[3], true, true
	}
	return
}

func (a *API) serveThreadApplicationServices(w http.ResponseWriter, r *http.Request, requestID, threadID, jobID string, stop bool) {
	if a.threadApplicationServiceController == nil {
		a.writeError(w, requestID, apperror.New(apperror.CodeNotFound, "Thread application services are unavailable"), http.StatusNotFound)
		return
	}
	token, method := a.tokenHash, http.MethodGet
	if stop {
		if !a.runExecutionEnabled {
			a.writeError(w, requestID, apperror.New(apperror.CodeNotFound, "Thread application service control is unavailable"), http.StatusNotFound)
			return
		}
		token, method = a.controlTokenHash, http.MethodPost
	}
	if !a.authorized(r, token) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="CyberAgent API"`)
		a.writeError(w, requestID, apperror.New(apperror.CodePolicyDenied, "valid bearer authorization is required"), http.StatusUnauthorized)
		return
	}
	if r.Method != method {
		w.Header().Set("Allow", method)
		a.writeError(w, requestID, apperror.New(apperror.CodeInvalidArgument, "Thread service method is invalid"), http.StatusMethodNotAllowed)
		return
	}
	if err := validatePathIdentity(threadID); err != nil {
		a.writeError(w, requestID, err, 0)
		return
	}
	if jobID != "" {
		if err := validatePathIdentity(jobID); err != nil {
			a.writeError(w, requestID, err, 0)
			return
		}
	}
	if !stop {
		if r.ContentLength != 0 || len(r.TransferEncoding) > 0 {
			a.writeError(w, requestID, apperror.New(apperror.CodeInvalidArgument, "Thread service reads cannot contain a body"), 0)
			return
		}
		var value any
		var err error
		if jobID == "" {
			if err = validateSingleQueryValues(r.URL.Query(), "limit"); err == nil {
				limit := application.DefaultThreadApplicationServicesLimit
				if raw := r.URL.Query().Get("limit"); raw != "" {
					limit, err = strconv.Atoi(raw)
				}
				if err != nil {
					err = apperror.New(apperror.CodeInvalidArgument, "Thread service limit is invalid")
				} else {
					value, err = a.threadApplicationServiceController.List(r.Context(), threadID, limit)
				}
			}
			if view, ok := value.(application.ThreadApplicationServicesView); ok && !a.runExecutionEnabled {
				for i := range view.Services {
					view.Services[i].CanStop = false
				}
				value = view
			}
		} else {
			if err = rejectQuery(r.URL.Query()); err == nil {
				value, err = a.threadApplicationServiceController.Get(r.Context(), threadID, jobID)
			}
			if view, ok := value.(application.ThreadApplicationServiceDetailView); ok && !a.runExecutionEnabled {
				view.Service.CanStop = false
				value = view
			}
		}
		if err != nil {
			a.writeError(w, requestID, err, 0)
			return
		}
		a.writeSuccess(w, requestID, value, nil)
		return
	}
	if err := rejectQuery(r.URL.Query()); err != nil {
		a.writeError(w, requestID, err, 0)
		return
	}
	if err := validateJSONContentType(r.Header); err != nil {
		a.writeError(w, requestID, err, http.StatusUnsupportedMediaType)
		return
	}
	if len(r.Header.Values("Idempotency-Key")) != 1 || r.Header.Get("Idempotency-Key") != "application-stop-"+jobID {
		a.writeError(w, requestID, apperror.New(apperror.CodeInvalidArgument, "Thread service stop key is invalid"), 0)
		return
	}
	body, err := readBoundedRequestBody(r, 4096)
	if err != nil {
		a.writeError(w, requestID, err, 0)
		return
	}
	if err = rejectDuplicateJSONObjectFields(body, "Thread service stop"); err != nil {
		a.writeError(w, requestID, err, 0)
		return
	}
	var input ThreadApplicationServiceStopRequestView
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&input); err == nil {
		err = ensureJSONEOF(decoder)
	}
	if err != nil {
		a.writeError(w, requestID, apperror.Wrap(apperror.CodeInvalidArgument, "Thread service stop must be a closed object", err), 0)
		return
	}
	value, err := a.threadApplicationServiceController.Stop(r.Context(), threadID, jobID, application.ThreadApplicationServiceStopRequest{
		Version: input.Version, ExpectedRunID: input.ExpectedRunID, OperationKey: r.Header.Get("Idempotency-Key")})
	if err != nil {
		a.writeError(w, requestID, err, 0)
		return
	}
	a.writeSuccess(w, requestID, value, nil)
}
