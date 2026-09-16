package application

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/fileedit"
	"cyberagent-workbench/internal/standardcodedelivery"
	"cyberagent-workbench/internal/verification"
	"cyberagent-workbench/internal/workspacecheckpoint"
)

func TestThreadReviewDoesNotTreatExcludedOrUnreadableEntriesAsDeleted(t *testing.T) {
	change := ThreadReviewChange{Path: "target", Operation: "delete", ProposedSHA256: "missing"}
	for _, entry := range []workspacecheckpoint.Entry{
		{Kind: workspacecheckpoint.EntryDirectory, State: workspacecheckpoint.StateIgnored, WorktreeSHA256: "missing"},
		{Kind: workspacecheckpoint.EntrySymlink, State: workspacecheckpoint.StatePresent, WorktreeSHA256: "missing"},
		{Kind: workspacecheckpoint.EntryFile, State: workspacecheckpoint.StateExternal, StoragePolicy: workspacecheckpoint.StorageUnreadable, WorktreeSHA256: "missing"},
	} {
		_, match := matchThreadReviewChange(change, map[string]workspacecheckpoint.Entry{"target": entry}, true)
		if match != "unavailable" {
			t.Fatalf("excluded entry was incorrectly counted as deleted: %+v -> %s", entry, match)
		}
	}
	_, match := matchThreadReviewChange(change, map[string]workspacecheckpoint.Entry{"target": {State: workspacecheckpoint.StateMissing, WorktreeSHA256: "missing"}}, false)
	if match != "matches" {
		t.Fatal("exact observed deletion lost its meaning")
	}
	_, match = matchThreadReviewChange(change, map[string]workspacecheckpoint.Entry{}, false)
	if match != "unavailable" {
		t.Fatal("a truncated manifest proved an unobserved path absent")
	}
}

