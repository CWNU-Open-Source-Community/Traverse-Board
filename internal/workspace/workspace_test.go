package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"cyberagent-workbench/internal/session"
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

type importFilteredStore struct{ *store.SQLiteStore }

func (s importFilteredStore) ListWorkspaces(ctx context.Context) ([]session.WorkspaceRecord, error) {
	rows, err := s.SQLiteStore.ListWorkspaces(ctx)
	visible := make([]session.WorkspaceRecord, 0, len(rows))
	for _, row := range rows {
		// Production lists likewise omit managed Drydock workspaces, whose
		// names remain unique in SQLite.
		if row.ID != "ws-prior" && row.ID != "ws-suffix" {
			visible = append(visible, row)
		}
	}
	return visible, err
}

func TestWorkspaceImportDoesNotOverwriteOccupiedDisambiguatedName(t *testing.T) {
	home := t.TempDir()
	state, err := store.Open(filepath.Join(home, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	selected := filepath.Join(home, "project")
	if err := os.Mkdir(selected, 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := canonicalImportRoot(selected)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(workspaceRootIdentity(root)))
	suffix := hex.EncodeToString(digest[:4])
	prior := []session.WorkspaceRecord{
		{ID: "ws-prior", Name: "project", RootPath: filepath.Join(home, "prior"), CreatedAt: time.Now().UTC()},
		{ID: "ws-suffix", Name: "project-" + suffix, RootPath: filepath.Join(home, "suffix"), CreatedAt: time.Now().UTC()},
	}
	for _, record := range prior {
		if err := state.SaveWorkspace(t.Context(), record); err != nil {
			t.Fatal(err)
		}
	}
	imported, err := NewManager(home, importFilteredStore{state}).Import(t.Context(), selected)
	if err != nil {
		t.Fatal(err)
	}
	if imported.Name != "project-"+suffix+"-2" {
		t.Fatalf("unexpected disambiguation: %#v", imported)
	}
	for _, record := range prior {
		current, err := state.GetWorkspaceByID(t.Context(), record.ID)
		if err != nil || current.RootPath != record.RootPath || current.Name != record.Name {
			t.Fatalf("import changed prior workspace: current=%#v err=%v", current, err)
		}
	}
}

func TestWorkspaceImportConcurrentRetriesPreserveDirectoryIdentity(t *testing.T) {
	home := t.TempDir()
	state, err := store.Open(filepath.Join(home, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	roots := make([]string, 8)
	for i := range roots {
		roots[i] = filepath.Join(home, string(rune('a'+i)), "project")
		if err := os.MkdirAll(roots[i], 0o755); err != nil {
			t.Fatal(err)
		}
	}
	results := make([]session.WorkspaceRecord, 16)
	errors := make([]error, len(results))
	var wait sync.WaitGroup
	start := make(chan struct{})
	for i := range results {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			// Separate managers model HTTP/native owners sharing one database.
			results[i], errors[i] = NewManager(home, state).Import(t.Context(), roots[i%len(roots)])
		}()
	}
	close(start)
	wait.Wait()
	for i, record := range results {
		if errors[i] != nil {
			t.Fatal(errors[i])
		}
		if record.ID != results[i%len(roots)].ID || record.RootPath != roots[i%len(roots)] {
			t.Fatalf("concurrent import lost identity: %#v", results)
		}
	}
	registered, err := state.ListWorkspaces(t.Context())
	if err != nil || len(registered) != len(roots) {
		t.Fatalf("registered=%#v err=%v", registered, err)
	}
	for _, record := range registered {
		entries, err := os.ReadDir(record.RootPath)
		if err != nil || len(entries) != 0 {
			t.Fatalf("import wrote directory: %#v %v", entries, err)
		}
	}
}

func TestWorkspaceImportBoundsLongDirectoryDisplayName(t *testing.T) {
	home := t.TempDir()
	state, err := store.Open(filepath.Join(home, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	selected := filepath.Join(home, strings.Repeat("a", 140))
	if err := os.Mkdir(selected, 0o755); err != nil {
		t.Fatal(err)
	}
	record, err := NewManager(home, state).Import(t.Context(), selected)
	if err != nil || len(record.Name) > 128 {
		t.Fatalf("native display rejected valid imported directory: %#v %v", record, err)
	}
}
