package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/workspacecheckpoint"
)

func TestWorkspaceCheckpointCLIProvidesIdempotentCaptureTimelineAndPreview(t *testing.T) {
	home := newCanonicalCLIHome(t)
	t.Setenv("CYBERAGENT_HOME", home)
	if stdout, stderr, code := executeTestCommand(t, "workspace", "init", "checkpoint-cli"); code != 0 || stderr != "" || !strings.Contains(stdout, "initialized") {
		t.Fatalf("workspace init output=%q stderr=%q code=%d", stdout, stderr, code)
	}
	path := filepath.Join(home, "workspaces", "checkpoint-cli", "state.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	created, stderr, code := executeTestCommand(t, "run", "create",
		"checkpoint CLI contract", "--workspace", "checkpoint-cli", "--profile", "code",
		"--phase", "deliver")
	if code != 0 || stderr != "" {
		t.Fatalf("run create output=%q stderr=%q code=%d", created, stderr, code)
	}
	runID := runIDPattern.FindString(created)
	if runID == "" {
		t.Fatalf("run identity missing: %s", created)
	}

	first, stderr, code := executeTestCommand(t, "workspace", "checkpoint", "capture",
		"--run", runID, "--operation-key", "cli-capture-0001", "--title", "before edit")
	if code != 0 || stderr != "" {
		t.Fatalf("capture output=%q stderr=%q code=%d", first, stderr, code)
	}
	var capture struct {
		Checkpoint workspacecheckpoint.Checkpoint `json:"checkpoint"`
		Replayed   bool                           `json:"replayed"`
	}
	if err := json.Unmarshal([]byte(first), &capture); err != nil {
		t.Fatal(err)
	}
	if capture.Checkpoint.ID == "" || capture.Replayed {
		t.Fatalf("unexpected first capture: %#v", capture)
	}

	replay, stderr, code := executeTestCommand(t, "workspace", "checkpoint", "capture",
		"--run", runID, "--operation-key", "cli-capture-0001", "--title", "before edit")
	if code != 0 || stderr != "" {
		t.Fatalf("capture replay output=%q stderr=%q code=%d", replay, stderr, code)
	}
	if err := json.Unmarshal([]byte(replay), &capture); err != nil {
		t.Fatal(err)
	}
	if !capture.Replayed {
		t.Fatalf("capture replay was not identified: %#v", capture)
	}

	timelineJSON, stderr, code := executeTestCommand(t, "workspace", "checkpoint", "timeline",
		"--run", runID, "--limit", "10")
	if code != 0 || stderr != "" {
		t.Fatalf("timeline output=%q stderr=%q code=%d", timelineJSON, stderr, code)
	}
	var timeline application.WorkspaceCheckpointTimeline
	if err := json.Unmarshal([]byte(timelineJSON), &timeline); err != nil {
		t.Fatal(err)
	}
	if timeline.Current == nil || timeline.Current.CurrentCheckpointID != capture.Checkpoint.ID ||
		len(timeline.Checkpoints) != 1 {
		t.Fatalf("unexpected timeline: %#v", timeline)
	}

	previewJSON, stderr, code := executeTestCommand(t, "workspace", "checkpoint", "preview",
		"--run", runID, "--checkpoint", capture.Checkpoint.ID,
		"--expected-current", capture.Checkpoint.ID)
	if code != 0 || stderr != "" {
		t.Fatalf("preview output=%q stderr=%q code=%d", previewJSON, stderr, code)
	}
	var preview application.WorkspaceRestoreResult
	if err := json.Unmarshal([]byte(previewJSON), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.Confirmed || preview.ProtocolVersion !=
		application.WorkspaceCheckpointAPIProtocolVersion {
		t.Fatalf("preview unexpectedly mutated state: %#v", preview)
	}
	firstID := capture.Checkpoint.ID
	if _, stderr, code := executeTestCommand(t, "run", "start", runID); code != 0 {
		t.Fatalf("start: %s", stderr)
	}
	state, err := store.Open(filepath.Join(home, "cyberagent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	checkpoints, err := application.NewWorkspaceCheckpointService(state, domain.ExecutionPermissionRuntimeCapabilities{})
	if err != nil {
		t.Fatal(err)
	}
	acquired, err := state.AcquireRunExecutionLease(t.Context(), domain.AcquireRunExecutionLeaseRequest{
		RunID: runID, OwnerID: "checkpoint-cli-fixture", TTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _, _ = state.ReleaseRunExecutionLease(context.WithoutCancel(t.Context()), acquired.Lease) })
	boundary := application.WorkspaceMutationBoundaryRequest{RunID: runID,
		Kind: workspacecheckpoint.TransactionFileTool, OperationKey: "cli-file-mutation-0001", TriggerReceiptID: "cli-file-receipt-0001",
		LeaseID: acquired.Lease.LeaseID, LeaseGeneration: acquired.Lease.Generation}
	if _, err := checkpoints.BeginBoundary(t.Context(), boundary); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("after\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mutation, err := checkpoints.CompleteBoundary(t.Context(), boundary, nil)
	if err != nil || mutation.After == nil || mutation.Transaction.Status != workspacecheckpoint.TransactionCompleted {
		t.Fatalf("file mutation boundary: %+v err=%v", mutation, err)
	}
	released, replayed, err := state.ReleaseRunExecutionLease(t.Context(), acquired.Lease)
	if err != nil || replayed || released.Status != domain.RunExecutionLeaseReleased || released.ReleasedAt == nil ||
		released.RunID != acquired.Lease.RunID || released.LeaseID != acquired.Lease.LeaseID ||
		released.OwnerID != acquired.Lease.OwnerID || released.Generation != acquired.Lease.Generation {
		t.Fatalf("release mutation lease: lease=%+v replayed=%t err=%v", released, replayed, err)
	}
	currentLease, found, err := state.GetRunExecutionLease(t.Context(), runID)
	if err != nil || !found || !reflect.DeepEqual(currentLease, released) {
		t.Fatalf("released mutation lease was not persisted: lease=%+v err=%v", currentLease, err)
	}
	second, stderr, code := executeTestCommand(t, "workspace", "checkpoint", "capture", "--run", runID, "--operation-key", "cli-capture-0002")
	if code != 0 || json.Unmarshal([]byte(second), &capture) != nil {
		t.Fatalf("second capture: %s %s", second, stderr)
	}
	currentID := capture.Checkpoint.ID
	if _, stderr, code := executeTestCommand(t, "run", "pause", runID); code != 0 {
		t.Fatalf("pause: %s", stderr)
	}
	if _, stderr, code := executeTestCommand(t, "run", "execution-permission", "set", runID, "full", "--operation-key", "checkpoint-full-0001", "--enable-permission-control", "--enable-danger-full-access", "--confirm-full"); code != 0 {
		t.Fatalf("set Full: %s", stderr)
	}
	undoID := ""
	for _, test := range []struct{ action, before, after string }{
		{"undo", "after\n", "before\n"},
		{"redo", "before\n", "after\n"},
		{"rewind", "after\n", "before\n"},
	} {
		action := test.action
		args := []string{"workspace", "checkpoint", action, "--run", runID, "--expected-current", currentID, "--operation-key", "cli-full-" + action, "--confirm", "--enable-permission-control", "--enable-danger-full-access"}
		if action == "rewind" {
			args = append(args, "--checkpoint", firstID)
		}
		beforeTimeline, err := checkpoints.Timeline(t.Context(), runID, 100)
		if err != nil {
			t.Fatal(err)
		}
		if _, stderr, code := executeTestCommand(t, args...); code != 5 || !strings.Contains(stderr, "not authorized") {
			t.Fatalf("cold %s: code=%d stderr=%s", action, code, stderr)
		}
		deniedTimeline, err := checkpoints.Timeline(t.Context(), runID, 100)
		if err != nil || !reflect.DeepEqual(deniedTimeline, beforeTimeline) {
			t.Fatalf("cold %s changed checkpoint history: before=%+v after=%+v err=%v", action, beforeTimeline, deniedTimeline, err)
		}
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(before) != test.before {
			t.Fatalf("cold %s changed bytes: %q", action, before)
		}
		encoded, stderr, code := executeTestCommand(t, append(append([]string{}, args...), "--confirm-full")...)
		var restored application.WorkspaceRestoreResult
		if code != 0 || json.Unmarshal([]byte(encoded), &restored) != nil || !restored.Confirmed || restored.After == nil || restored.Transaction == nil {
			t.Fatalf("activated %s: %s %s", action, encoded, stderr)
		}
		if action == "undo" {
			if restored.Transaction.TriggerReceiptID != mutation.Transaction.ID {
				t.Fatal("undo did not reference the original mutation")
			}
			undoID = restored.Transaction.ID
		} else if action == "redo" && restored.Transaction.TriggerReceiptID != undoID {
			t.Fatal("redo did not reference the matching undo")
		}
		afterTimeline, err := checkpoints.Timeline(t.Context(), runID, 100)
		if err != nil || afterTimeline.Current == nil || afterTimeline.Current.CurrentCheckpointID != restored.After.ID ||
			len(afterTimeline.Transactions) != len(beforeTimeline.Transactions)+1 {
			t.Fatalf("%s did not append one restore transaction: %+v err=%v", action, afterTimeline, err)
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != test.after {
			t.Fatalf("%s did not restore exact bytes: %q", action, after)
		}
		replayed, stderr, code := executeTestCommand(t, args...)
		var replay application.WorkspaceRestoreResult
		if code != 0 || json.Unmarshal([]byte(replayed), &replay) != nil || !replay.Replayed || replay.After == nil || replay.After.ID != restored.After.ID {
			t.Fatalf("cold %s terminal replay: %s %s", action, replayed, stderr)
		}
		replayTimeline, err := checkpoints.Timeline(t.Context(), runID, 100)
		if err != nil || !reflect.DeepEqual(replayTimeline, afterTimeline) {
			t.Fatalf("cold %s replay changed checkpoint history: %+v err=%v", action, replayTimeline, err)
		}
		currentID = restored.After.ID
	}

}

func TestWorkspaceCheckpointCLIRequiresExplicitMutationConfirmation(t *testing.T) {
	t.Setenv("CYBERAGENT_HOME", t.TempDir())
	_, stderr, code := executeTestCommand(t, "workspace", "checkpoint", "rewind",
		"--run", "run-placeholder", "--checkpoint", "target",
		"--expected-current", "current", "--operation-key", "op")
	if code == 0 || !strings.Contains(stderr, "--confirm") {
		t.Fatalf("mutation did not fail closed: stderr=%q code=%d", stderr, code)
	}
}
