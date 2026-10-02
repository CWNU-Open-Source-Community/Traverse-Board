package plugins

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"

	"cyberagent-workbench/internal/agentpackages"
	"cyberagent-workbench/internal/toolcontract"
)

const (
	PortableSnapshotProtocol = "agent-package-snapshot.v1"
	// These are acquisition limits of the existing plugin object store, not
	// restrictions on the Agent Skills / Agent Plugins specifications.
	MaxSnapshotEntries = 1024
)

// SnapshotEntry is host acquisition evidence, never an author declaration.
// This acquisition path accepts ordinary files and directories only. Links,
// junctions and special files are rejected rather than silently omitted or
// copied from an unreviewed target. Script/binary bytes remain inert data.
type SnapshotEntry struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	SHA256 string `json:"sha256,omitempty"`
	Bytes  int    `json:"bytes"`
	Mode   uint32 `json:"mode"`
}

type SnapshotSkill struct {
	Instructions toolcontract.ContentRef `json:"instructions"`
	Name         string                  `json:"name"`
	Description  string                  `json:"description"`
}

// PortableSnapshot belongs to the existing plugin installation/object flow.
// Revision identifies the acquired archive. AuthorVersion remains optional
// and unmodified; it is never replaced with a fabricated semantic version.
// Inventory and content references must be retained from acquisition, not
// recomputed at activation to authorize whatever is now present on disk.
type PortableSnapshot struct {
	ProtocolVersion string                    `json:"protocol_version"`
	PackageID       string                    `json:"package_id"`
	Revision        string                    `json:"revision"`
	RootName        string                    `json:"root_name"`
	Format          string                    `json:"format"`
	Name            string                    `json:"name"`
	AuthorVersion   string                    `json:"author_version,omitempty"`
	Source          toolcontract.SourceRef    `json:"source"`
	Manifest        toolcontract.ContentRef   `json:"manifest"`
	Inventory       []SnapshotEntry           `json:"inventory"`
	Skills          []SnapshotSkill           `json:"skills"`
	Diagnostics     []toolcontract.Diagnostic `json:"diagnostics,omitempty"`
}

type PortablePackage struct {
	Snapshot PortableSnapshot
	raw      []byte
}

func (p PortablePackage) Archive() []byte  { return bytes.Clone(p.raw) }
func (PortablePackage) String() string     { return "PortablePackage[redacted]" }
func (p PortablePackage) GoString() string { return p.String() }

func snapshotPath(value string) bool {
	if value == "." || !fs.ValidPath(value) || strings.ContainsAny(value, "\\:\x00") {
		return false
	}
	_, err := filepath.Localize(value)
	return err == nil
}

func (s PortableSnapshot) Validate() error {
	if s.ProtocolVersion != PortableSnapshotProtocol || !validDigest(s.Revision) ||
		(toolcontract.ComponentRef{PackageID: s.PackageID, ComponentID: "package"}).Validate() != nil ||
		!snapshotPath(s.RootName) || strings.Contains(s.RootName, "/") || s.Source.Validate() != nil ||
		(s.Format != agentpackages.FormatAgentSkill && s.Format != agentpackages.FormatAgentPlugin) ||
		s.Manifest.Validate() != nil || s.Manifest.Component.PackageID != s.PackageID ||
		len(s.Inventory) < 1 || len(s.Inventory) > MaxSnapshotEntries {
		return errors.New("portable snapshot identity is invalid")
	}
	entries := make(map[string]SnapshotEntry, len(s.Inventory))
	aliases := make(map[string]bool, len(s.Inventory))
	total := 0
	previous := ""
	for _, entry := range s.Inventory {
		if !snapshotPath(entry.Path) || entry.Path <= previous || aliases[strings.ToLower(entry.Path)] ||
			entry.Mode&^0o777 != 0 || entry.Bytes < 0 || entry.Bytes > MaxUncompressedBytes {
			return errors.New("portable snapshot inventory is invalid")
		}
		switch entry.Kind {
		case "file":
			if !validDigest(entry.SHA256) {
				return errors.New("snapshot file digest is invalid")
			}
		case "directory":
			if entry.Bytes != 0 || entry.SHA256 != "" {
				return errors.New("snapshot directory metadata is invalid")
			}
		default:
			return errors.New("snapshot entry is not an ordinary file or directory")
		}
		parent := path.Dir(entry.Path)
		if parent != "." && entries[parent].Kind != "directory" {
			return errors.New("snapshot entry has no acquired parent directory")
		}
		total += entry.Bytes
		if total > MaxUncompressedBytes {
			return errors.New("portable snapshot exceeds the object size limit")
		}
		entries[entry.Path], aliases[strings.ToLower(entry.Path)], previous = entry, true, entry.Path
	}
	bound := func(ref toolcontract.ContentRef) bool {
		entry, found := entries[ref.Path]
		return ref.Validate() == nil && ref.Component.PackageID == s.PackageID && found &&
			entry.Kind == "file" && entry.SHA256 == ref.SHA256
	}
	if !bound(s.Manifest) {
		return errors.New("snapshot manifest is not bound to acquired bytes")
	}
	seen := map[toolcontract.ComponentRef]bool{}
	for _, skill := range s.Skills {
		if seen[skill.Instructions.Component] || !bound(skill.Instructions) {
			return errors.New("snapshot skill is not bound to acquired bytes")
		}
		seen[skill.Instructions.Component] = true
	}
	encoded, err := json.Marshal(s)
	if err != nil || len(encoded) > MaxManifestBytes {
		return errors.New("portable snapshot metadata exceeds the installation limit")
	}
	return nil
}

