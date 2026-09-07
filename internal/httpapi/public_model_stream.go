package httpapi

import (
	"net/http"
	"strings"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
)

const (
	PublicModelStreamPathTemplate     = "/api/v1/runs/{run_id}/active-call"
	PublicModelStreamPollPathTemplate = "/api/v1/runs/{run_id}/active-call/poll"
	PublicModelStreamPollVersion      = "model_public_stream_poll.v1"
)

// PublicModelStreamPollView keeps an idle active-call probe on the successful
// HTTP path. The exact snapshot endpoint retains its useful 404 diagnostic
// contract, while renderers can poll this projection without turning the
// expected gap between model calls into browser console errors.
type PublicModelStreamPollView struct {
	Version  string                                 `json:"version"`
	Active   bool                                   `json:"active"`
	Snapshot *application.PublicModelStreamSnapshot `json:"snapshot,omitempty"`
}

type PublicModelStreamSource interface {
	PublicModelStream(string) (application.PublicModelStreamSnapshot, bool)
}

func matchPublicModelStreamPath(requestPath string) (string, bool) {
	const prefix = "/api/v1/runs/"
	const suffix = "/active-call"
	if !strings.HasPrefix(requestPath, prefix) || !strings.HasSuffix(requestPath, suffix) {
		return "", false
	}
	runID := strings.TrimSuffix(strings.TrimPrefix(requestPath, prefix), suffix)
	if runID == "" || strings.Contains(runID, "/") {
		return "", false
	}
	return runID, true
}

func matchPublicModelStreamPollPath(requestPath string) (string, bool) {
	const prefix = "/api/v1/runs/"
	const suffix = "/active-call/poll"
	if !strings.HasPrefix(requestPath, prefix) || !strings.HasSuffix(requestPath, suffix) {
		return "", false
	}
	runID := strings.TrimSuffix(strings.TrimPrefix(requestPath, prefix), suffix)
	if runID == "" || strings.Contains(runID, "/") {
		return "", false
	}
	return runID, true
}

func (a *API) servePublicModelStreamPoll(writer http.ResponseWriter, request *http.Request,
	requestID string, runID string,
) {
	if err := validatePathIdentity(runID); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if err := rejectQuery(request.URL.Query()); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if a.publicModelStreamSource == nil {
		a.writeError(writer, requestID,
			apperror.New(apperror.CodeNotFound, "public model stream is not available"), 0)
		return
	}
	snapshot, found := a.publicModelStreamSource.PublicModelStream(runID)
	if !found {
		a.writeSuccess(writer, requestID, PublicModelStreamPollView{
			Version: PublicModelStreamPollVersion,
			Active:  false,
		}, nil)
		return
	}
	if err := snapshot.Validate(); err != nil || snapshot.Call.RunID != runID {
		if err == nil {
			err = apperror.New(apperror.CodeConflict, "public model stream Run binding changed")
		}
		a.writeError(writer, requestID,
			apperror.Wrap(apperror.CodeConflict, "invalid public model stream snapshot", err), 0)
		return
	}
	a.writeSuccess(writer, requestID, PublicModelStreamPollView{
		Version:  PublicModelStreamPollVersion,
		Active:   true,
		Snapshot: &snapshot,
	}, nil)
}

func (a *API) servePublicModelStream(writer http.ResponseWriter, request *http.Request,
	requestID string, runID string,
) {
	if err := validatePathIdentity(runID); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if err := rejectQuery(request.URL.Query()); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if a.publicModelStreamSource == nil {
		a.writeError(writer, requestID,
			apperror.New(apperror.CodeNotFound, "public model stream is not available"), 0)
		return
	}
	snapshot, found := a.publicModelStreamSource.PublicModelStream(runID)
	if !found {
		a.writeError(writer, requestID,
			apperror.New(apperror.CodeNotFound, "active public model stream was not found"), 0)
		return
	}
	if err := snapshot.Validate(); err != nil || snapshot.Call.RunID != runID {
		if err == nil {
			err = apperror.New(apperror.CodeConflict, "public model stream Run binding changed")
		}
		a.writeError(writer, requestID,
			apperror.Wrap(apperror.CodeConflict, "invalid public model stream snapshot", err), 0)
		return
	}
	a.writeSuccess(writer, requestID, snapshot, nil)
}
