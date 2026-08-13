package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"cyberagent-workbench/internal/agent"
	"cyberagent-workbench/internal/session"
)

type Manager struct {
	Home  string
	Store WorkspaceStore
}

type WorkspaceStore interface {
	SaveWorkspace(ctx context.Context, rec session.WorkspaceRecord) error
	GetWorkspaceByName(ctx context.Context, name string) (session.WorkspaceRecord, error)
	ListWorkspaces(ctx context.Context) ([]session.WorkspaceRecord, error)
}

var ErrInvalidImportDirectory = errors.New("invalid workspace import directory")

func NewManager(home string, st WorkspaceStore) *Manager {
	return &Manager{Home: home, Store: st}
}

func (m *Manager) Init(ctx context.Context, name string) (session.WorkspaceRecord, error) {
	slug := Slug(name)
	root := filepath.Join(m.Home, "workspaces", slug)
	for _, dir := range []string{"attachments", "scripts", "outputs", "logs", "writeups", filepath.Join("tests", "sample_input")} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			return session.WorkspaceRecord{}, err
		}
	}
	readme := filepath.Join(root, "README.md")
	if _, err := os.Stat(readme); os.IsNotExist(err) {
		content := fmt.Sprintf("# %s\n\nPrayu local workspace.\n", name)
		if err := os.WriteFile(readme, []byte(content), 0o644); err != nil {
			return session.WorkspaceRecord{}, err
		}
	}
	rec := session.WorkspaceRecord{
		ID:        "ws-" + slug,
		Name:      slug,
		RootPath:  root,
		CreatedAt: time.Now().UTC(),
	}
	if err := m.Store.SaveWorkspace(ctx, rec); err != nil {
		return session.WorkspaceRecord{}, err
	}
	return rec, nil
}

func (m *Manager) Ensure(ctx context.Context, name string) (session.WorkspaceRecord, error) {
	slug := Slug(name)
	rec, err := m.Store.GetWorkspaceByName(ctx, slug)
	if err == nil {
		return rec, nil
	}
	return m.Init(ctx, slug)
}

// Import registers an existing directory without creating or modifying any
// content inside it. Re-selecting the same canonical directory is idempotent.
func (m *Manager) Import(ctx context.Context, selectedPath string) (session.WorkspaceRecord, error) {
	if ctx == nil || m == nil || m.Store == nil {
		return session.WorkspaceRecord{}, fmt.Errorf("%w: workspace manager is unavailable",
			ErrInvalidImportDirectory)
	}
	root, err := canonicalImportRoot(selectedPath)
	if err != nil {
		return session.WorkspaceRecord{}, err
	}
	records, err := m.Store.ListWorkspaces(ctx)
	if err != nil {
		return session.WorkspaceRecord{}, fmt.Errorf("list workspaces: %w", err)
	}
	for _, record := range records {
		if sameWorkspaceRoot(record.RootPath, root) {
			return record, nil
		}
	}

	fingerprint := sha256.Sum256([]byte(workspaceRootIdentity(root)))
	suffix := hex.EncodeToString(fingerprint[:4])
	name := Slug(importDirectoryName(root))
	for _, record := range records {
		if record.Name == name {
			name += "-" + suffix
			break
		}
	}
	record := session.WorkspaceRecord{
		ID:        "ws-import-" + hex.EncodeToString(fingerprint[:12]),
		Name:      name,
		RootPath:  root,
		CreatedAt: time.Now().UTC(),
	}
	if err := m.Store.SaveWorkspace(ctx, record); err != nil {
		return session.WorkspaceRecord{}, fmt.Errorf("save imported workspace: %w", err)
	}
	return record, nil
}

func canonicalImportRoot(selectedPath string) (string, error) {
	selectedPath = strings.TrimSpace(selectedPath)
	if selectedPath == "" || !filepath.IsAbs(selectedPath) {
		return "", fmt.Errorf("%w: an absolute directory is required",
			ErrInvalidImportDirectory)
	}
	root := filepath.Clean(selectedPath)
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("%w: selected path is not an existing directory",
			ErrInvalidImportDirectory)
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("%w: selected directory cannot be resolved",
			ErrInvalidImportDirectory)
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("%w: selected directory cannot be normalized",
			ErrInvalidImportDirectory)
	}
	return filepath.Clean(resolved), nil
}

func sameWorkspaceRoot(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func workspaceRootIdentity(root string) string {
	root = filepath.Clean(root)
	if runtime.GOOS == "windows" {
		return strings.ToLower(root)
	}
	return root
}

func importDirectoryName(root string) string {
	name := filepath.Base(root)
	if name == "." || name == string(filepath.Separator) || strings.TrimSpace(name) == "" {
		name = strings.TrimRight(filepath.VolumeName(root), `\/:`)
	}
	return name
}

func (m *Manager) ScriptPath(rec session.WorkspaceRecord, task agent.Task, ext string) string {
	if ext == "" {
		ext = ".txt"
	}
	name := Slug(task.Goal)
	if len(name) > 40 {
		name = name[:40]
	}
	return filepath.Join(rec.RootPath, "scripts", name+"-"+shortID(task.ID)+ext)
}

func (m *Manager) WriteupPath(rec session.WorkspaceRecord) string {
	return filepath.Join(rec.RootPath, "writeups", "writeup.md")
}

var slugPattern = regexp.MustCompile(`[^a-z0-9]+`)

func Slug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = slugPattern.ReplaceAllString(value, "-")
	value = strings.Trim(value, "-")
	if value == "" {
		return "workspace"
	}
	return value
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[len(id)-8:]
}
