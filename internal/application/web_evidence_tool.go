package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/toolgateway"
	"cyberagent-workbench/internal/webevidence"
)

type WebEvidenceToolStore interface {
	webevidence.Store
	GetRunMode(context.Context, string) (domain.RunModeSnapshot, error)
	GetRunExecutionPermission(context.Context, string) (domain.RunExecutionPermissionSnapshot, error)
}

type WebEvidenceToolExecutor struct {
	store   WebEvidenceToolStore
	service *webevidence.Service
}

type webFetchToolSnapshot struct {
	SourceID              string                  `json:"source_id"`
	SnapshotID            string                  `json:"snapshot_id"`
	URL                   string                  `json:"url"`
	Title                 string                  `json:"title,omitempty"`
	Byline                string                  `json:"byline,omitempty"`
	PublishedAt           string                  `json:"published_at,omitempty"`
	FetchedAt             string                  `json:"fetched_at"`
	StaleAt               string                  `json:"stale_at"`
	Digest                string                  `json:"digest"`
	MIME                  string                  `json:"mime"`
	Charset               string                  `json:"charset,omitempty"`
	Body                  string                  `json:"body,omitempty"`
	State                 webevidence.SourceState `json:"state"`
	Robots                string                  `json:"robots"`
	ErrorCode             string                  `json:"error_code,omitempty"`
	Redirects             int                     `json:"redirects"`
	SnapshotTruncated     bool                    `json:"snapshot_truncated"`
	BodyExcerptTruncated  bool                    `json:"body_excerpt_truncated"`
	Citeable              bool                    `json:"citeable"`
	Untrusted             bool                    `json:"untrusted"`
	InstructionAuthorized bool                    `json:"instruction_authorized"`
}

type webFetchToolOutput struct {
	ProtocolVersion string                         `json:"protocol_version"`
	Source          webevidence.SourcePresentation `json:"source"`
	Snapshot        webFetchToolSnapshot           `json:"snapshot"`
	Replayed        bool                           `json:"replayed"`
}

func NewWebEvidenceToolExecutor(store WebEvidenceToolStore,
	service *webevidence.Service,
) (*WebEvidenceToolExecutor, error) {
	if store == nil || service == nil {
		return nil, errors.New("web evidence tool dependencies are required")
	}
	return &WebEvidenceToolExecutor{store: store, service: service}, nil
}

