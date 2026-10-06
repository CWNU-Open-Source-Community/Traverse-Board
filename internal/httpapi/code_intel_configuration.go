package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/codeintel"
)

const (
	CodeIntelConfigurationsPath      = "/api/v1/code-intel/configurations"
	CodeIntelConfigurationReviewPath = "/api/v1/code-intel/configurations/{server_id}/review"
	CodeIntelConfigurationTestPath   = "/api/v1/code-intel/configurations/{server_id}/test"
	CodeIntelConfigurationProtocol   = application.CodeIntelConfigurationProtocol
)

type CodeIntelController interface {
	Configurations(context.Context, string) ([]application.CodeIntelConfiguration, error)
	Stage(context.Context, application.CodeIntelConfigurationRequest) (application.CodeIntelConfiguration, error)
	Review(context.Context, string, application.CodeIntelConfigurationReviewRequest) (application.CodeIntelConfiguration, error)
	Test(context.Context, string, application.CodeIntelConfigurationTestRequest) (application.CodeIntelConfigurationTest, error)
}

type CodeIntelConfigurationRequestView struct {
	Version               string               `json:"version"`
	ServerID              string               `json:"server_id"`
	Name                  string               `json:"name"`
	WorkspaceID           string               `json:"workspace_id"`
	Languages             []codeintel.Language `json:"languages"`
	Executable            string               `json:"executable"`
	Arguments             []string             `json:"arguments"`
	ExecutableSHA256      string               `json:"executable_sha256"`
	InitializationOptions json.RawMessage      `json:"initialization_options,omitempty"`
	RequestTimeoutMillis  int64                `json:"request_timeout_ms"`
}

type CodeIntelConfigurationView struct {
	ProtocolVersion       string               `json:"protocol_version"`
	ServerID              string               `json:"server_id"`
	ServerName            string               `json:"server_name"`
	WorkspaceID           string               `json:"workspace_id"`
	Scope                 string               `json:"scope"`
	Languages             []codeintel.Language `json:"languages"`
	ExecutableSHA256      string               `json:"executable_sha256"`
	DescriptorFingerprint string               `json:"descriptor_fingerprint"`
	ReviewState           string               `json:"review_state"`
	SourceKind            string               `json:"source_kind"`
	SourceLabel           string               `json:"source_label"`
	SourceSHA256          string               `json:"source_sha256"`
	ReviewedBy            string               `json:"reviewed_by,omitempty"`
	ReviewedAt            string               `json:"reviewed_at,omitempty"`
}

type CodeIntelConfigurationReviewRequestView struct {
	Version                       string `json:"version"`
	WorkspaceID                   string `json:"workspace_id"`
	ExpectedDescriptorFingerprint string `json:"expected_descriptor_fingerprint"`
}

type CodeIntelConfigurationTestRequestView struct {
	Version                       string `json:"version"`
	WorkspaceID                   string `json:"workspace_id"`
	ExpectedDescriptorFingerprint string `json:"expected_descriptor_fingerprint"`
	Tool                          string `json:"tool"`
	Path                          string `json:"path,omitempty"`
	Query                         string `json:"query,omitempty"`
}

type CodeIntelConfigurationTestItemView struct {
	Kind  string           `json:"kind"`
	Name  string           `json:"name,omitempty"`
	Path  string           `json:"path,omitempty"`
	Range *codeintel.Range `json:"range,omitempty"`
}

type CodeIntelConfigurationPageView struct {
	Limit     int  `json:"limit"`
	Returned  int  `json:"returned"`
	Total     int  `json:"total"`
	Truncated bool `json:"truncated"`
}

type CodeIntelConfigurationResultView struct {
	ProtocolVersion       string                               `json:"protocol_version"`
	Tool                  string                               `json:"tool"`
	State                 string                               `json:"state"`
	EvidenceLevel         string                               `json:"evidence_level"`
	WorkspaceID           string                               `json:"workspace_id"`
	ServerID              string                               `json:"server_id"`
	ServerGeneration      string                               `json:"server_generation"`
	CapabilityFingerprint string                               `json:"capability_fingerprint"`
	QueryFingerprint      string                               `json:"query_fingerprint"`
	DocumentPath          string                               `json:"document_path,omitempty"`
	DocumentSHA256        string                               `json:"document_sha256,omitempty"`
	Items                 []CodeIntelConfigurationTestItemView `json:"items"`
	Page                  CodeIntelConfigurationPageView       `json:"page"`
	Warnings              []string                             `json:"warnings"`
}

