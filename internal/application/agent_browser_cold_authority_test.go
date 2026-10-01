package application

import (
	"encoding/json"
	"testing"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/toolgateway"
)

func TestAgentBrowserUnusedColdSlotRotatesOnlyForFreshAdvertisement(t *testing.T) {
	service, st, runtime, s, turn := newAgentBrowserFixture(t)
	old, err := service.GetStatus(t.Context(), turn.Run.ID)
	if err != nil || old.SessionID == "" || len(runtime.actions) != 0 {
		t.Fatalf("cold status: %+v %v", old, err)
	}
	oldAuthority, err := service.authority(t.Context(), turn.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	oldCall := agentBrowserFixtureCall(t, service, st, toolgateway.BrowserNavigateTool, `{"version":"browser_navigate.v2","url":"https://example.org"}`)
	// An ordinary quiescent Plan -> Deliver toggle changes the mode revision.
	st.mode.Revision += 2
	turn.Mode = st.mode
	view, err := service.GetStatus(t.Context(), turn.Run.ID)
	if err != nil || view.SessionID != old.SessionID || !view.Capabilities.CanStart || len(runtime.actions) != 0 {
		t.Fatal("read-only status rotated or launched the stale slot")
	}
	caps, raw, err := s.agentBrowserCapabilities(t.Context(), turn)
	if err != nil || !caps.Available {
		t.Fatalf("valid live Full Access lost all browser schemas after cold mode change: %+v %v", caps, err)
	}
	var fresh toolgateway.AgentBrowserCallAuthority
	if json.Unmarshal(raw, &fresh) != nil || fresh.BrowserSessionID == old.SessionID || fresh.ModeRevision != st.mode.Revision || fresh.SessionGeneration <= oldAuthority.SessionGeneration {
		t.Fatalf("stale cold authority reused: %+v", fresh)
	}
	service.mu.Lock()
	retired := service.sessions[old.SessionID]
	cleaned := agentBrowserCleanupComplete(retired)
	service.mu.Unlock()
	if !cleaned || service.check(t.Context(), oldAuthority) == nil {
		t.Fatal("old immutable session remained dispatchable or unaccounted")
	}
	if fresh.RunAuthorizationFence != oldAuthority.RunAuthorizationFence || !service.options.Capabilities.RuntimeAuthority.AllowsRunAuthorizationFence(turn.Run.ID, oldAuthority.RunAuthorizationFence) {
		t.Fatal("creating a fresh browser revoked the Run's sibling capability fence")
	}
	_, rejected := runAgentBrowserFixtureCall(t, s, turn, oldCall)
	if len(runtime.actions) != 0 || st.calls[oldCall.CallID].Status == domain.SupervisorToolCompleted || st.calls[oldCall.CallID].AuthorityJSON != oldCall.AuthorityJSON || (rejected == nil && st.calls[oldCall.CallID].Status != domain.SupervisorToolFailed) {
		t.Fatalf("old durable call dispatched/rebound: err=%v actions=%v call=%+v", rejected, runtime.actions, st.calls[oldCall.CallID])
	}
	newCall := agentBrowserFixtureCall(t, service, st, toolgateway.BrowserNavigateTool, `{"version":"browser_navigate.v2","url":"https://example.org"}`)
	if _, err := runAgentBrowserFixtureCall(t, s, turn, newCall); err != nil || len(runtime.actions) != 1 || st.calls[newCall.CallID].Status != domain.SupervisorToolCompleted {
		t.Fatalf("new exact authority could not navigate: %v %+v", err, st.calls[newCall.CallID])
	}
}

func TestAgentBrowserColdRotationNeverFabricatesLaunchCleanupOrGrant(t *testing.T) {
	for _, reason := range []string{"launch_in_progress", "live_runtime", "revoked_parent"} {
		t.Run(reason, func(t *testing.T) {
			service, st, runtime, s, turn := newAgentBrowserFixture(t)
			old, err := service.GetStatus(t.Context(), turn.Run.ID)
			if err != nil {
				t.Fatal(err)
			}
			service.mu.Lock()
			slot := service.slots[turn.Run.ID]
			if reason == "launch_in_progress" {
				slot.launchAttempted = true
			}
			if reason == "live_runtime" {
				slot.runtime = runtime
			}
			service.mu.Unlock()
			st.mode.Revision += 2
			turn.Mode = st.mode
			if reason == "revoked_parent" {
				st.base.executionPermission.Mode = domain.RunExecutionPermissionConservative
			}
			caps, _, err := s.agentBrowserCapabilities(t.Context(), turn)
			service.mu.Lock()
			stillOld := service.slots[turn.Run.ID] == slot && slot.authority.BrowserSessionID == old.SessionID && slot.view.Cleanup == nil
			service.mu.Unlock()
			if err != nil || caps.Available || !stillOld || len(runtime.actions) != 0 {
				t.Fatalf("unsafe retirement: reason=%s caps=%+v sameOld=%v err=%v", reason, caps, stillOld, err)
			}
			// A synthetic launch marker has no launch cleanup receipt. Restore
			// only the fake test state so the fixture shutdown does not fabricate it.
			service.mu.Lock()
			slot.launchAttempted = false
			service.mu.Unlock()
		})
	}
}

func TestAgentBrowserUnusedColdFenceExpiryKeepsOldStartedCallImmutable(t *testing.T) {
	service, st, runtime, s, turn := newAgentBrowserFixture(t)
	old, err := service.authority(t.Context(), turn.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	call := agentBrowserFixtureCall(t, service, st, toolgateway.BrowserNavigateTool, `{"version":"browser_navigate.v2","url":"https://example.org"}`)
	st.started[call.CallID] = true
	if _, err := service.options.Capabilities.RuntimeAuthority.RotateRunAuthorizationFence(turn.Run.ID); err != nil {
		t.Fatal(err)
	}
	caps, raw, err := s.agentBrowserCapabilities(t.Context(), turn)
	var fresh toolgateway.AgentBrowserCallAuthority
	if err != nil || !caps.Available || json.Unmarshal(raw, &fresh) != nil || fresh.BrowserSessionID == old.BrowserSessionID || fresh.RunAuthorizationFence == old.RunAuthorizationFence {
		t.Fatalf("expired cold fence not refreshed: %v %+v", err, caps)
	}
	_, _ = runAgentBrowserFixtureCall(t, s, turn, call)
	if len(runtime.actions) != 0 || st.calls[call.CallID].Status == domain.SupervisorToolCompleted || st.calls[call.CallID].AuthorityJSON != call.AuthorityJSON || !st.started[call.CallID] {
		t.Fatal("started old call was rebound or redispatched")
	}
}
