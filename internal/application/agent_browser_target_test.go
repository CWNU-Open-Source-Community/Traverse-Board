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

type agentBrowserTargetProbe struct {
	*agentBrowserTestRuntime
	ref      string
	rejected bool
}

func (r *agentBrowserTargetProbe) ResolveSnapshotTarget(_ context.Context, snapshot, name, role string) (string, error) {
	if r.rejected || snapshot != "snapshot-live" || name != "加入队列" || role != "button" {
		return "", browserruntime.ErrAgentBrowserTargetChanged
	}
	return "submit-ref", nil
}
func (r *agentBrowserTargetProbe) Click(ctx context.Context, snapshot, ref string) (browserruntime.AgentBrowserInteraction, error) {
	r.ref = ref
	return r.agentBrowserTestRuntime.Click(ctx, snapshot, ref)
}
func TestAgentBrowserExactTargetDispatchRetainsDurableAndSensitiveGates(t *testing.T) {
	for _, rejected := range []bool{false, true} {
		t.Run(map[bool]string{false: "unique", true: "ambiguous"}[rejected], func(t *testing.T) {
			service, st, r, s, turn := newAgentBrowserFixture(t)
			probe := &agentBrowserTargetProbe{agentBrowserTestRuntime: r, rejected: rejected}
			launch := service.launch
			service.launch = func(ctx context.Context, request browserruntime.AgentBrowserStartRequest) (agentBrowserRuntime, error) {
				_, err := launch(ctx, request)
				return probe, err
			}
			nav := agentBrowserFixtureCall(t, service, st, toolgateway.BrowserNavigateTool, `{"version":"browser_navigate.v2","url":"https://example.org"}`)
			if _, err := runAgentBrowserFixtureCall(t, s, turn, nav); err != nil {
				t.Fatal(err)
			}
			call := agentBrowserFixtureCall(t, service, st, toolgateway.BrowserClickTool, `{"version":"browser_click.v2","snapshot_id":"snapshot-live","target":{"name":"加入队列","role":"button"},"sensitive_intent":{"version":"browser_sensitive_intent.v1","effect":"external_write","target":"https://example.org","description":"Submit the approved message","document_epoch":1}}`)
			waiting, err := runAgentBrowserFixtureCall(t, s, turn, call)
			if err != nil || !waiting || len(r.actions) != 1 {
				t.Fatalf("named target bypassed approval: %v %v %v", waiting, err, r.actions)
			}
			records := st.approvals[call.CallID]
			records.Status = "approved"
			st.approvals[call.CallID] = records
			if _, err = runAgentBrowserFixtureCall(t, s, turn, call); err != nil {
				t.Fatal(err)
			}
			settled := st.calls[call.CallID]
			expected := 2
			if rejected {
				expected = 1
				if settled.Status != domain.SupervisorToolFailed {
					t.Fatal("ambiguous named input succeeded")
				}
			} else if probe.ref != "submit-ref" || settled.Status != domain.SupervisorToolCompleted {
				t.Fatal("wrong opaque ref dispatched")
			}
			if len(r.actions) != expected {
				t.Fatal("unexpected dispatch count")
			}
			st.calls[call.CallID] = call
			if _, err = runAgentBrowserFixtureCall(t, s, turn, call); err != nil || len(r.actions) != expected || !strings.Contains(st.calls[call.CallID].ResultJSON, "outcome_unknown") {
				t.Fatalf("started named input repeated: %v", err)
			}
		})
	}
	if !errors.Is(normalizeAgentBrowserActionError(browserruntime.ErrAgentBrowserTargetChanged), browserruntime.ErrAgentBrowserTargetChanged) {
		t.Fatal("target refusal cause lost")
	}
}
