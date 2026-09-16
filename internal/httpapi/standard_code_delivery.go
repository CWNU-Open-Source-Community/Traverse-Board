package httpapi

import (
	"context"
	"net/http"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
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

// These read-time links are deliberately outside the immutable Report model.
// The existing activity endpoint revalidates and scrubs a body when opened.
type StandardCodeDeliveryOutputSourceView struct {
	JobID       string `json:"job_id"`
	ArtifactID  string `json:"artifact_id"`
	Status      string `json:"status"`
	ThreadID    string `json:"thread_id,omitempty"`
	ActivityRef string `json:"activity_ref,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

type StandardCodeDeliveryReportView struct {
	standardcodedelivery.Report
	OutputSources []StandardCodeDeliveryOutputSourceView `json:"output_sources,omitempty"`
}

type StandardCodeDeliveryRecordResultView struct {
	Report   StandardCodeDeliveryReportView `json:"report"`
	Replayed bool                           `json:"replayed"`
}

func (a *API) standardCodeDeliveryReportView(ctx context.Context, runID string,
	report standardcodedelivery.Report,
) (StandardCodeDeliveryReportView, error) {
	if report.Binding.RunID != runID {
		return StandardCodeDeliveryReportView{}, apperror.New(apperror.CodeFailedPrecondition,
			"Standard Code delivery report Run binding is inconsistent")
	}
	view := StandardCodeDeliveryReportView{Report: report}
	reader, ok := a.store.(application.ThreadActivityDetailStore)
	if !ok {
		for _, verification := range report.Verifications {
			for _, output := range verification.Artifacts {
				view.OutputSources = append(view.OutputSources, StandardCodeDeliveryOutputSourceView{
					JobID: verification.JobID, ArtifactID: output.ID, Status: "metadata_only",
					Reason: "activity_source_unavailable"})
			}
		}
		return view, nil
	}
	for _, source := range application.NewThreadActivityDetailService(reader).
		StandardCodeDeliveryOutputSources(ctx, report) {
		view.OutputSources = append(view.OutputSources, StandardCodeDeliveryOutputSourceView{
			JobID: source.JobID, ArtifactID: source.ArtifactID, Status: source.Status,
			ThreadID: source.ThreadID, ActivityRef: source.ActivityRef, Reason: source.Reason})
	}
	return view, nil
}

// Only detail projections query this fact. List and mutation responses may omit
// it; a similar execution tuple is not evidence of a configured preset.
func (a *API) projectStandardCodePreset(ctx context.Context, view *RunView) error {
	reader, ok := a.store.(interface {
		GetConfiguredStandardCodePresetOperation(context.Context, string) (domain.StandardCodePresetOperation, bool, error)
	})
	if !ok {
		return nil
	}
	operation, found, err := reader.GetConfiguredStandardCodePresetOperation(ctx, view.ID)
	if err != nil {
		return err
	}
	configured := found && operation.Status == domain.StandardCodePresetConfigured &&
		operation.RunID == view.ID && operation.MissionID == view.MissionID
	view.StandardCodePresetConfigured = &configured
	return nil
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
		view, err := a.standardCodeDeliveryReportView(request.Context(), runID, report)
		return view, nil, err
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
		if err != nil {
			return nil, nil, err
		}
		projected, err := a.standardCodeDeliveryReportView(request.Context(), runID, result.Report)
		return StandardCodeDeliveryRecordResultView{Report: projected, Replayed: result.Replayed}, nil, err
	default:
		return nil, nil, apperror.New(apperror.CodeInvalidArgument,
			"Standard Code delivery only supports GET and POST")
	}
}
