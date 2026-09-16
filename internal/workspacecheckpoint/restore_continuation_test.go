package workspacecheckpoint

import (
	"path/filepath"
	"testing"
	"time"
)

func TestWorkspaceRestorePreservesEpochIdentityAndStillRejectsAnotherPhysicalWorkspace(t *testing.T) {
	root := newCheckpointRepository(t)
	mustCheckpointWrite(t, filepath.Join(root, "continue.txt"), []byte("previous\n"))
	target := captureRestoreFixture(t, root, "before-epoch", "before-receipt", time.Now().UTC())
	mustCheckpointWrite(t, filepath.Join(root, "continue.txt"), []byte("current\n"))
	expected := captureRestoreFixture(t, root, "current-epoch", "current-receipt", time.Now().UTC())
	expected.Checkpoint.RunID = "successor-run"
	expected.Checkpoint.SessionID = "successor-session"
	observed := expected
	ordinary, err := PreviewRestore(expected, target, observed)
	if err != nil || !hasRestoreConflict(ordinary.Conflicts, ConflictCheckpointBinding) {
		t.Fatalf("ordinary Run scope expanded: %+v %v", ordinary, err)
	}
	preview, err := PreviewWorkspaceRestore(expected, target, observed)
	if err != nil || len(preview.Conflicts) != 0 || len(preview.Changes) != 1 {
		t.Fatalf("physical continuation preview=%+v %v", preview, err)
	}
	foreign := target
	foreign.Checkpoint.WorkspaceID = "another-workspace"
	preview, err = PreviewWorkspaceRestore(expected, foreign, observed)
	if err != nil || !hasRestoreConflict(preview.Conflicts, ConflictCheckpointBinding) {
		t.Fatalf("another workspace escaped binding: %+v %v", preview, err)
	}
	if _, err := ApplyWorkspaceRestore(t.Context(), root, expected, target, observed); err != nil {
		t.Fatal(err)
	}
	if target.Checkpoint.RunID == expected.Checkpoint.RunID || target.Checkpoint.SessionID == expected.Checkpoint.SessionID {
		t.Fatal("restore rewrote historical execution identity")
	}
}