// CapturePortableDirectory freezes all accepted source bytes before parsing
// metadata. Discovery therefore describes exactly the saved archive, even if
// the original directory is changed afterwards. No install script is run.
// scratchRoot is a host-owned temporary directory, never a package argument.
func CapturePortableDirectory(ctx context.Context, directory, packageID string,
	source toolcontract.SourceRef, scratchRoot string,
) (PortablePackage, error) {
	if ctx.Err() != nil {
		return PortablePackage{}, ctx.Err()
	}
	rootName := filepath.Base(filepath.Clean(directory))
	if !snapshotPath(rootName) || strings.Contains(rootName, "/") || source.Validate() != nil {
		return PortablePackage{}, errors.New("portable snapshot source is invalid")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return PortablePackage{}, err
	}
	defer root.Close()
	entries, contents, err := captureSnapshot(ctx, root)
	if err != nil {
		return PortablePackage{}, err
	}
	raw, err := encodeSnapshot(ctx, rootName, entries, contents)
	if err != nil {
		return PortablePackage{}, err
	}
	snapshot := PortableSnapshot{ProtocolVersion: PortableSnapshotProtocol,
		PackageID: packageID, Revision: digest(raw), RootName: rootName, Source: source, Inventory: entries}
	directoryCopy, cleanup, err := materializeSnapshot(ctx, snapshot, raw, scratchRoot)
	if err != nil {
		return PortablePackage{}, err
	}
	defer cleanup()
	pkg, err := agentpackages.OpenDirectory(ctx, directoryCopy, packageID, source)
	if err != nil {
		return PortablePackage{}, err
	}
	defer pkg.Close()
	describeSnapshot(&snapshot, pkg)
	if err := snapshot.Validate(); err != nil {
		return PortablePackage{}, err
	}
	return PortablePackage{Snapshot: snapshot, raw: raw}, nil
}

func describeSnapshot(snapshot *PortableSnapshot, pkg *agentpackages.Package) {
	snapshot.Format, snapshot.Name, snapshot.AuthorVersion = pkg.Format(), pkg.Name(), pkg.Version()
	snapshot.Manifest, snapshot.Diagnostics = pkg.Manifest(), pkg.Diagnostics()
	snapshot.Skills = nil
	for _, component := range pkg.Skills() {
		instructions, _ := pkg.Instructions(component)
		name, description, _ := pkg.SkillSummary(component)
		snapshot.Skills = append(snapshot.Skills, SnapshotSkill{instructions, name, description})
	}
}

