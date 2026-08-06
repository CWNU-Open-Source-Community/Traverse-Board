package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"unicode/utf8"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
)

const (
	EmbeddedAnalyzerExecutionPathTemplate   = "/api/v1/runs/{run_id}/analyzer-executions"
	EmbeddedAnalyzerExecutionControlVersion = "embedded_analyzer_execution_control.v1"
	MaxEmbeddedAnalyzerControlBodyBytes     = 70 * 1024
)

type EmbeddedAnalyzerExecutionController interface {
	Execute(context.Context, application.EmbeddedAnalyzerExecutionRequest) (
		application.EmbeddedAnalyzerExecutionResult, error)
}

type EmbeddedAnalyzerExecutionRequestView struct {
	Version      string `json:"version"`
	Text         string `json:"text,omitempty"`
	File         string `json:"file,omitempty"`
	MediaType    string `json:"media_type,omitempty"`
	Confirmation string `json:"confirmation"`
}

type EmbeddedAnalyzerExecutionControlView struct {
	Version               string `json:"version"`
	ExecutionID           string `json:"execution_id"`
	ArtifactID            string `json:"artifact_id"`
	RunID                 string `json:"run_id"`
	SessionID             string `json:"session_id"`
	WorkspaceID           string `json:"workspace_id"`
	Analyzer              string `json:"analyzer"`
	Status                string `json:"status"`
	MediaType             string `json:"media_type"`
	InputBytes            int    `json:"input_bytes"`
	LineCount             int    `json:"line_count"`
	SHA256                string `json:"sha256"`
	UTF8                  bool   `json:"utf8"`
	MetadataOnly          bool   `json:"metadata_only"`
	CapabilityConsumed    bool   `json:"capability_consumed"`
	ArtifactAtomic        bool   `json:"artifact_atomic"`
	FilesystemMounted     bool   `json:"filesystem_mounted"`
	NetworkEnabled        bool   `json:"network_enabled"`
	SubprocessEnabled     bool   `json:"subprocess_enabled"`
	HostProcessAuthorized bool   `json:"host_process_authorized"`
	RawRequestIncluded    bool   `json:"raw_request_included"`
	BearerTokenIncluded   bool   `json:"bearer_token_included"`
	Replayed              bool   `json:"replayed"`
}

func matchEmbeddedAnalyzerExecutionPath(requestPath string) (string, bool) {
	return matchRunOperationControlPath(requestPath, "/analyzer-executions")
}

func (a *API) serveEmbeddedAnalyzerExecutionControl(writer http.ResponseWriter,
	request *http.Request, requestID string, runID string,
) {
	if !a.authorizeRunOperation(writer, request, requestID,
		a.embeddedAnalyzerExecutionEnabled, "Embedded analyzer execution") {
		return
	}
	if err := validatePathIdentity(runID); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if err := rejectQuery(request.URL.Query()); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if err := validateJSONContentType(request.Header); err != nil {
		a.writeError(writer, requestID, err, http.StatusUnsupportedMediaType)
		return
	}
	body, err := readBoundedRequestBody(request, MaxEmbeddedAnalyzerControlBodyBytes)
	if err != nil {
		status := 0
		if apperror.CodeOf(apperror.Normalize(err)) == apperror.CodeResourceExhausted {
			status = http.StatusRequestEntityTooLarge
		}
		a.writeError(writer, requestID, err, status)
		return
	}
	if !utf8.Valid(body) {
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
			"embedded analyzer body must be valid UTF-8 JSON"), 0)
		return
	}
	if err := rejectDuplicateJSONObjectFields(body, "Embedded analyzer execution"); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	var view EmbeddedAnalyzerExecutionRequestView
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&view); err != nil {
		a.writeError(writer, requestID, apperror.Wrap(apperror.CodeInvalidArgument,
			"embedded analyzer body must be one JSON object", err), 0)
		return
	}
	if err := ensureJSONEOF(decoder); err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if strings.TrimSpace(view.Version) != application.EmbeddedAnalyzerExecutionProtocolVersion ||
		strings.TrimSpace(view.Confirmation) != application.EmbeddedAnalyzerExecutionConfirmation ||
		(view.Text == "") == (strings.TrimSpace(view.File) == "") {
		a.writeError(writer, requestID, apperror.New(apperror.CodeInvalidArgument,
			"embedded analyzer request, input, or explicit confirmation is invalid"), 0)
		return
	}
	result, err := a.embeddedAnalyzerExecutionController.Execute(request.Context(),
		application.EmbeddedAnalyzerExecutionRequest{
			ProtocolVersion: view.Version,
			RunID:           runID,
			Text:            view.Text,
			File:            view.File,
			MediaType:       view.MediaType,
			RequestedBy:     "http_control",
			Confirmation:    view.Confirmation,
		})
	if err != nil {
		a.writeError(writer, requestID, err, 0)
		return
	}
	if result.Record.RunID != runID || result.Artifact.RunID != runID ||
		result.Record.ArtifactID != result.Artifact.ID ||
		result.Record.RequestID != result.Result.RequestID ||
		!result.Record.CapabilityConsumed || !result.Record.ArtifactAtomic ||
		result.Record.RawRequestIncluded || result.Record.BearerTokenIncluded ||
		result.Record.FilesystemMounted || result.Record.NetworkEnabled ||
		result.Record.SubprocessEnabled || result.Record.HostProcessAuthorized ||
		!result.Result.MetadataOnly || result.Result.Status != "succeeded" {
		a.writeError(writer, requestID, apperror.New(apperror.CodeInternal,
			"embedded analyzer result violated its fixed execution boundary"), 0)
		return
	}
	a.writeSuccessStatus(writer, requestID, EmbeddedAnalyzerExecutionControlView{
		Version:     EmbeddedAnalyzerExecutionControlVersion,
		ExecutionID: result.Record.ID, ArtifactID: result.Artifact.ID,
		RunID: runID, SessionID: result.Record.SessionID, WorkspaceID: result.Record.WorkspaceID,
		Analyzer: result.Result.Analyzer, Status: result.Result.Status,
		MediaType: result.Result.Summary.MediaType, InputBytes: result.Result.Summary.InputBytes,
		LineCount: result.Result.Summary.LineCount, SHA256: result.Result.Summary.SHA256,
		UTF8: result.Result.Summary.UTF8, MetadataOnly: result.Result.MetadataOnly,
		CapabilityConsumed:    result.Record.CapabilityConsumed,
		ArtifactAtomic:        result.Record.ArtifactAtomic,
		FilesystemMounted:     result.Record.FilesystemMounted,
		NetworkEnabled:        result.Record.NetworkEnabled,
		SubprocessEnabled:     result.Record.SubprocessEnabled,
		HostProcessAuthorized: result.Record.HostProcessAuthorized,
		RawRequestIncluded:    result.Record.RawRequestIncluded,
		BearerTokenIncluded:   result.Record.BearerTokenIncluded,
		Replayed:              result.Replayed,
	}, nil, http.StatusCreated)
}
