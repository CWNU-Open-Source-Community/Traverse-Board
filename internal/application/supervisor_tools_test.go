package application

import (
	"encoding/json"
	"strings"
	"testing"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/toolgateway"
)

func TestSupervisorUnavailableWebSearchRejectionExplainsOperatorPath(t *testing.T) {
	// Default network permissions advertise per-call authorized web fetch
	// without search. A provider that still calls web_search must receive a
	// durable reason naming the operator path and stopping the per-round retry
	// loop, instead of the bare 2026-09-14 acceptance failure.
	capabilities := toolgateway.WebEvidenceCapabilities{
		ProtocolVersion: toolgateway.WebEvidenceRegistryVersion,
		Available:       true, FetchAvailable: true, SearchAvailable: false,
	}
	options := supervisorToolOptions{WebEvidence: supervisorWebEvidenceTools{
		Capabilities: capabilities,
		Authority:    json.RawMessage(`{"version":"` + toolgateway.WebEvidenceRegistryVersion + `"}`),
	}}
	_, err := prepareSupervisorToolCalls([]llm.ToolCall{{
		ID: "provider-call-web-search", Name: string(toolgateway.WebSearchTool),
		Arguments: json.RawMessage(`{"version":"web_search.v1","query":"Riemann Hypothesis latest progress","limit":3}`),
	}}, "run-web-rejection", 1, 1, domain.ExecutionSurfaceCode,
		domain.ExecutionPhaseDeliver, domain.RunExecutionPermissionConservative,
		false, false, options)
	if err == nil {
		t.Fatal("unavailable web_search was accepted")
	}
	if !strings.HasPrefix(err.Error(),
		`provider requested unavailable web evidence tool "web_search"`) {
		t.Fatalf("stable rejection prefix changed: %v", err)
	}
	for _, want := range []string{
		"not opened for the current Run",
		"conversation permissions",
		"Do not retry web_search",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("rejection omits %q: %v", want, err)
		}
	}
	// The repair reason parses round=N before the first "; " separator, so the
	// appended explanation must not embed that separator itself.
	if strings.Contains(err.Error(), "; ") {
		t.Fatalf("rejection reason must not embed round separators: %v", err)
	}
	// The same Run still offers web_fetch: the availability gate must pass it
	// through, so the call proceeds to later authority decoding instead.
	_, fetchErr := prepareSupervisorToolCalls([]llm.ToolCall{{
		ID: "provider-call-web-fetch", Name: string(toolgateway.WebFetchTool),
		Arguments: json.RawMessage(`{"version":"web_fetch.v1","url":"https://example.com/article"}`),
	}}, "run-web-rejection", 1, 1, domain.ExecutionSurfaceCode,
		domain.ExecutionPhaseDeliver, domain.RunExecutionPermissionConservative,
		false, false, options)
	if fetchErr == nil || strings.Contains(fetchErr.Error(),
		"unavailable web evidence tool") {
		t.Fatalf("offered web_fetch hit the availability rejection: %v", fetchErr)
	}
}

func TestSupervisorUnavailableWebFetchRejectionExplainsOperatorPath(t *testing.T) {
	options := supervisorToolOptions{WebEvidence: supervisorWebEvidenceTools{
		Capabilities: toolgateway.WebEvidenceCapabilities{
			Available: true, SearchAvailable: true, FetchAvailable: false},
		Authority: json.RawMessage(`{"version":"` + toolgateway.WebEvidenceRegistryVersion + `"}`),
	}}
	_, err := prepareSupervisorToolCalls([]llm.ToolCall{{
		ID: "provider-call-web-fetch", Name: string(toolgateway.WebFetchTool),
		Arguments: json.RawMessage(`{"version":"web_fetch.v1","url":"https://example.com/article"}`),
	}}, "run-fetch-closed", 1, 1, domain.ExecutionSurfaceCode,
		domain.ExecutionPhaseDeliver, domain.RunExecutionPermissionConservative,
		false, false, options)
	if err == nil ||
		!strings.HasPrefix(err.Error(),
			`provider requested unavailable web evidence tool "web_fetch"`) ||
		!strings.Contains(err.Error(), "web fetch is not opened for the current Run") ||
		!strings.Contains(err.Error(), "Do not retry this tool until it is opened") {
		t.Fatalf("web_fetch rejection lost its operator path: %v", err)
	}
}

func TestSupervisorClosedWebEvidenceRejectionKeepsGenericReason(t *testing.T) {
	_, err := prepareSupervisorToolCalls([]llm.ToolCall{{
		ID: "provider-call-web-search", Name: string(toolgateway.WebSearchTool),
		Arguments: json.RawMessage(`{"version":"web_search.v1","query":"bounded query","limit":1}`),
	}}, "run-web-closed", 1, 1, domain.ExecutionSurfaceCode,
		domain.ExecutionPhaseDeliver, domain.RunExecutionPermissionConservative,
		false, false)
	if err == nil {
		t.Fatal("web_search was accepted without any web evidence advertisement")
	}
	if !strings.HasPrefix(err.Error(),
		`provider requested unavailable web evidence tool "web_search"`) ||
		!strings.Contains(err.Error(), "web evidence tools are not open") ||
		!strings.Contains(err.Error(), "Do not retry this tool until it is opened") {
		t.Fatalf("closed web evidence rejection lost its reason: %v", err)
	}
}
