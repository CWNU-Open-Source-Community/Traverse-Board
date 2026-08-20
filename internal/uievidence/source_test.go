package uievidence

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"cyberagent-workbench/internal/workspacecheckpoint"
)

func TestBindSourceIgnoresBuildOutputsButDetectsSourceChanges(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("dist/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "source.txt"), []byte("fixed source\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, arguments := range [][]string{{"init", "-q"},
		{"config", "user.email", "ui-evidence@example.invalid"},
		{"config", "user.name", "UI Evidence"}, {"add", "."}, {"commit", "-m", "fixture"}} {
		command := exec.Command("git", append([]string{"-C", root}, arguments...)...)
		command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", arguments, err, output)
		}
	}

	initial := bindTestSource(t, root, "source-initial", time.Now().UTC())
	if err := os.Mkdir(filepath.Join(root, "dist"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "dist", "bundle.js"),
		[]byte("generated output\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	afterBuild := bindTestSource(t, root, "source-after-build", time.Now().UTC())
	if afterBuild != initial {
		t.Fatalf("ignored build output changed source binding:\ninitial=%+v\nafter=%+v",
			initial, afterBuild)
	}

	if err := os.WriteFile(filepath.Join(root, "source.txt"), []byte("changed source\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	afterSourceChange := bindTestSource(t, root, "source-after-change", time.Now().UTC())
	if afterSourceChange == initial || afterSourceChange.ManifestSHA256 == initial.ManifestSHA256 {
		t.Fatal("tracked content change retained the fixed source binding")
	}
}

func bindTestSource(t *testing.T, root, id string, at time.Time) SourceBinding {
	t.Helper()
	snapshot, err := workspacecheckpoint.Capture(context.Background(),
		workspacecheckpoint.CaptureRequest{ID: id, RunID: "run-source-test",
			MissionID: "mission-source-test", SessionID: "session-source-test",
			WorkspaceID: "workspace-source-test", WorkspaceRoot: root,
			AttemptID: "attempt-source-test", Trigger: workspacecheckpoint.TriggerManual,
			Phase: workspacecheckpoint.PhaseStandalone, TriggerReceiptID: id,
			RequestedBy: "run_supervisor", Title: "UI evidence source test", CreatedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := BindSource(context.Background(), root, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return binding
}
