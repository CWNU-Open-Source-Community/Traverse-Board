//go:build darwin && desktop

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"cyberagent-workbench/internal/desktop"
)

func createFakeAppBundle(t *testing.T, root, name string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Join(path, "Contents", "MacOS"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDarwinDiscoverWorkspaceLaunchersPinsFixedAppsAndDeduplicates(t *testing.T) {
	userRoot := t.TempDir()
	systemRoot := t.TempDir()
	for _, name := range []string{"Antigravity.app", "Visual Studio Code.app", "PyCharm.app"} {
		createFakeAppBundle(t, systemRoot, name)
	}
	for _, name := range []string{"Antigravity.app", "WebStorm.app", "Terminal.app"} {
		createFakeAppBundle(t, userRoot, name)
	}
	launchers := discoverWorkspaceLaunchersIn([]string{systemRoot}, userRoot)
	seen := make(map[string]desktop.WorkspaceLauncherDescriptor, len(launchers))
	for _, launcher := range launchers {
		if _, exists := seen[launcher.descriptor.ID]; exists {
			t.Fatalf("duplicate launcher identifier %q", launcher.descriptor.ID)
		}
		seen[launcher.descriptor.ID] = launcher.descriptor
	}
	finder, ok := seen["finder"]
	if !ok || finder.Kind != desktop.WorkspaceLauncherFolder {
		t.Fatalf("Finder was not always available: %#v", launchers)
	}
	code, ok := seen["visual-studio-code"]
	if !ok || code.Kind != desktop.WorkspaceLauncherEditor {
		t.Fatalf("Visual Studio Code was not discovered: %#v", launchers)
	}
	if !ok || seen["antigravity"].Label != "Antigravity" {
		t.Fatal("Antigravity label or identity is wrong")
	}
	if len(launchers) == 0 || launchers[0].descriptor.ID != "antigravity" ||
		launchers[1].descriptor.ID != "finder" {
		t.Fatalf("launcher order is not deterministic: %#v", launchers)
	}
	for _, id := range []string{"pycharm", "webstorm", "terminal"} {
		if _, ok := seen[id]; !ok {
			t.Fatalf("launcher %q was not discovered: %#v", id, launchers)
		}
	}
	if len(seen) != 6 {
		t.Fatalf("unexpected launcher count: %#v", launchers)
	}
}

func TestDarwinWorkspaceLauncherCommandUsesOpenWithFixedArguments(t *testing.T) {
	root := t.TempDir()
	editor := createFakeAppBundle(t, t.TempDir(), "Editor.app")
	target := desktop.WorkspaceOpenTarget{ID: "workspace-1", Name: "demo", RootPath: root}

	editorCandidate := workspaceLauncher("editor", "Editor", desktop.WorkspaceLauncherEditor,
		editor, true, false)
	command, err := workspaceLauncherCommand(editorCandidate, target)
	if err != nil {
		t.Fatal(err)
	}
	if command.Path != darwinOpenExecutable || command.Dir != root ||
		!reflect.DeepEqual(command.Args, []string{darwinOpenExecutable, "-a", editor, root}) ||
		command.SysProcAttr == nil || !command.SysProcAttr.Setsid {
		t.Fatalf("unexpected editor command: path=%q dir=%q args=%#v attr=%#v",
			command.Path, command.Dir, command.Args, command.SysProcAttr)
	}

	terminalCandidate := workspaceLauncher("terminal", "Terminal",
		desktop.WorkspaceLauncherTerminal, editor, false, false)
	command, err = workspaceLauncherCommand(terminalCandidate, target)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(command.Args, []string{darwinOpenExecutable, "-a", editor}) {
		t.Fatalf("terminal received the directory: %#v", command.Args)
	}

	finderCandidate := workspaceLauncher("finder", "Finder", desktop.WorkspaceLauncherFolder,
		"", true, false)
	command, err = workspaceLauncherCommand(finderCandidate, target)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(command.Args, []string{darwinOpenExecutable, root}) {
		t.Fatalf("finder did not open the registered directory: %#v", command.Args)
	}

	nonCanonical := target
	nonCanonical.RootPath = root + string(filepath.Separator) + "."
	if _, err := workspaceLauncherCommand(editorCandidate, nonCanonical); err == nil {
		t.Fatal("non-canonical registered root was accepted")
	}
}

func TestDarwinValidateLauncherExecutableRequiresRealAppBundles(t *testing.T) {
	root := t.TempDir()
	for name, candidate := range map[string]workspaceLauncherCandidate{
		"missing": workspaceLauncher("editor", "Editor", desktop.WorkspaceLauncherEditor,
			filepath.Join(root, "Missing.app"), true, false),
		"relative": workspaceLauncher("editor", "Editor", desktop.WorkspaceLauncherEditor,
			"Applications/Editor.app", true, false),
		"plain file": workspaceLauncher("editor", "Editor", desktop.WorkspaceLauncherEditor,
			filepath.Join(root, "editor"), true, false),
		"not a bundle": workspaceLauncher("editor", "Editor", desktop.WorkspaceLauncherEditor,
			filepath.Join(root, "Editor.exe"), true, false),
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateLauncherExecutable(candidate); err == nil {
				t.Fatalf("candidate %#v was accepted", candidate)
			}
		})
	}
	bundle := createFakeAppBundle(t, root, "Editor.app")
	valid := workspaceLauncher("editor", "Editor", desktop.WorkspaceLauncherEditor,
		bundle, true, false)
	if err := validateLauncherExecutable(valid); err != nil {
		t.Fatalf("real app bundle was rejected: %v", err)
	}
	if err := validateLauncherExecutable(workspaceLauncher("finder", "Finder",
		desktop.WorkspaceLauncherFolder, "", true, false)); err != nil {
		t.Fatalf("Finder gateway validation failed: %v", err)
	}
}

