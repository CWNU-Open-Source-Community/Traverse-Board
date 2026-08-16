package application

import (
	"context"
	"path/filepath"
	"testing"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/projectconfig"
	"cyberagent-workbench/internal/store"
)

func TestRunCreatePinsProjectConfigSnapshotAndNarrowsBudget(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "project-config-run.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	service := NewRunService(st)
	ctx := context.Background()
	effective := projectconfig.Effective{
		Protocol:        projectconfig.ProtocolVersion,
		ReadOnly:        false,
		AllowedProfiles: []string{"code"},
		MaxTurns:        7,
		MaxToolCalls:    4,
		ExcludePaths:    []string{"fixtures"},
	}
	fingerprint := effective.Fingerprint()
	_, run, err := service.Create(ctx, CreateRunRequest{
		Goal: "project config snapshot", Profile: "code", Surface: "code",
		Phase: "plan", WorkspaceID: "ws-1", Budget: domain.Budget{MaxTurns: 50, MaxToolCalls: 30},
		ProjectConfig: &effective,
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.Budget.MaxTurns != 7 || run.Budget.MaxToolCalls != 4 {
		t.Fatalf("project budget did not narrow the run: %#v", run.Budget)
	}
	if run.Config.ProjectConfigFingerprint != fingerprint || len(run.Config.ProjectConfig) == 0 {
		t.Fatalf("project snapshot was not pinned: fp=%q raw=%d", run.Config.ProjectConfigFingerprint, len(run.Config.ProjectConfig))
	}
	// Simulate a later file edit: reload the Run and confirm immutability.
	reloaded, err := st.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Config.ProjectConfigFingerprint != fingerprint {
		t.Fatal("run snapshot drifted after creation")
	}
}

func TestRunCreateRejectsWideningProjectProfilesAndReadOnlyWriteProfile(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "project-config-guard.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	service := NewRunService(st)
	ctx := context.Background()
	// read_only forbids the write-capable script profile.
	if _, _, err := service.Create(ctx, CreateRunRequest{
		Goal: "read only", Profile: "script", Surface: "cyber", Phase: "plan",
		WorkspaceID: "ws-1", Budget: domain.Budget{MaxTurns: 50},
		ProjectConfig: &projectconfig.Effective{Protocol: projectconfig.ProtocolVersion, ReadOnly: true},
	}); err == nil {
		t.Fatal("read_only project accepted a write-capable profile")
	}
	// allowed_profiles without the requested profile fails closed.
	if _, _, err := service.Create(ctx, CreateRunRequest{
		Goal: "profile guard", Profile: "code", Surface: "code", Phase: "plan",
		WorkspaceID: "ws-1", Budget: domain.Budget{MaxTurns: 50},
		ProjectConfig: &projectconfig.Effective{Protocol: projectconfig.ProtocolVersion,
			AllowedProfiles: []string{"review"}},
	}); err == nil {
		t.Fatal("project allowed_profiles accepted a profile outside the set")
	}
}