func (e *WebEvidenceToolExecutor) ExecuteWebEvidence(ctx context.Context,
	scope toolgateway.WebEvidenceExecutionScope, name toolgateway.ToolName,
	payload json.RawMessage,
) (toolgateway.WebEvidenceExecutionResult, error) {
	if e == nil || e.store == nil || e.service == nil {
		return toolgateway.WebEvidenceExecutionResult{}, apperror.New(
			apperror.CodeFailedPrecondition, "web evidence executor is unavailable")
	}
	if err := scope.Validate(); err != nil {
		return toolgateway.WebEvidenceExecutionResult{}, apperror.Wrap(
			apperror.CodeFailedPrecondition, "web evidence Supervisor scope is invalid", err)
	}
	mode, err := e.store.GetRunMode(ctx, scope.RunID)
	if err != nil {
		return toolgateway.WebEvidenceExecutionResult{}, apperror.Normalize(err)
	}
	permission, err := e.store.GetRunExecutionPermission(ctx, scope.RunID)
	if err != nil {
		return toolgateway.WebEvidenceExecutionResult{}, apperror.Normalize(err)
	}
	networkAuthority := webevidence.NetworkAuthority{Mode: mode.Scope.NetworkMode,
		AllowedTargets: append([]string(nil), mode.Scope.AllowedTargets...)}
	providerFingerprint := e.service.SearchProviderFingerprintFor(networkAuthority)
	capabilityContext := toolgateway.WebEvidenceCapabilityContext{
		RunID: scope.RunID, MissionID: scope.MissionID, SessionID: scope.SessionID,
		RootAgentID: scope.RootAgentID, WorkspaceID: scope.WorkspaceID,
		Surface: mode.Surface, Phase: mode.Phase, Role: scope.Role, Profile: mode.Profile,
		PermissionMode: permission.Mode, PermissionRevision: permission.Revision,
		ModeRevision: mode.Revision, NetworkMode: mode.Scope.NetworkMode,
		AllowedTargets:      append([]string(nil), mode.Scope.AllowedTargets...),
		ProviderAvailable:   providerFingerprint != "",
		ProviderFingerprint: providerFingerprint}
	capabilities := toolgateway.WebEvidenceCapabilitySnapshot(capabilityContext)
	if mode.RunID != scope.RunID || mode.MissionID != scope.MissionID ||
		mode.Revision != scope.ModeRevision || permission.Mode != scope.PermissionMode ||
		permission.Revision != scope.PermissionRevision || mode.Surface != scope.Surface ||
		mode.Phase != scope.Phase || mode.Profile != scope.Profile || !capabilities.Available ||
		capabilities.Generation != scope.CapabilityGeneration {
		return toolgateway.WebEvidenceExecutionResult{}, apperror.New(
			apperror.CodeFailedPrecondition,
			"web evidence authority no longer matches the active Run policy")
	}
	executionScope := webevidence.ExecutionScope{RunID: scope.RunID,
		MissionID: scope.MissionID, WorkspaceID: scope.WorkspaceID,
		Authority: networkAuthority}
	switch name {
	case toolgateway.WebSearchTool:
		var request toolgateway.WebSearchPayload
		if err := json.Unmarshal(payload, &request); err != nil {
			return toolgateway.WebEvidenceExecutionResult{}, err
		}
		result, err := e.service.Search(ctx, executionScope, webevidence.SearchRequest{
			Query: request.Query, Limit: request.Limit}, scope.OperationKey)
		if err != nil {
			return toolgateway.WebEvidenceExecutionResult{}, err
		}
		replayed := result.Replayed
		result.Replayed = false
		content, _ := json.Marshal(result)
		return toolgateway.WebEvidenceExecutionResult{Content: string(content),
			Metadata: map[string]string{"provider": result.Provider,
				"source_count": strconv.Itoa(len(result.Sources)),
				"searched_at":  result.SearchedAt.Format("2006-01-02T15:04:05.999999999Z07:00"),
				"replayed":     strconv.FormatBool(replayed), "citeable": "false"}}, nil
	case toolgateway.WebFetchTool:
		var request toolgateway.WebFetchPayload
		if err := json.Unmarshal(payload, &request); err != nil {
			return toolgateway.WebEvidenceExecutionResult{}, err
		}
		result, err := e.service.Fetch(ctx, executionScope, webevidence.FetchRequest{
			SourceID: request.SourceID, URL: request.URL}, scope.OperationKey)
		if err != nil {
			return toolgateway.WebEvidenceExecutionResult{}, err
		}
		replayed := result.Replayed
		result.Replayed = false
		presentation := webevidence.PresentSnapshot(result.Snapshot, time.Now().UTC())
		content, bodyExcerptTruncated, err := encodeWebFetchToolOutput(result, presentation)
		if err != nil {
			return toolgateway.WebEvidenceExecutionResult{}, apperror.Wrap(
				apperror.CodeInternal, "encode bounded Web fetch result", err)
		}
		return toolgateway.WebEvidenceExecutionResult{Content: string(content),
			Truncated: result.Snapshot.Truncated || bodyExcerptTruncated,
			Metadata: map[string]string{"source_id": result.Source.ID,
				"snapshot_id": result.Snapshot.ID,
				"url":         presentation.URL, "title": presentation.Title,
				"fetched_at": presentation.FetchedAt.Format("2006-01-02T15:04:05.999999999Z07:00"),
				"stale_at":   presentation.StaleAt.Format("2006-01-02T15:04:05.999999999Z07:00"),
				"digest":     presentation.Digest, "state": presentation.Status,
				"robots": result.Snapshot.Robots, "partial": strconv.FormatBool(result.Snapshot.State == webevidence.SourcePartial),
				"stale":                  strconv.FormatBool(presentation.Stale),
				"body_excerpt_truncated": strconv.FormatBool(bodyExcerptTruncated),
				"replayed":               strconv.FormatBool(replayed),
				"citeable":               strconv.FormatBool(presentation.Citeable),
				"untrusted":              "true", "instruction_authorized": "false"}}, nil
	case toolgateway.WebCitationTool:
		var request toolgateway.WebCitationPayload
		if err := json.Unmarshal(payload, &request); err != nil {
			return toolgateway.WebEvidenceExecutionResult{}, err
		}
		result, err := e.service.Cite(ctx, executionScope, webevidence.CiteRequest{
			SourceID: request.SourceID, SnapshotID: request.SnapshotID,
			Claim: request.Claim, SpanStart: request.SpanStart, SpanEnd: request.SpanEnd},
			scope.OperationKey)
		if err != nil {
			return toolgateway.WebEvidenceExecutionResult{}, err
		}
		replayed := result.Replayed
		result.Replayed = false
		content, _ := json.Marshal(result)
		citation := result.Citation
		presentation := webevidence.PresentCitation(citation, time.Now().UTC())
		return toolgateway.WebEvidenceExecutionResult{Content: string(content),
			Metadata: map[string]string{"citation_id": citation.ID,
				"source_id": citation.SourceID, "snapshot_id": citation.SnapshotID,
				"url": presentation.URL, "title": presentation.Title,
				"fetched_at": presentation.FetchedAt.Format("2006-01-02T15:04:05.999999999Z07:00"),
				"stale_at":   presentation.StaleAt.Format("2006-01-02T15:04:05.999999999Z07:00"),
				"digest":     presentation.Digest, "partial": strconv.FormatBool(presentation.Partial),
				"stale": strconv.FormatBool(presentation.Stale), "state": presentation.Status,
				"replayed": strconv.FormatBool(replayed), "citeable": "true",
				"untrusted": "true", "instruction_authorized": "false"}}, nil
	default:
		return toolgateway.WebEvidenceExecutionResult{}, apperror.New(
			apperror.CodeInvalidArgument, fmt.Sprintf("unsupported web evidence tool %q", name))
	}
}