type CodeIntelConfigurationTestView struct {
	ProtocolVersion string                           `json:"protocol_version"`
	Configuration   CodeIntelConfigurationView       `json:"configuration"`
	Server          CodeIntelServerView              `json:"server"`
	Result          CodeIntelConfigurationResultView `json:"result"`
}

type codeIntelConfigurationMutationKind int

const (
	codeIntelConfigurationStage codeIntelConfigurationMutationKind = iota + 1
	codeIntelConfigurationReview
	codeIntelConfigurationTest
)

func matchCodeIntelConfigurationMutationPath(path string) (string, codeIntelConfigurationMutationKind, bool) {
	if path == CodeIntelConfigurationsPath {
		return "", codeIntelConfigurationStage, true
	}
	for _, candidate := range []struct {
		suffix string
		kind   codeIntelConfigurationMutationKind
	}{
		{"/review", codeIntelConfigurationReview}, {"/test", codeIntelConfigurationTest},
	} {
		if strings.HasPrefix(path, CodeIntelConfigurationsPath+"/") && strings.HasSuffix(path, candidate.suffix) {
			identity := strings.TrimSuffix(strings.TrimPrefix(path, CodeIntelConfigurationsPath+"/"), candidate.suffix)
			if identity != "" && !strings.Contains(identity, "/") {
				return identity, candidate.kind, true
			}
		}
	}
	return "", 0, false
}

func (a *API) serveCodeIntelConfigurationMutation(writer http.ResponseWriter, request *http.Request,
	requestID, identity string, kind codeIntelConfigurationMutationKind,
) {
	label := "code-intel configuration"
	if !a.authorizeRunOperation(writer, request, requestID, a.extensionControlEnabled, label) {
		return
	}
	if a.codeIntelController == nil {
		a.writeError(writer, requestID, apperror.New(apperror.CodeNotFound, "code-intel configuration is unavailable"), http.StatusNotFound)
		return
	}
	if identity != "" {
		if err := validatePathIdentity(identity); err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
	}
	var body []byte
	var err error
	if kind == codeIntelConfigurationStage {
		if err = rejectQuery(request.URL.Query()); err == nil {
			err = validateJSONContentType(request.Header)
		}
		if err == nil {
			body, err = readBoundedRequestBody(request, codeintel.MaxConfigBytes)
		}
		if err == nil {
			err = rejectDuplicateJSONObjectFields(body, label)
		}
	} else {
		body, err = readStrictControlBody(request, label)
	}
	if err != nil {
		a.writeError(writer, requestID, err, runOperationErrorStatus(err))
		return
	}
	if len(body) > codeintel.MaxConfigBytes {
		a.writeError(writer, requestID, apperror.New(apperror.CodeResourceExhausted, "code-intel configuration body exceeds its limit"), http.StatusRequestEntityTooLarge)
		return
	}
	switch kind {
	case codeIntelConfigurationStage:
		var view CodeIntelConfigurationRequestView
		if err = decodeStrictRunOperation(body, &view, label); err == nil && view.Version != CodeIntelConfigurationProtocol {
			err = apperror.New(apperror.CodeInvalidArgument, "code-intel configuration version is invalid")
		}
		if err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		if err = validatePathIdentity(view.ServerID); err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		value, err := a.codeIntelController.Stage(request.Context(), application.CodeIntelConfigurationRequest{ServerID: view.ServerID, Name: view.Name,
			WorkspaceID: view.WorkspaceID, Languages: view.Languages, Executable: view.Executable, Arguments: view.Arguments, ExecutableSHA256: view.ExecutableSHA256,
			InitializationOptions: view.InitializationOptions, RequestTimeoutMillis: view.RequestTimeoutMillis})
		if err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		a.writeSuccessStatus(writer, requestID, codeIntelConfigurationView(value), nil, http.StatusAccepted)
	case codeIntelConfigurationReview:
		var view CodeIntelConfigurationReviewRequestView
		if err = decodeStrictRunOperation(body, &view, label); err == nil && view.Version != CodeIntelConfigurationProtocol {
			err = apperror.New(apperror.CodeInvalidArgument, "code-intel configuration version is invalid")
		}
		if err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		value, err := a.codeIntelController.Review(request.Context(), identity, application.CodeIntelConfigurationReviewRequest{WorkspaceID: view.WorkspaceID,
			ExpectedDescriptorFingerprint: view.ExpectedDescriptorFingerprint, ReviewedBy: "http_extension_operator"})
		if err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		a.writeSuccessStatus(writer, requestID, codeIntelConfigurationView(value), nil, http.StatusAccepted)
	case codeIntelConfigurationTest:
		var view CodeIntelConfigurationTestRequestView
		if err = decodeStrictRunOperation(body, &view, label); err == nil && view.Version != CodeIntelConfigurationProtocol {
			err = apperror.New(apperror.CodeInvalidArgument, "code-intel configuration version is invalid")
		}
		if err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		value, err := a.codeIntelController.Test(request.Context(), identity, application.CodeIntelConfigurationTestRequest{WorkspaceID: view.WorkspaceID,
			ExpectedDescriptorFingerprint: view.ExpectedDescriptorFingerprint, Tool: view.Tool, Path: view.Path, Query: view.Query})
		if err != nil {
			a.writeError(writer, requestID, err, 0)
			return
		}
		if err = value.Server.Validate(); err == nil {
			err = value.Result.Validate()
		}
		if err != nil || (value.Result.State != codeintel.EvidenceCurrent && value.Result.State != codeintel.EvidencePartial) {
			a.writeError(writer, requestID, apperror.New(apperror.CodeUnavailable, "LSP probe returned invalid semantic evidence"), 0)
			return
		}
		a.writeSuccessStatus(writer, requestID, codeIntelConfigurationTestView(value), nil, http.StatusAccepted)
	}
}

