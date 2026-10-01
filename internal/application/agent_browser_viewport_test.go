package application

import (
	"context"
	"errors"
	"strings"
	"testing"

	"cyberagent-workbench/internal/browserruntime"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/toolgateway"
)

type agentBrowserViewportProbe struct {
	*agentBrowserTestRuntime
	viewport browserruntime.AgentBrowserViewport
}

func (r *agentBrowserViewportProbe) NavigateWithViewport(ctx context.Context, url string, v browserruntime.AgentBrowserViewport) (browserruntime.AgentBrowserNavigation, error) {
	r.viewport = v
	n, err := r.Navigate(ctx, url)
	n.Viewport = &v
	return n, err
}

func TestAgentBrowserViewportDurableDispatchAndNoFallback(t *testing.T) {
	for _, supported := range []bool{true, false} {
		t.Run(map[bool]string{true: "supported", false: "unsupported"}[supported], func(t *testing.T) {
			service, st, runtime, s, turn := newAgentBrowserFixture(t)
			probe := &agentBrowserViewportProbe{agentBrowserTestRuntime: runtime}
			launch := service.launch
			if supported {
				service.launch = func(ctx context.Context, req browserruntime.AgentBrowserStartRequest) (agentBrowserRuntime, error) {
					_, err := launch(ctx, req)
					return probe, err
				}
			}
			call := agentBrowserFixtureCall(t, service, st, toolgateway.BrowserNavigateTool, `{"version":"browser_navigate.v2","url":"https://example.org","viewport":{"width":390,"height":844}}`)
			waiting, err := runAgentBrowserFixtureCall(t, s, turn, call)
			if err != nil || waiting {
				t.Fatalf("settlement: %v %v", waiting, err)
			}
			result := st.calls[call.CallID]
			if supported {
				if probe.viewport.Width != 390 || probe.viewport.Height != 844 || len(runtime.actions) != 1 || result.Status != domain.SupervisorToolCompleted || !strings.Contains(result.ResultJSON, `\"width\":390`) {
					t.Fatalf("exact viewport not dispatched/receipted: %+v %+v", probe, result)
				}
			} else if len(runtime.actions) != 0 || result.Status != domain.SupervisorToolFailed {
				t.Fatalf("unsupported viewport silently fell back: %+v %+v", runtime, result)
			}
			// A started call whose completion was lost cannot resize/navigate again.
			st.calls[call.CallID] = call
			if _, err := runAgentBrowserFixtureCall(t, s, turn, call); err != nil || len(runtime.actions) != map[bool]int{true: 1, false: 0}[supported] || !strings.Contains(st.calls[call.CallID].ResultJSON, "outcome_unknown") {
				t.Fatalf("replayed viewport dispatch: %v %+v", err, st.calls[call.CallID])
			}
		})
	}
}

func TestAgentBrowserStaleDiagnosticPreservesUnknownPriority(t *testing.T) {
	stale := normalizeAgentBrowserActionError(browserruntime.ErrAgentBrowserStaleReference)
	if !strings.Contains(stale.Error(), "stale_browser_reference") || !errors.Is(stale, browserruntime.ErrAgentBrowserStaleReference) {
		t.Fatal("actionable stale cause lost")
	}
	unknown := normalizeAgentBrowserActionError(&browserruntime.AgentBrowserActionError{Err: browserruntime.ErrAgentBrowserStaleReference, Dispatched: true, OutcomeUnknown: true})
	if !strings.Contains(unknown.Error(), "outcome_unknown") || strings.Contains(unknown.Error(), "stale_browser_reference") {
		t.Fatalf("partial input could be misclassified as reusable stale ref: %v", unknown)
	}
}
