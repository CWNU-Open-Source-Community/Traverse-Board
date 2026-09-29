package application

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/session"
	"cyberagent-workbench/internal/toolgateway"
)

func TestRunRuntimeBrowserSelectionKeepsExplicitSessionAndOriginalPendingBackend(t *testing.T) {
	ordinary, st, ordinaryRuntime, supervisor, turn := newAgentBrowserFixture(t)
	full, base, _, latest := newFullCDPProductionServiceFixture(t)
	base.run.Status = domain.RunRunning
	st.base = base
	ordinary.options.Capabilities = full.executionCapabilities
	full.store = &fakeFullCDPBrowserActionStore{fakeFullCDPProductionStore: base,
		workspace: session.WorkspaceInfo{ID: base.mission.WorkspaceID, RootPath: t.TempDir()}}
	t.Cleanup(func() { _ = full.Close(context.Background()) })
	supervisor.WithBrowserActions(full)
	turn.Run, turn.Mission = base.run, base.mission
	permission := base.executionPermission

	capability, _, err := supervisor.supervisorBrowserActionCapabilities(t.Context(), turn, permission)
	if err != nil || !capability.Available || capability.ProtocolVersion != toolgateway.AgentBrowserAuthorityVersion {
		t.Fatalf("ordinary browser before explicit open: %+v err=%v", capability, err)
	}
	pending := agentBrowserFixtureCall(t, ordinary, st, toolgateway.BrowserNavigateTool,
		`{"version":"browser_navigate.v2","url":"https://example.com"}`)
	if _, err := full.OpenFullCDPSession(t.Context(), fullCDPOpenFixture(full, base, "runtime-explicit-browser")); err != nil {
		t.Fatal(err)
	}
	full.mu.Lock()
	full.latestByRun[base.run.ID].runtime = &fakeFullCDPBrowserActionRuntime{
		fakeManagedFullCDPRuntime: *latest, png: []byte("fixture pixels")}
	full.mu.Unlock()

	// The same selection and refresh used before EVERY provider request must
	// replace the ordinary v2 schemas with the explicit Full CDP v1 schemas.
	tools := refreshBrowserModelTools(nil, capability)
	for round := 1; round <= 2; round++ {
		var authority json.RawMessage
		capability, authority, err = supervisor.supervisorBrowserActionCapabilities(t.Context(), turn, permission)
		if err != nil || !capability.Available || capability.ProtocolVersion == toolgateway.AgentBrowserAuthorityVersion {
			t.Fatalf("round %d selected wrong backend: %+v err=%v", round, capability, err)
		}
		if _, err := toolgateway.DecodeBrowserActionCallAuthority(authority); err != nil {
			t.Fatal(err)
		}
		tools = refreshBrowserModelTools(tools, capability)
		for _, tool := range tools {
			if tool.Name == string(toolgateway.BrowserScrollTool) || tool.Name == string(toolgateway.BrowserKeyTool) ||
				strings.Contains(string(tool.Parameters), ".v2") {
				t.Fatalf("ordinary tool schema leaked into Full CDP request: %s", tool.Name)
			}
		}
		if len(tools) != len(toolgateway.BrowserActionToolNames()) {
			t.Fatalf("Full CDP tools missing or duplicated: %d", len(tools))
		}
	}

	// Switching the next request's tools never rebinds a persisted v2 call.
	if waiting, err := runAgentBrowserFixtureCall(t, supervisor, turn, pending); err != nil || waiting || len(ordinaryRuntime.actions) != 1 || ordinaryRuntime.actions[0] != "navigate" {
		t.Fatalf("pending ordinary call changed backend: waiting=%t actions=%v err=%v", waiting, ordinaryRuntime.actions, err)
	}
	full.mu.Lock()
	entry := full.latestByRun[base.run.ID]
	sessionID := entry.view.SessionID
	full.mu.Unlock()
	if _, err := full.CloseFullCDPSession(t.Context(), CloseFullCDPSessionRequest{
		RunID: base.run.ID, ExpectedSessionID: sessionID,
		OperationKey: "runtime-explicit-browser-close", Reason: "finish fixture",
	}); err != nil {
		t.Fatal(err)
	}
	if own, err := ordinary.Capabilities(t.Context(), base.run.ID); err != nil || !own.Available {
		t.Fatalf("fixture ordinary adapter should remain independently available: %+v err=%v", own, err)
	}
	capability, _, err = supervisor.supervisorBrowserActionCapabilities(t.Context(), turn, permission)
	if err != nil || capability.Available {
		t.Fatalf("closed explicit session silently fell back: %+v err=%v", capability, err)
	}
	if got := refreshBrowserModelTools(tools, capability); len(got) != 0 {
		t.Fatal("unavailable browser retained tool schemas")
	}
	for _, eviction := range []string{"retention", "capacity"} {
		full.mu.Lock()
		full.latestByRun[base.run.ID] = entry
		if eviction == "retention" {
			full.pruneTerminalCachesLocked(time.Now().Add(2 * fullCDPTerminalRecordRetention))
		} else {
			full.cachePolicy.latestByRunLimit = 1
			err = full.reserveOpenCacheCapacityLocked("run-another-browser")
		}
		retained := full.latestByRun[base.run.ID] != nil
		full.mu.Unlock()
		if err != nil || retained {
			t.Fatalf("%s fixture did not evict its closed entry: retained=%t err=%v", eviction, retained, err)
		}
		capability, _, err = supervisor.supervisorBrowserActionCapabilities(t.Context(), turn, permission)
		if err != nil || capability.Available {
			t.Fatalf("%s eviction switched browser target: %+v err=%v", eviction, capability, err)
		}
	}
	base.executionPermission = permission
	full.executionCapabilities.RuntimeAuthority.RevokeRun(base.run.ID)
	capability, _, err = supervisor.supervisorBrowserActionCapabilities(t.Context(), turn, permission)
	if err != nil || capability.Available {
		t.Fatalf("revoked explicit session silently fell back: %+v err=%v", capability, err)
	}
}

func TestRunRuntimeKeepsDefaultsAndSharedAuthorityWithoutGrant(t *testing.T) {
	caps := domain.ExecutionPermissionRuntimeCapabilities{OperatorApprovalEnabled: true,
		DangerFullAccessEnabled: true, FullAccessRequiresRuntimeGrant: true,
		RuntimeAuthority: domain.NewExecutionPermissionRuntimeAuthority()}
	calls := NewActiveCallRegistry()
	deps := RunRuntimeDependencies{ActiveCalls: calls, ExecutionCapabilities: caps}
	// No Store methods or host resource creation are needed for pure assembly.
	s := NewRunSupervisorWithRuntime(nil, llm.NewRouter(llm.ModelRef{}), nil, deps)
	h := NewRunExecutionHandoffWithRuntime(nil, nil, nil, deps)
	for _, assembled := range []*RunSupervisor{s, h.supervisor} {
		if assembled.activeCalls != calls || assembled.executionCapabilities.RuntimeAuthority != caps.RuntimeAuthority ||
			!assembled.generatedContextCompactionEnabled || assembled.tools == nil || assembled.agentBrowser != nil {
			t.Fatal("runtime assembly replaced shared authority, defaults or optional adapters")
		}
	}
	if _, live := caps.FullAccessGeneration(domain.RunExecutionPermissionSnapshot{RunID: "run-not-confirmed", Mode: domain.RunExecutionPermissionFullAccess}); live {
		t.Fatal("constructing the runtime activated a durable preference")
	}
}
