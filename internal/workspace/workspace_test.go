package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cyberagent-workbench/internal/store"
)

func TestWorkspaceInitCreatesExpectedLayout(t *testing.T) {
	home := t.TempDir()
	st, err := store.Open(filepath.Join(home, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	mgr := NewManager(home, st)
	rec, err := mgr.Init(context.Background(), "Demo Workspace")
	if err != nil {
		t.Fatal(err)
	}

	for _, dir := range []string{"attachments", "scripts", "outputs", "logs", "writeups", filepath.Join("tests", "sample_input")} {
		path := filepath.Join(rec.RootPath, dir)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("expected %s: %v", path, err)
		}
		if !info.IsDir() {
			t.Fatalf("%s is not a directory", path)
		}
	}
}

func TestWorkspaceImportRegistersExistingDirectoryWithoutWritingIntoIt(t *testing.T) {
	home := t.TempDir()
	state, err := store.Open(filepath.Join(home, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	selected := filepath.Join(home, "existing-project")
	if err := os.Mkdir(selected, 0o755); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(home, state)
	first, err := manager.Import(t.Context(), selected)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Import(t.Context(), selected+string(filepath.Separator))
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || first.Name != "existing-project" || first.RootPath != selected {
		t.Fatalf("unexpected idempotent import: first=%#v second=%#v", first, second)
	}
	entries, err := os.ReadDir(selected)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("workspace import wrote into selected directory: %#v", entries)
	}
}

func TestWorkspaceImportKeepsSameBasenameDirectoriesDistinct(t *testing.T) {
	home := t.TempDir()
	state, err := store.Open(filepath.Join(home, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	manager := NewManager(home, state)

	firstRoot := filepath.Join(home, "one", "project")
	secondRoot := filepath.Join(home, "two", "project")
	for _, root := range []string{firstRoot, secondRoot} {
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	first, err := manager.Import(t.Context(), firstRoot)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Import(t.Context(), secondRoot)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID || first.Name == second.Name ||
		!strings.HasPrefix(second.Name, "project-") {
		t.Fatalf("same-basename imports collided: first=%#v second=%#v", first, second)
	}
}

func TestWorkspaceImportRejectsFilesAndRelativePaths(t *testing.T) {
	home := t.TempDir()
	state, err := store.Open(filepath.Join(home, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	manager := NewManager(home, state)
	file := filepath.Join(home, "not-a-directory.txt")
	if err := os.WriteFile(file, []byte("no"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{file, "relative-project"} {
		if _, err := manager.Import(t.Context(), candidate); !errors.Is(err, ErrInvalidImportDirectory) {
			t.Fatalf("Import(%q) error = %v, want invalid import directory", candidate, err)
		}
	}
}