func encodeWebFetchToolOutput(result webevidence.FetchResult,
	presentation webevidence.SnapshotPresentation,
) ([]byte, bool, error) {
	body := result.Snapshot.Body
	bodyExcerptTruncated := false
	const reserve = 1024
	for {
		output := webFetchToolOutput{ProtocolVersion: webevidence.FetchProtocolVersion,
			Source: webevidence.PresentSource(result.Source), Replayed: false,
			Snapshot: webFetchToolSnapshot{SourceID: result.Snapshot.SourceID,
				SnapshotID: result.Snapshot.ID, URL: presentation.URL,
				Title: presentation.Title, Byline: result.Snapshot.Byline,
				PublishedAt: result.Snapshot.PublishedAt,
				FetchedAt:   presentation.FetchedAt.Format(time.RFC3339Nano),
				StaleAt:     presentation.StaleAt.Format(time.RFC3339Nano),
				Digest:      presentation.Digest, MIME: result.Snapshot.MIME,
				Charset: result.Snapshot.Charset, Body: body, State: result.Snapshot.State,
				Robots: result.Snapshot.Robots, ErrorCode: result.Snapshot.ErrorCode,
				Redirects:            result.Snapshot.Redirects,
				SnapshotTruncated:    result.Snapshot.Truncated,
				BodyExcerptTruncated: bodyExcerptTruncated,
				Citeable:             presentation.Citeable, Untrusted: true}}
		var buffer bytes.Buffer
		encoder := json.NewEncoder(&buffer)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(output); err != nil {
			return nil, false, err
		}
		encoded := bytes.TrimSuffix(buffer.Bytes(), []byte("\n"))
		if len(encoded) <= toolgateway.MaxResultStdoutBytes-reserve {
			return encoded, bodyExcerptTruncated, nil
		}
		if body == "" {
			return nil, false, errors.New("Web fetch metadata exceeds the tool result limit")
		}
		bodyExcerptTruncated = true
		nextLimit := len([]byte(body)) * 3 / 4
		if nextLimit >= len([]byte(body)) {
			nextLimit = len([]byte(body)) - 1
		}
		body = boundedWebEvidenceBodyPrefix(body, nextLimit)
	}
}

func boundedWebEvidenceBodyPrefix(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	data := []byte(value)
	if len(data) <= maxBytes {
		return value
	}
	data = data[:maxBytes]
	for len(data) > 0 && !utf8.Valid(data) {
		data = data[:len(data)-1]
	}
	return string(data)
}

func webEvidenceServiceForStore(store WebEvidenceToolStore,
	searchEndpoint string,
) *webevidence.Service {
	client := webevidence.NewSafeHTTPClient()
	var provider webevidence.SearchProvider
	if strings.TrimSpace(searchEndpoint) != "" {
		provider, _ = webevidence.NewSearXNGProvider(client, searchEndpoint)
	}
	return webevidence.NewService(store, provider, webevidence.NewFetcher(client))
}
