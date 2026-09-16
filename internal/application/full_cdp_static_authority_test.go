package application

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/session"
	"cyberagent-workbench/internal/toolgateway"
)

func TestFullCDPBrowserToolsHonorStaticAuthorityAndRejectRevokedDynamicGrant(t *testing.T) {
	for _, mode := range []string{"static_debug", "static_full", "dynamic_full"} {
		t.Run(mode, func(t *testing.T) {
			service, baseStore, _, latest := newFullCDPProductionServiceFixture(t)
			if mode == "static_debug" {
				permission, err := baseStore.executionPermission.Next("execution-static-debug",
					domain.RunExecutionPermissionDebug, true, "runtime-operator", "confirmed Debug", time.Now().UTC().Add(time.Millisecond))
				if err != nil {
					t.Fatal(err)
				}
				baseStore.executionPermission = permission
				service.executionCapabilities.DebugMaximumAccessEnabled = true
				service.executionCapabilities.RuntimeAuthority = domain.NewExecutionPermissionRuntimeAuthority()
			} else if mode == "static_full" {
				service.executionCapabilities.FullAccessRequiresRuntimeGrant = false
				service.executionCapabilities.RuntimeAuthority = domain.NewExecutionPermissionRuntimeAuthority()
			}
			service.store = &fakeFullCDPBrowserActionStore{fakeFullCDPProductionStore: baseStore,
				workspace: session.WorkspaceInfo{ID: baseStore.mission.WorkspaceID, Name: "static authority", RootPath: t.TempDir()}}
			t.Cleanup(func() { _ = service.Close(context.Background()) })
			if _, err := service.OpenFullCDPSession(t.Context(), fullCDPOpenFixture(service, baseStore, "open-static-authority")); err != nil {
				t.Fatal(err)
			}
			runtime := &fakeFullCDPBrowserActionRuntime{fakeManagedFullCDPRuntime: *latest, png: []byte("bounded test screenshot")}
			service.mu.Lock()
			service.latestByRun[baseStore.run.ID].runtime = runtime
			service.mu.Unlock()
			binding, live, err := service.browserActionBinding(t.Context(), baseStore.run.ID)
			if err != nil || !live || (binding.executionActivation == 0) != (mode != "dynamic_full") {
				t.Fatalf("wrong live authority semantics: live=%t activation=%d err=%v", live, binding.executionActivation, err)
			}
			supervisor := &RunSupervisor{browserActions: service}
			turn := domain.SupervisorTurn{Run: baseStore.run, Mission: baseStore.mission,
				Agent: domain.AgentNode{ID: "agent-browser-root", Role: domain.AgentRoleRoot},
				Mode:  domain.RunModeSnapshot{Surface: domain.ExecutionSurfaceCode, Phase: domain.ExecutionPhaseDeliver, Profile: domain.ProfileCode, Revision: 1}}
			capability, encoded, err := supervisor.supervisorBrowserActionCapabilities(t.Context(), turn, baseStore.executionPermission)
			if err != nil || !capability.Available || len(encoded) == 0 {
				t.Fatalf("live %s not advertised to model: capability=%+v err=%v", mode, capability, err)
			}
			authority, err := toolgateway.DecodeBrowserActionCallAuthority(encoded)
			if err != nil || authority.PermissionActivation != binding.executionActivation {
				t.Fatalf("authority changed generation semantics: %+v err=%v", authority, err)
			}
			gateway := toolgateway.New(nil, nil).WithBrowserActionExecutor(service)
			call := toolgateway.ToolCall{Name: toolgateway.BrowserScreenshotTool,
				Payload: json.RawMessage(`{"version":"browser_screenshot.v1"}`), OperationKey: "static-browser-screenshot",
				RunID: authority.RunID, MissionID: authority.MissionID, SessionID: authority.SessionID,
				WorkspaceID: authority.WorkspaceID, AgentID: authority.RootAgentID,
				Surface: authority.Surface, Phase: authority.Phase, Role: authority.Role, Profile: authority.Profile,
				PermissionMode: authority.PermissionMode, ModeRevision: authority.ModeRevision,
				PermissionSnapshotID: authority.PermissionSnapshotID, PermissionRevision: authority.PermissionRevision,
				PermissionGeneration: authority.PermissionActivation, RunAuthorizationFence: authority.RunAuthorizationFence,
				CapabilityGeneration: authority.Generation, BrowserActionSessionID: authority.FullCDPSessionID,
				BrowserPermissionSnapshotID: authority.BrowserPermissionSnapshotID, BrowserPermissionRevision: authority.BrowserPermissionRevision,
				RequestedBy: "run_supervisor", LeaseID: "lease-browser-static", LeaseGeneration: 1}
			result, err := gateway.Invoke(t.Context(), call)
			if err != nil || result.Result == nil || result.Result.Metadata["artifact_locator"] == "" || runtime.screenshots != 1 {
				t.Fatalf("visible static browser tool did not execute: result=%+v calls=%d err=%v", result, runtime.screenshots, err)
			}
			service.executionCapabilities.RuntimeAuthority.RevokeRun(baseStore.run.ID)
			capability, _, err = supervisor.supervisorBrowserActionCapabilities(t.Context(), turn, baseStore.executionPermission)
			if err != nil || capability.Available {
				t.Fatalf("revoked authority remained advertised: capability=%+v err=%v", capability, err)
			}
			if mode == "dynamic_full" {
				// Treating the missing activation as zero must not turn a revoked
				// dynamic grant into a valid static grant.
				binding.executionActivation = 0
				forged := browserActionTestScope(binding, "forged-zero-activation")
				call.PermissionGeneration = 0
				call.CapabilityGeneration = forged.CapabilityGeneration
			}
			call.OperationKey = "must-not-execute-after-revocation"
			if _, err := gateway.Invoke(t.Context(), call); err == nil || runtime.screenshots != 1 {
				t.Fatalf("revoked %s executed with zero/static activation: calls=%d err=%v", mode, runtime.screenshots, err)
			}
		})
	}
}
