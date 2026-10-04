package application

import (
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
)

func TestAgentCodeLegacyReadBindingsDoNotAcquireNewRuntimeAuthority(t *testing.T) {
	now := time.Now().UTC()
	mission := domain.Mission{ID: "mission-file-history", CreatedAt: now}
	run := domain.Run{ID: "run-file-history", MissionID: mission.ID, Status: domain.RunCreated, CreatedAt: now}
	initial, err := domain.NewInitialRunExecutionPermissionSnapshot("permission-file-initial", run, mission, "operator", now)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []domain.RunExecutionPermissionMode{domain.RunExecutionPermissionConservative,
		domain.RunExecutionPermissionWorkspaceAccess, domain.RunExecutionPermissionApproval,
		domain.RunExecutionPermissionFullAccess, domain.RunExecutionPermissionDebug} {
		t.Run(string(mode), func(t *testing.T) {
			legacy, err := initial.Next("permission-file-history", mode, mode != domain.RunExecutionPermissionConservative, "operator", "historical reader fixture", now)
			if err != nil || legacy.Validate() != nil {
				t.Fatalf("historical tuple: %+v %v", legacy, err)
			}
			id, generation, epoch, fence, live := bindAgentCodeRuntime(domain.ExecutionPermissionRuntimeCapabilities{}, legacy)
			if live || id != "" || generation != 0 || epoch != "" || fence != 0 {
				t.Fatalf("historical tuple produced a live binding: %q %d %q %d %t", id, generation, epoch, fence, live)
			}
			if agentCodeRuntimeCurrent(domain.ExecutionPermissionRuntimeCapabilities{}, legacy, "", 0, "", 0) {
				t.Fatal("historical empty binding became current authority")
			}
		})
	}
}
