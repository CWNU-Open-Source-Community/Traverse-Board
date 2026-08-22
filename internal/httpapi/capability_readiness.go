package httpapi

import (
	"net/http"

	"cyberagent-workbench/internal/apperror"
)

const RunCapabilityReadinessPathTemplate = "/api/v1/runs/{run_id}/capability-readiness"

func (a *API) runCapabilityReadiness(request *http.Request,
	runID string,
) (any, *Page, error) {
	if err := rejectQuery(request.URL.Query()); err != nil {
		return nil, nil, err
	}
	if a.capabilityReadiness == nil {
		return nil, nil, apperror.New(apperror.CodeFailedPrecondition,
			"Run capability readiness projection is unavailable")
	}
	projection, err := a.capabilityReadiness.Project(request.Context(), runID)
	if err != nil {
		return nil, nil, err
	}
	return runCapabilityReadinessView(projection), nil, nil
}