func captureSnapshot(ctx context.Context, root *os.Root) ([]SnapshotEntry, map[string][]byte, error) {
	var entries []SnapshotEntry
	contents := map[string][]byte{}
	total := 0
	var walk func(string) error
	walk = func(directory string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		before, err := root.Lstat(directory)
		if err != nil || before.Mode()&fs.ModeType != fs.ModeDir {
			return errors.New("snapshot directory is not ordinary")
		}
		file, err := root.Open(directory)
		if err != nil {
			return err
		}
		opened, err := file.Stat()
		if err != nil || !opened.IsDir() || !os.SameFile(before, opened) {
			_ = file.Close()
			return errors.New("snapshot directory identity changed")
		}
		children, err := file.ReadDir(MaxSnapshotEntries + 1)
		_ = file.Close()
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		if len(children)+len(entries) > MaxSnapshotEntries {
			return errors.New("snapshot entry limit exceeded")
		}
		sort.Slice(children, func(i, j int) bool { return children[i].Name() < children[j].Name() })
		for _, child := range children {
			// Repository administration is not plugin source, including a Git
			// worktree's .git pointer file. Never traverse or package it.
			if directory == "." && child.Name() == ".git" {
				continue
			}
			name := path.Join(directory, child.Name())
			if !snapshotPath(name) {
				return errors.New("snapshot path is invalid")
			}
			before, err := root.Lstat(name)
			if err != nil {
				return err
			}
			if before.Mode()&fs.ModeType != 0 && before.Mode()&fs.ModeType != fs.ModeDir {
				return errors.New("snapshot acquisition requires ordinary files and directories")
			}
			entry := SnapshotEntry{Path: name, Mode: uint32(before.Mode().Perm())}
			if before.IsDir() {
				entry.Kind = "directory"
				entries = append(entries, entry)
				if len(entries) > MaxSnapshotEntries {
					return errors.New("snapshot entry limit exceeded")
				}
				if err := walk(name); err != nil {
					return err
				}
				continue
			}
			value, err := readSnapshotFile(ctx, root, name, before, MaxUncompressedBytes-total)
			if err != nil {
				return err
			}
			total += len(value)
			entry.Kind, entry.SHA256, entry.Bytes = "file", digest(value), len(value)
			entries, contents[name] = append(entries, entry), value
			if len(entries) > MaxSnapshotEntries {
				return errors.New("snapshot entry limit exceeded")
			}
		}
		after, err := root.Lstat(directory)
		if err != nil || after.Mode()&fs.ModeType != fs.ModeDir || !os.SameFile(before, after) {
			return errors.New("snapshot directory changed during acquisition")
		}
		return nil
	}
	if err := walk("."); err != nil {
		return nil, nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, contents, nil
}

func readSnapshotFile(ctx context.Context, root *os.Root, name string, before fs.FileInfo, limit int) ([]byte, error) {
	if before.Size() < 0 || before.Size() > int64(limit) {
		return nil, errors.New("snapshot byte limit exceeded")
	}
	file, err := openSnapshotFile(root, name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return nil, errors.New("snapshot file identity changed")
	}
	value, err := io.ReadAll(io.LimitReader(&snapshotContextReader{ctx, file}, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	after, err := root.Lstat(name)
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) ||
		len(value) > limit || int64(len(value)) != opened.Size() {
		return nil, errors.New("snapshot file changed or exceeds its limit")
	}
	return value, ctx.Err()
}

type snapshotContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *snapshotContextReader) Read(value []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(value)
}

func encodeSnapshot(ctx context.Context, rootName string, entries []SnapshotEntry, contents map[string][]byte) ([]byte, error) {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			_ = writer.Close()
			return nil, err
		}
		header := &zip.FileHeader{Name: rootName + "/" + entry.Path, Method: zip.Deflate}
		mode := fs.FileMode(entry.Mode)
		if entry.Kind == "directory" {
			header.Name += "/"
			mode |= fs.ModeDir
		}
		header.SetMode(mode)
		target, err := writer.CreateHeader(header)
		if err != nil {
			_ = writer.Close()
			return nil, err
		}
		if _, err := target.Write(contents[entry.Path]); err != nil {
			_ = writer.Close()
			return nil, err
		}
		if buffer.Len() > MaxArchiveBytes {
			_ = writer.Close()
			return nil, errors.New("snapshot archive exceeds the object limit")
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	if buffer.Len() < 1 || buffer.Len() > MaxArchiveBytes {
		return nil, errors.New("snapshot archive exceeds the object limit")
	}
	return buffer.Bytes(), nil
}

// PortableReader is a per-request view of already acquired bytes. It owns an
// isolated temporary extraction and a held loader root; Close releases both.
// Callers still check current installation/Run authority before I/O and before
// returning content. This object grants no permission or executable capability.
type PortableReader struct {
	pkg       *agentpackages.Package
	snapshot  PortableSnapshot
	directory string
	cleanup   func()
}

func OpenPortableSnapshot(ctx context.Context, snapshot PortableSnapshot, raw []byte, scratchRoot string) (*PortableReader, error) {
	if err := snapshot.Validate(); err != nil {
		return nil, err
	}
	// Copy before retaining caller-owned slice fields.
	encoded, _ := json.Marshal(snapshot)
	var frozen PortableSnapshot
	if err := json.Unmarshal(encoded, &frozen); err != nil {
		return nil, err
	}
	directory, cleanup, err := materializeSnapshot(ctx, frozen, raw, scratchRoot)
	if err != nil {
		return nil, err
	}
	pkg, err := agentpackages.OpenDirectory(ctx, directory, frozen.PackageID, frozen.Source)
	if err != nil {
		cleanup()
		return nil, err
	}
	actual := frozen
	describeSnapshot(&actual, pkg)
	if !reflect.DeepEqual(actual, frozen) {
		_ = pkg.Close()
		cleanup()
		return nil, errors.New("snapshot descriptions do not match acquired bytes")
	}
	return &PortableReader{pkg: pkg, snapshot: frozen, directory: directory, cleanup: cleanup}, nil
}

func (r *PortableReader) Close() error {
	if r == nil || r.pkg == nil {
		return nil
	}
	err := r.pkg.Close()
	r.pkg = nil
	r.cleanup()
	return err
}

func (r *PortableReader) Read(ctx context.Context, component toolcontract.ComponentRef, resource string, limit int) ([]byte, toolcontract.ContentRef, error) {
	if r == nil || r.pkg == nil {
		return nil, toolcontract.ContentRef{}, errors.New("portable reader is closed")
	}
	index := slices.IndexFunc(r.snapshot.Skills, func(s SnapshotSkill) bool { return s.Instructions.Component == component })
	if index < 0 {
		return nil, toolcontract.ContentRef{}, errors.New("snapshot skill component is unknown")
	}
	ref := r.snapshot.Skills[index].Instructions
	if resource != "" {
		if !snapshotPath(resource) {
			return nil, toolcontract.ContentRef{}, errors.New("snapshot resource path is invalid")
		}
		name := path.Join(path.Dir(ref.Path), resource)
		entry := slices.IndexFunc(r.snapshot.Inventory, func(e SnapshotEntry) bool { return e.Path == name && e.Kind == "file" })
		if entry < 0 {
			return nil, toolcontract.ContentRef{}, errors.New("resource is absent from the acquired inventory")
		}
		var err error
		ref, err = r.pkg.Resource(component, resource, r.snapshot.Inventory[entry].SHA256)
		if err != nil {
			return nil, toolcontract.ContentRef{}, err
		}
	}
	value, err := r.pkg.Read(ctx, ref, limit)
	return value, ref, err
}

func materializeSnapshot(ctx context.Context, snapshot PortableSnapshot, raw []byte, scratchRoot string) (string, func(), error) {
	if ctx.Err() != nil {
		return "", nil, ctx.Err()
	}
	if len(raw) < 1 || len(raw) > MaxArchiveBytes || digest(raw) != snapshot.Revision ||
		!snapshotPath(snapshot.RootName) || strings.Contains(snapshot.RootName, "/") {
		return "", nil, errors.New("snapshot archive does not match its acquired revision")
	}
	archive, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return "", nil, err
	}
	if len(archive.File) != len(snapshot.Inventory) || len(archive.File) > MaxSnapshotEntries {
		return "", nil, errors.New("snapshot archive inventory count changed")
	}
	temporary, err := os.MkdirTemp(scratchRoot, "agent-package-read-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(temporary) }
	success := false
	defer func() {
		if !success {
			cleanup()
		}
	}()
	root, err := os.OpenRoot(temporary)
	if err != nil {
		return "", nil, err
	}
	defer root.Close()
	if err := root.Mkdir(snapshot.RootName, 0o700); err != nil {
		return "", nil, err
	}
	total := 0
	for index, file := range archive.File {
		if ctx.Err() != nil {
			return "", nil, ctx.Err()
		}
		entry := snapshot.Inventory[index]
		name := snapshot.RootName + "/" + entry.Path
		if entry.Kind == "directory" {
			name += "/"
		}
		if !snapshotPath(entry.Path) || file.Name != name || file.Mode().Perm() != fs.FileMode(entry.Mode) ||
			file.UncompressedSize64 != uint64(entry.Bytes) || (file.FileInfo().IsDir() != (entry.Kind == "directory")) ||
			file.Mode() & ^(fs.ModePerm|fs.ModeDir) != 0 {
			return "", nil, errors.New("snapshot archive entry does not match its inventory")
		}
		if entry.Kind == "directory" {
			if err := root.Mkdir(strings.TrimSuffix(name, "/"), 0o700); err != nil {
				return "", nil, err
			}
			continue
		}
		total += entry.Bytes
		if entry.Bytes < 0 || total > MaxUncompressedBytes {
			return "", nil, errors.New("snapshot extraction byte limit exceeded")
		}
		stream, err := file.Open()
		if err != nil {
			return "", nil, err
		}
		value, readErr := io.ReadAll(io.LimitReader(&snapshotContextReader{ctx, stream}, int64(entry.Bytes)+1))
		_ = stream.Close()
		if readErr != nil || len(value) != entry.Bytes || digest(value) != entry.SHA256 {
			return "", nil, errors.New("snapshot extracted content differs from acquired bytes")
		}
		// Extraction is for reading; executable mode evidence stays in inventory.
		// Starting a process requires the separately authorized runtime path.
		target, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return "", nil, err
		}
		_, writeErr := target.Write(value)
		closeErr := target.Close()
		if writeErr != nil || closeErr != nil {
			return "", nil, fmt.Errorf("snapshot extraction failed: %w", errors.Join(writeErr, closeErr))
		}
	}
	success = true
	return filepath.Join(temporary, snapshot.RootName), cleanup, nil
}
