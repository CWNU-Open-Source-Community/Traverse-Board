package application

import (
	"context"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/fileedit"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/session"
	"cyberagent-workbench/internal/store"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Real SQLite/Git with reviewed edits, dependencies, cache and a large file.
// Both a pre-publication failure and a lost publication response are retried.
func TestThreadFileContinuationRetainsAppliedFilesAndRecoversPublication(t *testing.T) {
	for _, status := range []domain.RunStatus{domain.RunFailed} {
		t.Run(string(status), func(t *testing.T) {
			fixture, owned := newFileEditDrydockFixture(t)
			ctx := t.Context()
			checkpoints, err := NewWorkspaceCheckpointService(fixture.state, standardCodeThreadTestRuntime().ExecutionPermissionCapabilities)
			if err != nil {
				t.Fatal(err)
			}
			fixture.service.WithCheckpointService(checkpoints)
			proposals := NewFileEditProposalService(fixture.state, policy.NewDefaultChecker()).WithDrydock(fixture.service)
			source, err := proposals.IssueSource(ctx, fixture.run.ID, "tracked.txt")
			if err != nil {
				t.Fatal(err)
			}
			created, err := proposals.Propose(ctx, CreateFileEditProposalRequest{Version: FileEditProposalProtocolVersion, RunID: fixture.run.ID, SourceHandle: source.Handle, ProposedText: "prior turn reviewed edit\n"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = NewFileEditReviewService(fixture.state).WithDrydock(fixture.service).Review(ctx, ReviewFileEditRequest{Version: FileEditReviewProtocolVersion, RunID: fixture.run.ID, EditID: created.Edit.ID, Action: FileEditApproveIntent})
			if err != nil {
				t.Fatal(err)
			}
			applied, err := NewFileEditApplyService(fixture.state, policy.NewDefaultChecker(), checkpoints).WithDrydock(fixture.service).Apply(ctx, ApplyFileEditRequest{Version: fileedit.FileEditApplyProtocolVersion, RunID: fixture.run.ID, EditID: created.Edit.ID, OperationKey: "phase-m-prior-reviewed-apply", AppliedBy: "operator"})
			if err != nil || applied.Result.Status != fileedit.ApplyCompleted {
				t.Fatalf("prior apply=%+v err=%v", applied, err)
			}
			// External additions in both folders must never be erased or
			// silently used to replace the already reviewed working copy.
			writeDrydockTestFile(t, filepath.Join(owned.Path, "external.txt"), "external owned addition\n")
			writeDrydockTestFile(t, filepath.Join(fixture.sourceRoot, "outside.txt"), "external source addition\n")

			writeDrydockTestFile(t, filepath.Join(owned.Path, ".gitignore"), "node_modules/\n")
			writeDrydockTestFile(t, filepath.Join(owned.Path, "node_modules", "dependency.txt"), "dependency kept\n")
			writeDrydockTestFile(t, filepath.Join(owned.Path, ".cache", "cache.bin"), "cache kept\n")
			writeDrydockTestFile(t, filepath.Join(owned.Path, "large.bin"), strings.Repeat("x", 5*1024*1024))
			runs := NewRunService(fixture.state)
			if status == domain.RunCompleted {
				_, err = runs.Complete(ctx, fixture.run.ID)
			} else {
				_, err = runs.Fail(ctx, fixture.run.ID, "diagnostic failure after reviewed edit")
			}
			if err != nil {
				t.Fatal(err)
			}
			thread, err := fixture.state.GetThreadByRun(ctx, fixture.run.ID)
			if err != nil {
				t.Fatal(err)
			}

			failing := &failThreadFilePublicationStore{SQLiteStore: fixture.state, fail: true, failAfter: true}
			request := SubmitThreadMessageRequest{Version: domain.ThreadMessageProtocolVersion, ThreadID: thread.ID, Content: "Continue from the previous edit", OperationKey: "phase-m-next-turn", RequestedBy: "operator"}
			service := NewThreadServiceWithExecutionCapabilities(failing, standardCodeThreadTestRuntime().ExecutionPermissionCapabilities).WithDrydock(fixture.service)
			if _, err := service.Submit(ctx, request); err == nil || failing.candidateID == "" {
				t.Fatalf("expected injected publication interruption, got %v", err)
			}
			afterFailure, err := fixture.state.GetThread(ctx, thread.ID)
			if err != nil || afterFailure.ActiveRunID != "" || afterFailure.LastRunID != fixture.run.ID {
				t.Fatalf("partial successor published: %+v %v", afterFailure, err)
			}
			writeDrydockTestFile(t, filepath.Join(fixture.sourceRoot, "later note.txt"), "source note after preparation\n")

			if _, err := service.Submit(ctx, request); err == nil || failing.publishedID == "" {
				t.Fatalf("expected lost published response: %v", err)
			}
			successor, err := NewThreadServiceWithExecutionCapabilities(failing, standardCodeThreadTestRuntime().ExecutionPermissionCapabilities).WithDrydock(fixture.service).Submit(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			if successor.Run.ID != failing.publishedID {
				t.Fatalf("retry changed candidate identity %s -> %s", failing.publishedID, successor.Run.ID)
			}
			replay, err := service.Submit(ctx, request)
			if err != nil || replay.Run.ID != successor.Run.ID || replay.Message.ID != successor.Message.ID {
				t.Fatalf("same-key replay changed result: %+v %v", replay, err)
			}

			mission, err := fixture.state.GetMission(ctx, successor.Run.MissionID)
			if err != nil {
				t.Fatal(err)
			}
			files, err := ResolveRunFileWorkspace(ctx, fixture.state, successor.Run, mission, fixture.service)
			if err != nil {
				t.Fatal(err)
			}
			_, hasDrydock, err := fixture.state.GetRunFileDrydock(ctx, successor.Run.ID)
			if err != nil {
				t.Fatal(err)
			}
			_, configured, err := fixture.state.GetConfiguredStandardCodePresetOperation(ctx, successor.Run.ID)
			if err != nil {
				t.Fatal(err)
			}

			if !hasDrydock || !configured || files.Workspace.ID != owned.WorkspaceID || files.Workspace.ID == fixture.workspace.ID {
				t.Fatal("successor did not retain its configured Thread working directory")
			}
			if _, err := os.Stat(filepath.Join(files.Workspace.RootPath, "later note.txt")); !os.IsNotExist(err) {
				t.Fatalf("source note copied into continuation: %v", err)
			}
			if got := readDrydockTestFile(t, filepath.Join(fixture.sourceRoot, "later note.txt")); got != "source note after preparation\n" {
				t.Fatal("source note changed")
			}
			t.Logf("status=%s successor=%t old_workspace=%s next_workspace=%s has_drydock=%t configured=%t", status, successor.SuccessorCreated, owned.WorkspaceID, files.Workspace.ID, hasDrydock, configured)
			if got := readDrydockTestFile(t, filepath.Join(owned.Path, "tracked.txt")); got != "prior turn reviewed edit\n" {
				t.Fatalf("old edit changed: %q", got)
			}
			if got := readDrydockTestFile(t, filepath.Join(fixture.sourceRoot, "tracked.txt")); got != "user source\r\n" {
				t.Fatalf("user source changed: %q", got)
			}
			if got := readDrydockTestFile(t, filepath.Join(files.Workspace.RootPath, "tracked.txt")); got != "prior turn reviewed edit\n" {
				t.Fatalf("continued task lost applied current file: got %q; want prior reviewed edit", got)
			}

			if _, createdPhysical, err := fixture.state.GetDrydockByRun(ctx, successor.Run.ID); err != nil || createdPhysical {
				t.Fatalf("successor rewrote physical ownership: %v", err)
			}
			for path, want := range map[string]string{"node_modules/dependency.txt": "dependency kept\n", ".cache/cache.bin": "cache kept\n", "large.bin": strings.Repeat("x", 5*1024*1024)} {
				if got := readDrydockTestFile(t, filepath.Join(files.Workspace.RootPath, path)); got != want {
					t.Fatalf("working content changed at %s", path)
				}
			}
			if _, err := runs.Cancel(ctx, successor.Run.ID); err != nil {
				t.Fatal(err)
			}
			nextRequest := request
			nextRequest.OperationKey = "phase-m-third-turn"
			nextRequest.Content = "Continue after Stop"
			third, err := service.Submit(ctx, nextRequest)
			if err != nil {
				t.Fatal(err)
			}
			thirdFiles, err := ResolveRunFileWorkspace(ctx, fixture.state, third.Run, mission, fixture.service)
			if err != nil || thirdFiles.Workspace.ID != owned.WorkspaceID {
				t.Fatalf("third epoch lost files: %+v %v", thirdFiles, err)
			}

			readiness, err := NewRunCapabilityReadinessService(fixture.state, standardCodeThreadTestRuntime()).Project(ctx, third.Run.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, option := range readiness.Profiles {
				if option.Selected {
					for _, blocker := range option.BlockedBy {
						if blocker == CapabilityBlockerWorkspaceUntrusted {
							t.Fatal("new epoch readiness lost the trusted physical directory")
						}
					}
				}
			}
			if readiness.CommandRuntime.CurrentRunGranted {
				t.Fatal("created successor falsely inherited an execution grant")
			}
			if current, err := fixture.state.RunOwnsCurrentDrydock(ctx, fixture.run.ID, owned.ID); err != nil || current {
				t.Fatalf("old creator retained execution ownership: %v", err)
			}
			if current, err := fixture.state.RunOwnsCurrentDrydock(ctx, third.Run.ID, owned.ID); err != nil || !current {
				t.Fatalf("latest epoch lost working directory holder: %v", err)
			}
			if got := readDrydockTestFile(t, filepath.Join(files.Workspace.RootPath, "external.txt")); got != "external owned addition\n" {
				t.Fatalf("continued task lost external working-file addition: %q", got)
			}
		})
	}
}

type failThreadFilePublicationStore struct {
	*store.SQLiteStore
	fail        bool
	candidateID string
	failAfter   bool
	publishedID string
}

func (s *failThreadFilePublicationStore) EnsureThreadSuccessorWithFiles(ctx context.Context, request domain.ThreadMessageIntentRequest, predecessor string, mission domain.Mission, candidate domain.Run, mode domain.RunModeSnapshot, linked session.Session, initial []events.Event, files domain.ThreadFileContinuation) (domain.Thread, domain.Run, bool, error) {
	if s.fail {
		s.fail = false
		s.candidateID = candidate.ID
		return domain.Thread{}, domain.Run{}, false, errors.New("injected loss before publication")
	}

	thread, run, created, err := s.SQLiteStore.EnsureThreadSuccessorWithFiles(ctx, request, predecessor, mission, candidate, mode, linked, initial, files)
	if err == nil && s.failAfter {
		s.failAfter = false
		s.publishedID = run.ID
		return domain.Thread{}, domain.Run{}, false, errors.New("injected response loss after atomic publication")
	}
	return thread, run, created, err

}
