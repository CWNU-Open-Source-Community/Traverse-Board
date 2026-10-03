package agentpackages

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

var errContentLimit = errors.New("content_limit_exceeded")
var errDirectoryLimit = errors.New("directory_entry_limit_exceeded")
var errMissingSkill = errors.New("skill_content_missing")

func loadFailureCode(err error, fallback string) string {
	if errors.Is(err, errContentLimit) || errors.Is(err, errDirectoryLimit) {
		return "resource_limit_exceeded"
	}
	return fallback
}

func digestBytes(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func reference(name string, raw []byte) contentReference {
	return contentReference{path: name, sha256: digestBytes(raw), bytes: len(raw)}
}

// source must be openDirectory over the host's package snapshot (os.Root.FS
// also works for relative links). A plain os.DirFS provides no such boundary.
// fs.FS injection is private and used by the deterministic parsing tests.
func readBounded(ctx context.Context, source fs.FS, name string, limit int) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if source == nil || !relativeFile(name) || limit < 1 || limit > maxResourceBytes {
		return nil, errors.New("content_reference_invalid")
	}
	before, err := fs.Stat(source, name)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, errors.New("content_not_regular")
	}
	if before.Size() < 0 || before.Size() > int64(limit) {
		return nil, errContentLimit
	}
	file, err := source.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("content_not_regular")
	}
	if info.Size() < 0 || info.Size() > int64(limit) {
		return nil, errContentLimit
	}
	raw, err := io.ReadAll(io.LimitReader(&contextReader{ctx: ctx, source: file}, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(raw) > limit {
		return nil, errContentLimit
	}
	if int64(len(raw)) != info.Size() {
		return nil, errors.New("content_changed_during_read")
	}
	return raw, nil
}

type contextReader struct {
	ctx    context.Context
	source io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.source.Read(p)
}

func relativeFile(name string) bool {
	_, err := filepath.Localize(name)
	return name != "." && err == nil
}

func directoryEntries(ctx context.Context, source fs.FS, name string) ([]fs.DirEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if source == nil || !fs.ValidPath(name) {
		return nil, errors.New("directory_invalid")
	}
	info, err := fs.Stat(source, name)
	if err != nil || !info.IsDir() {
		return nil, errors.New("directory_invalid")
	}
	file, err := source.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader, ok := file.(fs.ReadDirFile)
	if !ok {
		return nil, errors.New("directory_unreadable")
	}
	entries := make([]fs.DirEntry, 0)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		batch, err := reader.ReadDir(maxDirectoryEntries + 1 - len(entries))
		entries = append(entries, batch...)
		if len(entries) > maxDirectoryEntries {
			return nil, errDirectoryLimit
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			return nil, errors.New("directory_read_stalled")
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, nil
}

func readContent(ctx context.Context, source fs.FS, ref contentReference, limit int) ([]byte, error) {
	decoded, err := hex.DecodeString(ref.sha256)
	if err != nil || len(decoded) != sha256.Size || strings.ToLower(ref.sha256) != ref.sha256 {
		return nil, errors.New("content_digest_required")
	}
	raw, err := readBounded(ctx, source, ref.path, limit)
	if err != nil {
		return nil, err
	}
	if (ref.bytes >= 0 && len(raw) != ref.bytes) || digestBytes(raw) != ref.sha256 {
		return nil, errors.New("content_revision_changed")
	}
	return raw, nil
}

// The digest comes from the host's immutable snapshot inventory, not from an
// untrusted skill declaration or a later stat. Reading never executes scripts.
func readSkillResource(ctx context.Context, source fs.FS, skill skillDescription, relative, digest string, limit int) ([]byte, error) {
	if !relativeFile(relative) || !fs.ValidPath(skill.root) {
		return nil, errors.New("resource_reference_invalid")
	}
	return readContent(ctx, source, contentReference{path: path.Join(skill.root, relative), sha256: digest, bytes: -1}, limit)
}
