package application

import (
	"testing"

	"cyberagent-workbench/internal/fileedit"
)

func TestThreadActivityFileEditUsesDurableWorkspaceOwnership(t *testing.T) {
	fixture, owned := newFileEditDrydockFixture(t)
	edit, err := fileedit.NewManager(fixture.state).Propose(t.Context(), fileedit.Proposal{
		SessionID: fixture.run.SessionID, WorkspaceID: owned.WorkspaceID, WorkspaceRoot: owned.Path,
		Path: "tracked.txt", ProposedText: "activity target\n"})
	if err != nil {
		t.Fatal(err)
	}
	service := NewThreadActivityDetailService(fixture.state)
	detail := &ThreadActivityFileEditDetail{EditID: edit.ID}
	if err := service.enrichThreadActivityFileEdit(t.Context(), fixture.run, detail); err != nil ||
		!detail.DiffAvailable || detail.Path != edit.Path || detail.Diff.AddedLines != 1 || detail.Diff.RemovedLines != 1 {
		t.Fatalf("owned Drydock activity disappeared: %+v %v", detail, err)
	}
	// The sibling shares a source, but its activity cannot claim this edit.
	_, sibling, err := NewRunService(fixture.state).Create(t.Context(), CreateRunRequest{
		Goal: "independent source sibling", Profile: "code", WorkspaceID: fixture.workspace.ID})
	if err != nil {
		t.Fatal(err)
	}
	sibling, err = NewRunService(fixture.state).Start(t.Context(), sibling.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.enrichThreadActivityFileEdit(t.Context(), sibling,
		&ThreadActivityFileEditDetail{EditID: edit.ID}); err == nil {
		t.Fatal("another Run claimed the owned target's activity diff")
	}
}
