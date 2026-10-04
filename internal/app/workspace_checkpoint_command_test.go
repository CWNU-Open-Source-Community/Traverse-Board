package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/workspacecheckpoint"
)

func TestWorkspaceCheckpointCLIProvidesIdempotentCaptureTimelineAndPreview(t *testing.T) {
	home := t.TempDir()
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
	if err := os.WriteFile(path, []byte("after\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, stderr, code := executeTestCommand(t, "workspace", "checkpoint", "capture", "--run", runID, "--operation-key", "cli-capture-0002")
	if code != 0 || json.Unmarshal([]byte(second), &capture) != nil {
		t.Fatalf("second capture: %s %s", second, stderr)
	}
	currentID := capture.Checkpoint.ID
	for _, action := range []string{"start", "pause"} {
		if _, stderr, code := executeTestCommand(t, "run", action, runID); code != 0 {
			t.Fatalf("%s: %s", action, stderr)
		}
	}
	if _, stderr, code := executeTestCommand(t, "run", "execution-permission", "set", runID, "full", "--operation-key", "checkpoint-full", "--enable-permission-control", "--enable-danger-full-access", "--confirm-full"); code != 0 {
		t.Fatalf("set Full: %s", stderr)
	}
	for _, action := range []string{"rewind", "undo", "redo"} {
		args := []string{"workspace", "checkpoint", action, "--run", runID, "--expected-current", currentID, "--operation-key", "cli-full-" + action, "--confirm", "--enable-permission-control", "--enable-danger-full-access"}
		if action == "rewind" {
			args = append(args, "--checkpoint", firstID)
		}
		if _, stderr, code := executeTestCommand(t, args...); code != 5 || !strings.Contains(stderr, "not authorized") {
			t.Fatalf("cold %s: code=%d stderr=%s", action, code, stderr)
		}
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		wantBefore := "before\n"
		if action == "rewind" || action == "redo" {
			wantBefore = "after\n"
		}
		if string(before) != wantBefore {
			t.Fatalf("cold %s changed bytes: %q", action, before)
		}
		encoded, stderr, code := executeTestCommand(t, append(append([]string{}, args...), "--confirm-full")...)
		var restored application.WorkspaceRestoreResult
		if code != 0 || json.Unmarshal([]byte(encoded), &restored) != nil || !restored.Confirmed || restored.After == nil {
			t.Fatalf("activated %s: %s %s", action, encoded, stderr)
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		wantAfter := "before\n"
		if action == "undo" {
			wantAfter = "after\n"
		}
		if string(after) != wantAfter {
			t.Fatalf("%s did not restore exact bytes: %q", action, after)
		}
		replayed, stderr, code := executeTestCommand(t, args...)
		var replay application.WorkspaceRestoreResult
		if code != 0 || json.Unmarshal([]byte(replayed), &replay) != nil || !replay.Replayed || replay.After == nil || replay.After.ID != restored.After.ID {
			t.Fatalf("cold %s terminal replay: %s %s", action, replayed, stderr)
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