func codeIntelConfigurationView(value application.CodeIntelConfiguration) CodeIntelConfigurationView {
	result := CodeIntelConfigurationView{ProtocolVersion: CodeIntelConfigurationProtocol, ServerID: value.ServerID, ServerName: value.ServerName,
		WorkspaceID: value.WorkspaceID, Scope: "workspace", Languages: value.Languages, ExecutableSHA256: value.ExecutableSHA256,
		DescriptorFingerprint: value.DescriptorFingerprint, ReviewState: value.ReviewState, SourceKind: value.Source.Kind, SourceLabel: value.Source.Label,
		SourceSHA256: value.Source.SHA256, ReviewedBy: value.ReviewedBy}
	if !value.ReviewedAt.IsZero() {
		result.ReviewedAt = value.ReviewedAt.UTC().Format(time.RFC3339Nano)
	}
	return result
}

func codeIntelConfigurationTestView(value application.CodeIntelConfigurationTest) CodeIntelConfigurationTestView {
	result := value.Result
	projection := CodeIntelConfigurationResultView{ProtocolVersion: result.ProtocolVersion, Tool: result.Tool, State: string(result.State), EvidenceLevel: result.EvidenceLevel,
		WorkspaceID: result.Provenance.WorkspaceID, ServerID: result.Provenance.ServerID, ServerGeneration: result.Provenance.ServerGeneration,
		CapabilityFingerprint: result.Provenance.CapabilityFingerprint, QueryFingerprint: result.Provenance.QueryFingerprint,
		DocumentPath: result.Provenance.DocumentPath, DocumentSHA256: result.Provenance.DocumentSHA256, Items: []CodeIntelConfigurationTestItemView{},
		Page:     CodeIntelConfigurationPageView{Limit: result.Page.Limit, Returned: result.Page.Returned, Total: result.Page.Total, Truncated: result.Page.Truncated},
		Warnings: append([]string{}, result.Warnings...)}
	for _, item := range result.Items {
		projection.Items = append(projection.Items, CodeIntelConfigurationTestItemView{Kind: item.Kind, Name: item.Name, Path: item.Path, Range: item.Range})
	}
	return CodeIntelConfigurationTestView{ProtocolVersion: CodeIntelConfigurationProtocol, Configuration: codeIntelConfigurationView(value.Configuration),
		Server: codeIntelServerView(value.Server), Result: projection}
}