func TestThreadReviewDrydockSuccessorKeepsActualTargetAndHistoricalEdit(t *testing.T) {
	fixture, owned := newFileEditDrydockFixture(t)
	ctx := t.Context()
	manager := fileedit.NewManager(fixture.state)
	edit, err := manager.Propose(ctx, fileedit.Proposal{SessionID: fixture.run.SessionID, WorkspaceID: owned.WorkspaceID,
		WorkspaceRoot: owned.Path, Path: "tracked.txt", ProposedText: "reviewed isolated content\n"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = NewFileEditReviewService(fixture.state).WithDrydock(fixture.service).Review(ctx, ReviewFileEditRequest{Version: FileEditReviewProtocolVersion, RunID: fixture.run.ID, EditID: edit.ID, Action: FileEditApproveIntent}); err != nil {
		t.Fatal(err)
	}
	if _, err = manager.Approve(ctx, edit.ID, owned.Path); err != nil {
		t.Fatal(err)
	}
	if _, err = NewRunService(fixture.state).Fail(ctx, fixture.run.ID, "fixture boundary"); err != nil {
		t.Fatal(err)
	}
	thread, err := fixture.state.GetThreadByRun(ctx, fixture.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	next, err := NewThreadServiceWithExecutionCapabilities(fixture.state, standardCodeThreadTestRuntime().ExecutionPermissionCapabilities).WithDrydock(fixture.service).Submit(ctx,
		SubmitThreadMessageRequest{Version: domain.ThreadMessageProtocolVersion, ThreadID: thread.ID, Content: "Review the existing changes", OperationKey: "review-successor", RequestedBy: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	review, err := NewThreadReviewService(fixture.state).WithDrydock(fixture.service).WithCodeHandoff(NewCodeHandoffService(fixture.state)).Review(ctx, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if review.CurrentRunID != next.Run.ID || review.Target.Kind != "drydock" || review.Target.WorkspaceID != owned.WorkspaceID || review.Target.RootPath != owned.Path || len(review.AppliedChanges) != 1 || review.AppliedChanges[0].RunID != fixture.run.ID || review.AppliedChanges[0].WorkspaceID != owned.WorkspaceID || review.AppliedChanges[0].CurrentMatch != "matches" {
		t.Fatalf("review lost exact inherited worktree and historical edit: %+v", review)
	}
	source, err := os.ReadFile(filepath.Join(fixture.sourceRoot, "tracked.txt"))
	if err != nil || string(source) == "reviewed isolated content\n" {
		t.Fatal("review or prior edit wrote the source directory")
	}
	// With no exact Drydock reader the original directory must remain unavailable,
	// while historical records still retain their original physical Workspace.
	unavailable, err := NewThreadReviewService(fixture.state).Review(ctx, thread.ID)
	if err != nil || unavailable.Target.State != "unavailable" || unavailable.Target.RootPath != "" || len(unavailable.AppliedChanges) != 1 || unavailable.AppliedChanges[0].CurrentMatch != "unavailable" {
		t.Fatalf("unavailable Drydock silently fell back to source: %+v %v", unavailable, err)
	}
}

func TestThreadReviewCheckBindingsNeverUpgradeLegacyReceipts(t *testing.T) {
	now := time.Now().UTC()
	revision := strings.Repeat("a", 64)
	result := ThreadReview{Target: ThreadReviewTarget{WorkspaceID: "physical"}, Revision: ThreadReviewRevision{State: "available", RevisionSHA256: revision}}
	zero := 0
	handoff := CodeHandoff{RunID: "run", SessionID: "session", Verification: CodeHandoffVerification{References: []CodeHandoffVerificationReference{{ID: "operator-pass", Outcome: verification.OutcomePass, CreatedAt: now}}},
		HostCommands: &CodeHandoffHostCommands{Items: []CodeHandoffHostCommand{{ProposalID: "host-pass", Purpose: "npm test", ResultStatus: "succeeded", Receipt: &CodeHandoffHostCommandReceipt{ExitCode: 0, CompletedAt: now}}, {ProposalID: "approved-only", ReviewDecision: "approve"}}},
		StandardCodeDelivery: &standardcodedelivery.Report{Binding: standardcodedelivery.Binding{RunID: "run", SessionID: "session", DrydockWorkspaceID: "physical"}, CreatedAt: now,
			Verifications: []standardcodedelivery.Verification{
				{JobID: "current", ExitCode: &zero, RevisionSHA256: revision, CurrentRevision: true},
				{JobID: "stale", ExitCode: &zero, RevisionSHA256: strings.Repeat("b", 64), CurrentRevision: true},
				{JobID: "unbound", ExitCode: &zero},
				{JobID: "not-observed", ExitCode: &zero, RevisionSHA256: revision},
			}}}
	addThreadReviewChecks(&result, handoff, "/handoff")
	want := map[string]string{"operator-pass": "unbound", "host-pass": "unbound", "current": "current", "stale": "stale", "unbound": "unbound", "not-observed": "unavailable"}
	if len(result.Checks) != len(want) {
		t.Fatalf("approval was counted as execution: %+v", result.Checks)
	}
	for _, check := range result.Checks {
		if check.RevisionState != want[check.ID] || check.RunID != "run" || check.HandoffURL != "/handoff" {
			t.Fatalf("incorrect check binding: %+v", check)
		}
	}
	result.Checks = nil
	result.Revision.State = "unavailable"
	addThreadReviewChecks(&result, handoff, "/handoff")
	for _, check := range result.Checks {
		if check.SourceKind == "standard_code" && check.RecordedRevisionSHA256 != "" && check.RevisionState != "unavailable" {
			t.Fatalf("unavailable observation accepted: %+v", check)
		}
	}
	result.Checks = nil
	result.Revision.State = "available"
	result.Target.WorkspaceID = "source"
	addThreadReviewChecks(&result, handoff, "/handoff")
	for _, check := range result.Checks {
		if check.ID == "current" && (check.RevisionState != "stale" || check.Reason != "different_workspace_target") {
			t.Fatalf("receipt transferred across workspaces: %+v", check)
		}
	}
}
