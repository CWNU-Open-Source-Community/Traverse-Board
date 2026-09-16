package application

import (
	"testing"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/fileedit"
)

func TestBuildFileEditChangeSetPreservesIndependentFileStates(t *testing.T) {
	run := domain.Run{ID: "run-change-set", MissionID: "mission-change-set",
		SessionID: "session-change-set"}
	mission := domain.Mission{ID: run.MissionID, WorkspaceID: "workspace-change-set"}
	previews := []fileedit.Preview{
		{ID: "edit-proposed", SessionID: run.SessionID, WorkspaceID: mission.WorkspaceID,
			Path: "a.txt", Status: fileedit.StatusProposed, Diff: "+a\n"},
		{ID: "edit-applied", SessionID: run.SessionID, WorkspaceID: mission.WorkspaceID,
			Path: "b.txt", Status: fileedit.StatusApplied, Diff: "+b\n"},
		{ID: "edit-failed", SessionID: run.SessionID, WorkspaceID: mission.WorkspaceID,
			Path: "c.txt", Status: fileedit.StatusFailed, Diff: "+c\n"},
	}
	result, err := BuildFileEditChangeSet(run, mission, previews)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 3 || result.Counts.Proposed != 1 ||
		result.Counts.Applied != 1 || result.Counts.Failed != 1 ||
		result.TotalDiffBytes != 9 {
		t.Fatalf("unexpected change set: %+v", result)
	}
	previews[0].Status = fileedit.StatusDenied
	if result.Items[0].Status != fileedit.StatusProposed {
		t.Fatal("change set did not own its projected slice")
	}
}

func TestBuildFileEditChangeSetRejectsCrossRunRecords(t *testing.T) {
	run := domain.Run{ID: "run-change-set", MissionID: "mission-change-set",
		SessionID: "session-change-set"}
	mission := domain.Mission{ID: run.MissionID, WorkspaceID: "workspace-change-set"}
	_, err := BuildFileEditChangeSet(run, mission, []fileedit.Preview{{
		ID: "edit-cross-run", SessionID: "session-other", WorkspaceID: mission.WorkspaceID,
		Path: "a.txt", Status: fileedit.StatusProposed,
	}})
	if err == nil {
		t.Fatal("expected cross-Run file edit to be rejected")
	}
}

func TestBuildFileEditChangeSetKeepsTargetSeparateFromSourceHistory(t *testing.T) {
	run := domain.Run{ID: "run-target-change-set", MissionID: "mission-target-change-set", SessionID: "session-target-change-set"}
	mission := domain.Mission{ID: run.MissionID, WorkspaceID: "workspace-source"}
	preview := fileedit.Preview{ID: "edit-drydock", SessionID: run.SessionID, WorkspaceID: "workspace-drydock",
		Path: "same.txt", Status: fileedit.StatusApplied, Diff: "+target\n"}
	result, err := BuildFileEditChangeSetForWorkspace(run, mission, preview.WorkspaceID, []fileedit.Preview{preview})
	if err != nil || result.WorkspaceID != preview.WorkspaceID || result.Counts.Applied != 1 || mission.WorkspaceID != "workspace-source" {
		t.Fatalf("target change set changed source identity: result=%+v mission=%+v err=%v", result, mission, err)
	}
	previous := preview
	previous.ID, previous.WorkspaceID = "edit-source", mission.WorkspaceID
	if _, err := BuildFileEditChangeSetForWorkspace(run, mission, preview.WorkspaceID, []fileedit.Preview{preview, previous}); err == nil {
		t.Fatal("source history was merged into the target change set")
	}
}
