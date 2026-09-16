package application

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/drydock"
	"cyberagent-workbench/internal/store"
)

func TestRunFileWorkspaceSeparatesRunsSharingOneSource(t *testing.T) {
	fixture := newDrydockApplicationFixture(t, "shared source run files")
	mission, err := fixture.state.GetMission(t.Context(), fixture.run.MissionID)
	if err != nil {
		t.Fatal(err)
	}
	ordinary, err := ResolveRunFileWorkspace(t.Context(), fixture.state, fixture.run, mission, nil)
	if err != nil || ordinary.Workspace.ID != fixture.workspace.ID || ordinary.Drydock != nil {
		t.Fatalf("ordinary source target=%+v err=%v", ordinary, err)
	}
	first := mustCreateDrydock(t, fixture)
	_, secondRun, err := NewRunService(fixture.state).Create(t.Context(), CreateRunRequest{
		Goal: "independent second Run", Profile: "code", Surface: "code", Phase: "deliver",
		WorkspaceID: fixture.workspace.ID, Budget: domain.Budget{MaxTurns: 8, MaxTokens: 2000, MaxToolCalls: 32}})
	if err != nil {
		t.Fatal(err)
	}
	secondFixture := fixture
	secondFixture.run = secondRun
	second := mustCreateDrydock(t, secondFixture)
	writeDrydockTestFile(t, filepath.Join(fixture.sourceRoot, "tracked.txt"), "user source\r\n")
	writeDrydockTestFile(t, filepath.Join(first.Path, "tracked.txt"), "first target\n")
	writeDrydockTestFile(t, filepath.Join(second.Path, "tracked.txt"), "second target\n")
	resolver := NewAgentCodeWorkspaceResolver(fixture.state, fixture.service)
	for _, item := range []struct{ runID, workspaceID, root, content string }{
		{fixture.run.ID, first.WorkspaceID, first.Path, "first target\n"},
		{secondRun.ID, second.WorkspaceID, second.Path, "second target\n"},
	} {
		id, root, err := resolver(t.Context(), item.runID, fixture.workspace.ID)
		if err != nil || id != item.workspaceID || root != item.root ||
			readDrydockTestFile(t, filepath.Join(root, "tracked.txt")) != item.content {
			t.Fatalf("Run %s target id=%s root=%s err=%v", item.runID, id, root, err)
		}
	}
	if _, _, err := resolver(t.Context(), fixture.run.ID, second.WorkspaceID); err == nil {
		t.Fatal("owned target was accepted as source control identity")
	}
	if _, err := ResolveRunFileWorkspace(t.Context(), fixture.state, fixture.run, mission, nil); err == nil {
		t.Fatal("owned Run fell back to source without the registered Drydock service")
	}
	// The owned directory still exists, but its Git worktree identity is missing.
	// Resolution must not reinterpret that directory or the source as the target.
	gitFile := filepath.Join(first.Path, ".git")
	hiddenGit := filepath.Join(first.Path, ".git-hidden-by-test")
	if err := os.Rename(gitFile, hiddenGit); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Rename(hiddenGit, gitFile) })
	if _, _, err := resolver(t.Context(), fixture.run.ID, fixture.workspace.ID); err == nil {
		t.Fatal("missing owned worktree identity fell back to source")
	}
}

type runFileMissingDrydockStore struct{ *store.SQLiteStore }

func (s runFileMissingDrydockStore) GetDrydockByRun(context.Context, string) (drydock.Workspace, bool, error) {
	return drydock.Workspace{}, false, nil
}

func (s runFileMissingDrydockStore) GetRunFileDrydock(context.Context, string) (drydock.Workspace, bool, error) {
	return drydock.Workspace{}, false, nil
}

func TestRunFileWorkspaceConfiguredMissingDrydockFailsClosed(t *testing.T) {
	fixture := newDrydockApplicationFixture(t, "configured missing file target")
	presets, err := NewStandardCodePresetService(fixture.state, fixture.service, standardCodeThreadTestRuntime())
	if err != nil {
		t.Fatal(err)
	}
	request := ConfigureStandardCodeRequest{Version: domain.StandardCodePresetProtocolVersion,
		RunID: fixture.run.ID, Action: "configure", BackendIntent: "local",
		OperationKey: "missing-file-target-preset-0001", RequestedBy: "operator"}
	preview, err := presets.Configure(t.Context(), request)
	if err != nil || !preview.TrustRequired {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
	request.ConfirmWorkspaceTrust, request.ExpectedTrustDigest = true, preview.TrustDigest
	configured, err := presets.Configure(t.Context(), request)
	if err != nil || configured.Status != StandardCodeResultConfigured {
		t.Fatalf("configured=%+v err=%v", configured, err)
	}
	run, err := fixture.state.GetRun(t.Context(), fixture.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	mission, err := fixture.state.GetMission(t.Context(), run.MissionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveRunFileWorkspace(t.Context(), fixture.state, run, mission, fixture.service); err != nil {
		t.Fatal(err)
	}
	// Preserve the real configured operation; simulate the storage read losing
	// its exact owned binding. There is no fake command or execution evidence.
	if _, err := ResolveRunFileWorkspace(t.Context(), runFileMissingDrydockStore{fixture.state}, run, mission, fixture.service); err == nil {
		t.Fatal("configured Run with missing Drydock fell back to source")
	}
}