func TestNativeWorkspaceLauncherCancellationAndConfirmedStart(t *testing.T) {
	root := t.TempDir()
	executable := createFakeAppBundle(t, root, "Editor.app")
	candidate := workspaceLauncher("editor", "Editor", desktop.WorkspaceLauncherEditor,
		executable, true, false)
	target := desktop.WorkspaceOpenTarget{ID: "workspace-1", Name: "demo", RootPath: root}
	confirmed := false
	starts := 0
	launcher := &nativeWorkspaceLauncher{
		discover: func() ([]workspaceLauncherCandidate, error) {
			return []workspaceLauncherCandidate{candidate}, nil
		},
		confirm: func(_ context.Context, got workspaceLauncherCandidate,
			gotTarget desktop.WorkspaceOpenTarget) (bool, error) {
			if got.descriptor.ID != candidate.descriptor.ID || gotTarget != target {
				t.Fatalf("unexpected confirmation input: %#v %#v", got, gotTarget)
			}
			return confirmed, nil
		},
		start: func(_ context.Context, got workspaceLauncherCandidate,
			gotTarget desktop.WorkspaceOpenTarget) error {
			starts++
			if got.executable != executable || gotTarget != target {
				t.Fatalf("unexpected start input: %#v %#v", got, gotTarget)
			}
			return nil
		},
	}

	cancelled, err := launcher.Open(context.Background(), target, "editor")
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != desktop.WorkspaceOpenCancelled ||
		cancelled.OperatorConfirmed || cancelled.ExternalProcessStarted || starts != 0 {
		t.Fatalf("unexpected cancelled result: %#v starts=%d", cancelled, starts)
	}

	confirmed = true
	started, err := launcher.Open(context.Background(), target, "editor")
	if err != nil {
		t.Fatal(err)
	}
	if started.Status != desktop.WorkspaceOpenStarted || !started.OperatorConfirmed ||
		!started.ExternalProcessStarted || starts != 1 {
		t.Fatalf("unexpected started result: %#v starts=%d", started, starts)
	}
}

func TestNativeWorkspaceLauncherFailsBeforeConfirmationForInvalidInputs(t *testing.T) {
	root := t.TempDir()
	confirmations := 0
	launcher := &nativeWorkspaceLauncher{
		discover: func() ([]workspaceLauncherCandidate, error) {
			return []workspaceLauncherCandidate{workspaceLauncher("editor", "Editor",
				desktop.WorkspaceLauncherEditor, filepath.Join(root, "Missing.app"), true, false)}, nil
		},
		confirm: func(context.Context, workspaceLauncherCandidate,
			desktop.WorkspaceOpenTarget) (bool, error) {
			confirmations++
			return true, nil
		},
		start: func(context.Context, workspaceLauncherCandidate,
			desktop.WorkspaceOpenTarget) error {
			t.Fatal("start must not run")
			return nil
		},
	}
	_, err := launcher.Open(context.Background(), desktop.WorkspaceOpenTarget{
		ID: "workspace-1", Name: "demo", RootPath: root,
	}, "editor")
	if err == nil || confirmations != 0 {
		t.Fatalf("invalid executable error = %v confirmations=%d", err, confirmations)
	}

	launcher.discover = func() ([]workspaceLauncherCandidate, error) {
		executable := createFakeAppBundle(t, root, "Editor.app")
		return []workspaceLauncherCandidate{workspaceLauncher("editor", "Editor",
			desktop.WorkspaceLauncherEditor, executable, true, false)}, nil
	}
	_, err = launcher.Open(context.Background(), desktop.WorkspaceOpenTarget{
		ID: "workspace-1", Name: "demo", RootPath: filepath.Join(root, "missing"),
	}, "editor")
	if err == nil || confirmations != 0 {
		t.Fatalf("invalid root error = %v confirmations=%d", err, confirmations)
	}
}

func TestNativeWorkspaceLauncherPropagatesConfirmationFailureWithoutStarting(t *testing.T) {
	root := t.TempDir()
	executable := createFakeAppBundle(t, root, "Editor.app")
	want := errors.New("dialog failed")
	starts := 0
	launcher := &nativeWorkspaceLauncher{
		discover: func() ([]workspaceLauncherCandidate, error) {
			return []workspaceLauncherCandidate{workspaceLauncher("editor", "Editor",
				desktop.WorkspaceLauncherEditor, executable, true, false)}, nil
		},
		confirm: func(context.Context, workspaceLauncherCandidate,
			desktop.WorkspaceOpenTarget) (bool, error) {
			return false, want
		},
		start: func(context.Context, workspaceLauncherCandidate,
			desktop.WorkspaceOpenTarget) error {
			starts++
			return nil
		},
	}
	_, err := launcher.Open(context.Background(), desktop.WorkspaceOpenTarget{
		ID: "workspace-1", Name: "demo", RootPath: root,
	}, "editor")
	if !errors.Is(err, want) || starts != 0 {
		t.Fatalf("confirmation error = %v starts=%d", err, starts)
	}
}
