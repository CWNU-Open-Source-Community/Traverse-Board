package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/toolgateway"
	"cyberagent-workbench/internal/webevidence"
)

type WebEvidenceToolStore interface {
	webevidence.Store
	GetRun(context.Context, string) (domain.Run, error)
	GetRunMode(context.Context, string) (domain.RunModeSnapshot, error)
	GetRunExecutionPermission(context.Context, string) (domain.RunExecutionPermissionSnapshot, error)
}

type WebEvidenceToolExecutor struct {
	store                                 WebEvidenceToolStore
	service                               *webevidence.Service
	webFetchAuthorizationSchedulerEnabled bool
}

var errWebFetchWaitingApproval = errors.New("web fetch is waiting for exact host approval")

type webFetchInlineAuthorizationStore interface {
	PrepareWebFetchAuthorization(context.Context, domain.WebFetchAuthorizationRequest) (
		domain.WebFetchAuthorization, bool, error)
	ConsumeWebFetchAuthorization(context.Context, string, string) error
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
	HTTPStatus            int                     `json:"http_status,omitempty"`
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

func (e *WebEvidenceToolExecutor) WithWebFetchAuthorizationScheduler(
	enabled bool,
) *WebEvidenceToolExecutor {
	if e != nil {
		e.webFetchAuthorizationSchedulerEnabled = enabled
	}
	return e
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
	run, err := e.store.GetRun(ctx, scope.RunID)
	if err != nil {
		return toolgateway.WebEvidenceExecutionResult{}, apperror.Normalize(err)
	}
	if run.ID != scope.RunID || run.MissionID != scope.MissionID ||
		run.SessionID != scope.SessionID {
		return toolgateway.WebEvidenceExecutionResult{}, apperror.New(
			apperror.CodeFailedPrecondition,
			"web evidence Run route no longer matches the Supervisor scope")
	}
	networkAuthority := effectiveWebEvidenceAuthority(mode.Scope, permission.Mode)
	providerFingerprint := e.service.SearchProviderFingerprintForScope(ctx,
		webevidence.ExecutionScope{RunID: scope.RunID, MissionID: scope.MissionID,
			WorkspaceID: scope.WorkspaceID, ModelRoute: run.Config.ModelRoute,
			Authority: networkAuthority})
	providerIndependent := e.service.SearchProviderIndependentForScope(ctx,
		webevidence.ExecutionScope{RunID: scope.RunID, MissionID: scope.MissionID,
			WorkspaceID: scope.WorkspaceID, ModelRoute: run.Config.ModelRoute,
			Authority: networkAuthority})
	capabilityContext := toolgateway.WebEvidenceCapabilityContext{
		RunID: scope.RunID, MissionID: scope.MissionID, SessionID: scope.SessionID,
		RootAgentID: scope.RootAgentID, WorkspaceID: scope.WorkspaceID,
		Surface: mode.Surface, Phase: mode.Phase, Role: scope.Role, Profile: mode.Profile,
		PermissionMode: permission.Mode, PermissionRevision: permission.Revision,
		ModeRevision: mode.Revision, NetworkMode: networkAuthority.Mode,
		AllowedTargets:                  append([]string(nil), networkAuthority.AllowedTargets...),
		ProviderAvailable:               providerFingerprint != "",
		ProviderFingerprint:             providerFingerprint,
		ProviderSearchIndependent:       providerIndependent,
		InlineWebFetchApprovalAvailable: e.webFetchAuthorizationSchedulerEnabled}
	capabilities := toolgateway.WebEvidenceCapabilitySnapshot(capabilityContext)
	if mode.RunID != scope.RunID || mode.MissionID != scope.MissionID ||
		mode.Revision != scope.ModeRevision || permission.Mode != scope.PermissionMode ||
		permission.Revision != scope.PermissionRevision || mode.Surface != scope.Surface ||
		mode.Phase != scope.Phase || mode.Profile != scope.Profile || !capabilities.Available ||
		(name == toolgateway.WebSearchTool && !capabilities.SearchAvailable) ||
		(name != toolgateway.WebSearchTool && !capabilities.FetchAvailable) ||
		capabilities.Generation != scope.CapabilityGeneration {
		return toolgateway.WebEvidenceExecutionResult{}, apperror.New(
			apperror.CodeFailedPrecondition,
			"web evidence authority no longer matches the active Run policy")
	}
	executionScope := webevidence.ExecutionScope{RunID: scope.RunID,
		MissionID: scope.MissionID, WorkspaceID: scope.WorkspaceID,
		ModelRoute: run.Config.ModelRoute, Authority: networkAuthority,
		RobotsPolicy:        effectiveWebEvidenceRobotsPolicy(permission.Mode),
		ProviderFingerprint: scope.ProviderFingerprint}
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
		providerGrounded := result.HasProviderGroundedCitations()
		provenance := "discovery"
		if providerGrounded {
			provenance = webevidence.ProviderGroundedProvenance
		}
		return toolgateway.WebEvidenceExecutionResult{Content: string(content),
			Metadata: map[string]string{"provider": result.Provider,
				"search_policy":      result.SearchPolicy,
				"selection_reason":   result.SelectionReason,
				"source_count":       strconv.Itoa(len(result.Sources)),
				"searched_at":        result.SearchedAt.Format("2006-01-02T15:04:05.999999999Z07:00"),
				"replayed":           strconv.FormatBool(replayed),
				"citeable":           strconv.FormatBool(providerGrounded),
				"provenance":         provenance,
				"provider_qualified": strconv.FormatBool(providerGrounded),
				"locally_verified":   "false", "untrusted": "true",
				"instruction_authorized": "false"}}, nil
	case toolgateway.WebFetchTool:
		var request toolgateway.WebFetchPayload
		if err := json.Unmarshal(payload, &request); err != nil {
			return toolgateway.WebEvidenceExecutionResult{}, err
		}
		canonicalURL := request.URL
		if request.SourceID != "" {
			source, sourceErr := e.store.GetWebSource(ctx, scope.RunID, request.SourceID)
			if sourceErr != nil {
				return toolgateway.WebEvidenceExecutionResult{}, apperror.Normalize(sourceErr)
			}
			canonicalURL = source.CanonicalURL
		}
		inlineAuthorization, inlineErr := e.authorizeInlineWebFetch(ctx, scope,
			executionScope.Authority, canonicalURL)
		if inlineErr != nil {
			return toolgateway.WebEvidenceExecutionResult{}, inlineErr
		}
		if inlineAuthorization.ExactTarget != "" {
			// An inline grant is a subordinate authority for this exact execution,
			// not a mutation of the persisted Run authority. Conservative mode can
			// legitimately start with network disabled; merely appending a host to
			// that disabled authority would still make the approved retry fail.
			executionScope.Authority = webevidence.NetworkAuthority{Mode: "allowlist",
				AllowedTargets: []string{inlineAuthorization.ExactTarget}}
		}
		result, err := e.service.Fetch(ctx, executionScope, webevidence.FetchRequest{
			SourceID: request.SourceID, URL: request.URL}, scope.OperationKey)
		if err != nil {
			return toolgateway.WebEvidenceExecutionResult{}, err
		}
		if inlineAuthorization.ID != "" &&
			inlineAuthorization.Scope == domain.WebFetchAuthorizationOnce {
			if store, ok := e.store.(webFetchInlineAuthorizationStore); ok {
				if err := store.ConsumeWebFetchAuthorization(ctx, scope.RunID,
					scope.SupervisorToolCallID); err != nil {
					return toolgateway.WebEvidenceExecutionResult{}, apperror.Normalize(err)
				}
			}
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
				"robots":                 result.Snapshot.Robots,
				"http_status":            strconv.Itoa(result.Snapshot.HTTPStatus),
				"redirects":              strconv.Itoa(result.Snapshot.Redirects),
				"robots_policy":          string(executionScope.RobotsPolicy),
				"partial":                strconv.FormatBool(result.Snapshot.State == webevidence.SourcePartial),
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

func (e *WebEvidenceToolExecutor) authorizeInlineWebFetch(ctx context.Context,
	scope toolgateway.WebEvidenceExecutionScope, authority webevidence.NetworkAuthority,
	canonicalURL string,
) (domain.WebFetchAuthorization, error) {
	canonicalURL, err := webevidence.CanonicalizePublicHTTPSURL(canonicalURL)
	if err != nil {
		return domain.WebFetchAuthorization{}, apperror.Wrap(
			apperror.CodePolicyDenied, "web fetch target is outside public HTTPS", err)
	}
	if _, err := authority.Authorize(canonicalURL); err == nil {
		return domain.WebFetchAuthorization{}, nil
	}
	if !e.webFetchAuthorizationSchedulerEnabled {
		return domain.WebFetchAuthorization{}, apperror.New(
			apperror.CodePolicyDenied, "inline web fetch approval scheduler is unavailable")
	}
	// Inline approval is deliberately limited to the two user-mediated modes.
	// Workspace Access remains networkless, while Full/Debug receive their
	// separately projected public-HTTPS authority before reaching this path.
	if scope.PermissionMode != domain.RunExecutionPermissionConservative &&
		scope.PermissionMode != domain.RunExecutionPermissionApproval {
		return domain.WebFetchAuthorization{}, apperror.New(
			apperror.CodePolicyDenied, "web fetch target is outside Run network authority")
	}
	parsed, err := url.Parse(canonicalURL)
	if err != nil || parsed.Hostname() == "" {
		return domain.WebFetchAuthorization{}, apperror.New(
			apperror.CodePolicyDenied, "web fetch target host is invalid")
	}
	exactTarget := strings.ToLower(parsed.Hostname())
	// Re-authorize through the normal transport policy before asking the user.
	// This ensures loopback/private/metadata names and non-HTTPS targets can
	// never be made approvable by this control surface.
	if _, err := (webevidence.NetworkAuthority{Mode: "allowlist",
		AllowedTargets: []string{exactTarget}}).Authorize(canonicalURL); err != nil {
		return domain.WebFetchAuthorization{}, apperror.Wrap(
			apperror.CodePolicyDenied, "web fetch target cannot be approved", err)
	}
	store, ok := e.store.(webFetchInlineAuthorizationStore)
	if !ok {
		return domain.WebFetchAuthorization{}, apperror.New(
			apperror.CodePolicyDenied, "inline web fetch approval is unavailable")
	}
	fingerprint := approval.Fingerprint(domain.WebFetchAuthorizationProtocolVersion,
		scope.RunID, scope.SessionID, scope.SupervisorToolCallID, canonicalURL, exactTarget)
	value, authorized, err := store.PrepareWebFetchAuthorization(ctx,
		domain.WebFetchAuthorizationRequest{ID: idgen.New("web-fetch-approval"),
			RunID: scope.RunID, MissionID: scope.MissionID,
			SessionID: scope.SessionID, WorkspaceID: scope.WorkspaceID,
			SupervisorTurn:       scope.SupervisorTurn,
			SupervisorToolCallID: scope.SupervisorToolCallID,
			CanonicalURL:         canonicalURL, ExactTarget: exactTarget,
			RequestFingerprint: fingerprint, RequestedBy: scope.RequestedBy})
	if err != nil {
		return domain.WebFetchAuthorization{}, apperror.Normalize(err)
	}
	if authorized {
		return value, nil
	}
	if value.Status == domain.WebFetchAuthorizationDenied {
		return domain.WebFetchAuthorization{}, apperror.New(
			apperror.CodePolicyDenied, "operator denied this public HTTPS host")
	}
	return domain.WebFetchAuthorization{}, errWebFetchWaitingApproval
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
				HTTPStatus:           result.Snapshot.HTTPStatus,
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
