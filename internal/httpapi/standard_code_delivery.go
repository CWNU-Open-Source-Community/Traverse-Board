package httpapi

import (
	"context"
	"net/http"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/standardcodedelivery"
)

const (
	StandardCodeDeliveryPathTemplate = "/api/v1/runs/{run_id}/standard-code-delivery"
	MaxStandardCodeDeliveryBodyBytes = 64 * 1024
)

type StandardCodeDeliveryController interface {
	Current(context.Context, string) (standardcodedelivery.Report, bool, error)
	Record(context.Context, application.StandardCodeDeliveryRecordRequest) (
		application.StandardCodeDeliveryRecordResult, error)
}

type StandardCodeDeliveryRecordView struct {
	OperationKey       string                           `json:"operation_key"`
	Declaration        standardcodedelivery.Declaration `json:"declaration,omitempty"`
	VerificationJobIDs []string                         `json:"verification_job_ids"`
	UncoveredItems     []string                         `json:"uncovered_items"`
}

func matchStandardCodeDeliveryPath(value string) (string, bool) {
	return matchRunOperationControlPath(value, "/standard-code-delivery")
}

func (a *API) serveStandardCodeDelivery(writer http.ResponseWriter,
	request *http.Request, requestID, runID string,
) {
	if a.standardCodeDeliveryController == nil {
		a.writeError(writer, requestID,
			apperror.New(apperror.CodeNotFound, "HTTP API endpoint was not found"),
			http.StatusNotFound)
		return
	}
	if err := validatePathIdentity(runID); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	switch request.Method {
	case http.MethodGet:
		if !a.authorized(request, a.tokenHash) {
			writer.Header().Set("WWW-Authenticate", `Bearer realm="CyberAgent API"`)
			a.writeError(writer, requestID, apperror.New(apperror.CodePolicyDenied,
				"valid bearer authorization is required"), http.StatusUnauthorized)
			return
		}
		if request.ContentLength != 0 || len(request.TransferEncoding) != 0 {
			a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
				"read-only HTTP API requests cannot contain a body"), 0)
			return
		}
	case http.MethodPost:
		if !a.authorized(request, a.controlTokenHash) {
			writer.Header().Set("WWW-Authenticate", `Bearer realm="CyberAgent Control API"`)
			a.writeError(writer, requestID, apperror.New(apperror.CodePolicyDenied,
				"valid control bearer authorization is required"), http.StatusUnauthorized)
			return
		}
	default:
		writer.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
			"Standard Code delivery only supports GET and POST"), http.StatusMethodNotAllowed)
		return
	}
	value, page, err := a.runStandardCodeDelivery(request, runID)
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	a.writeSuccess(writer, requestID, value, page)
}

func (a *API) runStandardCodeDelivery(request *http.Request,
	runID string,
) (any, *Page, error) {
	if a.standardCodeDeliveryController == nil {
		return nil, nil, apperror.New(apperror.CodeNotFound,
			"Standard Code delivery endpoint is unavailable")
	}
	if err := validatePathIdentity(runID); err != nil {
		return nil, nil, err
	}
	switch request.Method {
	case http.MethodGet:
		if err := rejectQuery(request.URL.Query()); err != nil {
			return nil, nil, err
		}
		report, found, err := a.standardCodeDeliveryController.Current(
			request.Context(), runID)
		if err != nil {
			return nil, nil, err
		}
		if !found {
			return nil, nil, apperror.New(apperror.CodeNotFound,
				"Standard Code delivery report was not found")
		}
		return report, nil, nil
	case http.MethodPost:
		if !a.authorized(request, a.controlTokenHash) {
			return nil, nil, apperror.New(apperror.CodePolicyDenied,
				"valid control bearer authorization is required")
		}
		if err := rejectQuery(request.URL.Query()); err != nil {
			return nil, nil, err
		}
		if err := validateJSONContentType(request.Header); err != nil {
			return nil, nil, err
		}
		body, err := readBoundedRequestBody(request,
			MaxStandardCodeDeliveryBodyBytes)
		if err != nil {
			return nil, nil, err
		}
		if err := rejectDuplicateJSONObjectFields(body,
			"Standard Code delivery"); err != nil {
			return nil, nil, err
		}
		var view StandardCodeDeliveryRecordView
		if err := decodeStrictRunOperation(body, &view,
			"Standard Code delivery"); err != nil {
			return nil, nil, err
		}
		result, err := a.standardCodeDeliveryController.Record(request.Context(),
			application.StandardCodeDeliveryRecordRequest{RunID: runID,
				OperationKey: view.OperationKey, RequestedBy: "api_operator",
				Declaration:        view.Declaration,
				VerificationJobIDs: view.VerificationJobIDs,
				UncoveredItems:     view.UncoveredItems})
		return result, nil, err
	default:
		return nil, nil, apperror.New(apperror.CodeInvalidArgument,
			"Standard Code delivery only supports GET and POST")
	}
}
