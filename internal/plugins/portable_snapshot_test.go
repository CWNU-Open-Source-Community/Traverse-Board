package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"cyberagent-workbench/internal/agentpackages"
	"cyberagent-workbench/internal/toolcontract"
)

func TestPortableSnapshotRetainsRealUpstreamAndRestoresExactResources(t *testing.T) {
	for _, fixture := range []struct {
		name, directory, skill, resource, revision string
		files                                      int
	}{
		{"agent-plugin", "agent-plugins-example", "migrate-agent-plugin", "references/migration-guide.md", "5f3f5084a821aefa792e79500dd8f0462ab83473", 7},
		{"standalone", "anthropic-skill-creator/skills/skill-creator", "skill-creator", "scripts/__init__.py", "8a1541c4a3ffa5a20a5a91de0dcf3f0bab1d1ef4", 18},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			ctx := context.Background()
			source := filepath.Join("..", "agentpackages", "testdata", "upstream", filepath.FromSlash(fixture.directory))
			provenance := toolcontract.SourceRef{URI: "test:unmodified-upstream", Revision: fixture.revision}
			pkg, err := CapturePortableDirectory(ctx, source, "fixture-installation", provenance, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if pkg.Snapshot.RootName != filepath.Base(source) || pkg.Snapshot.Source != provenance {
				t.Fatal("lost source/root identity")
			}
			files := 0
			for _, entry := range pkg.Snapshot.Inventory {
				if entry.Kind != "file" {
					continue
				}
				files++
				raw, err := os.ReadFile(filepath.Join(source, filepath.FromSlash(entry.Path)))
				if err != nil || digest(raw) != entry.SHA256 || len(raw) != entry.Bytes {
					t.Fatalf("original file differs: %s", entry.Path)
				}
			}
			if files != fixture.files {
				t.Fatalf("got %d files, want all %d", files, fixture.files)
			}
			second, err := CapturePortableDirectory(ctx, source, "fixture-installation", provenance, t.TempDir())
			if err != nil || !bytes.Equal(pkg.Archive(), second.Archive()) {
				t.Fatalf("acquisition not deterministic: %v", err)
			}
			// Serialize only the descriptor, as the durable installation does. A
			// reopened reader obtains bodies from the pinned object after restart.
			encoded, err := json.Marshal(pkg.Snapshot)
			if err != nil {
				t.Fatal(err)
			}
			var descriptor PortableSnapshot
			if err := json.Unmarshal(encoded, &descriptor); err != nil {
				t.Fatal(err)
			}
			reader, err := OpenPortableSnapshot(ctx, descriptor, pkg.Archive(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			directory := reader.directory
			t.Cleanup(func() { _ = reader.Close() })
			index := slices.IndexFunc(descriptor.Skills, func(s SnapshotSkill) bool { return s.Name == fixture.skill })
			if index < 0 {
				t.Fatal("skill summary missing")
			}
			skill := descriptor.Skills[index]
			content, ref, err := reader.Read(ctx, skill.Instructions.Component, "", 1024*1024)
			if err != nil || ref != skill.Instructions {
				t.Fatalf("activation failed: %v", err)
			}
			original, err := os.ReadFile(filepath.Join(source, filepath.FromSlash(ref.Path)))
			if err != nil || !bytes.Equal(content, original) {
				t.Fatal("instructions were rewritten")
			}
			if fixture.name == "standalone" && (len(content) != 33168 || descriptor.AuthorVersion != "") {
				t.Fatal("standalone version/body was synthesized or truncated")
			}
			value, resource, err := reader.Read(ctx, skill.Instructions.Component, fixture.resource, MaxUncompressedBytes)
			if err != nil {
				t.Fatal(err)
			}
			original, err = os.ReadFile(filepath.Join(source, filepath.FromSlash(resource.Path)))
			if err != nil || !bytes.Equal(value, original) {
				t.Fatal("resource bytes were changed")
			}
			if fixture.name == "standalone" && len(value) != 0 {
				t.Fatal("empty Python file was lost")
			}
			if err := reader.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(directory); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("per-request extraction retained: %v", err)
			}
		})
	}
}

func portableSnapshotFixture(t *testing.T) (PortablePackage, string) {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "native-skill")
	if err := os.MkdirAll(filepath.Join(directory, "references", "empty-dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string][]byte{
		"SKILL.md":             []byte("---\nname: native-skill\ndescription: Original instructions\n---\nRead references/data.bin on demand.\n"),
		"references/data.bin":  {0, 0xff, 0x82, 0x0d, 0x0a},
		"references/empty.txt": {},
	} {
		if err := os.WriteFile(filepath.Join(directory, filepath.FromSlash(name)), value, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	pkg, err := CapturePortableDirectory(context.Background(), directory, "installed-native-skill", toolcontract.SourceRef{URI: "test:controlled-snapshot"}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return pkg, directory
}

func TestPortableSnapshotUsesAcquiredInventoryRatherThanRehashingMutations(t *testing.T) {
	pkg, source := portableSnapshotFixture(t)
	component := pkg.Snapshot.Skills[0].Instructions.Component
	// Original source mutation cannot change an installed object or its refs.
	if err := os.WriteFile(filepath.Join(source, "references", "data.bin"), []byte("other"), 0o600); err != nil {
		t.Fatal(err)
	}
	reader, err := OpenPortableSnapshot(context.Background(), pkg.Snapshot, pkg.Archive(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	original, _, err := reader.Read(context.Background(), component, "references/data.bin", 10)
	if err != nil || !bytes.Equal(original, []byte{0, 0xff, 0x82, 0x0d, 0x0a}) {
		t.Fatalf("lost acquired binary: %v", err)
	}
	if _, err := os.Stat(filepath.Join(reader.directory, "references", "empty-dir")); err != nil {
		t.Fatal("empty directory was dropped")
	}
	if err := os.WriteFile(filepath.Join(reader.directory, "references", "data.bin"), []byte("other"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := reader.Read(context.Background(), component, "references/data.bin", 10); err == nil {
		t.Fatal("same-size modified resource was reauthorized")
	}
	if err := os.WriteFile(filepath.Join(reader.directory, "references", "unacquired.txt"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := reader.Read(context.Background(), component, "references/unacquired.txt", 10); err == nil {
		t.Fatal("unacquired resource was authorized")
	}
	if _, _, err := reader.Read(context.Background(), toolcontract.ComponentRef{PackageID: component.PackageID, ComponentID: "wrong"}, "", 1000); err == nil {
		t.Fatal("unknown component accepted")
	}
	if _, _, err := reader.Read(context.Background(), component, "../../outside", 1000); err == nil {
		t.Fatal("escaping resource accepted")
	}
	if _, _, err := reader.Read(context.Background(), component, "references/empty.txt", 1); err != nil {
		t.Fatalf("empty resource: %v", err)
	}
	if err := os.WriteFile(filepath.Join(reader.directory, "SKILL.md"), []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := reader.Read(context.Background(), component, "", 1000); err == nil {
		t.Fatal("mutated instructions accepted")
	}
}

func TestPortableSnapshotRejectsArchiveDescriptorDriftAndCancellation(t *testing.T) {
	pkg, source := portableSnapshotFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := CapturePortableDirectory(ctx, source, "package", toolcontract.SourceRef{URI: "test:cancel"}, t.TempDir()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled acquisition: %v", err)
	}
	if _, err := OpenPortableSnapshot(ctx, pkg.Snapshot, pkg.Archive(), t.TempDir()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled reopen: %v", err)
	}
	archive := pkg.Archive()
	archive[len(archive)/2] ^= 1
	if _, err := OpenPortableSnapshot(context.Background(), pkg.Snapshot, archive, t.TempDir()); err == nil {
		t.Fatal("changed archive accepted")
	}
	descriptor := pkg.Snapshot
	descriptor.Skills = slices.Clone(descriptor.Skills)
	descriptor.Skills[0].Description = "invented native description"
	if _, err := OpenPortableSnapshot(context.Background(), descriptor, pkg.Archive(), t.TempDir()); err == nil {
		t.Fatal("changed metadata accepted")
	}
	reader, err := OpenPortableSnapshot(context.Background(), pkg.Snapshot, pkg.Archive(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, _, err := reader.Read(ctx, pkg.Snapshot.Skills[0].Instructions.Component, "", 1000); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read: %v", err)
	}
	// A caller changing its descriptor after open cannot replace trusted digests.
	pkg.Snapshot.Skills[0].Instructions.SHA256 = strings.Repeat("f", 64)
	if _, _, err := reader.Read(context.Background(), reader.snapshot.Skills[0].Instructions.Component, "", 1000); err != nil {
		t.Fatalf("reader retained mutable caller state: %v", err)
	}
}

func TestPortableSnapshotKeepsExplicitLegacyFormatsOnExistingReader(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "legacy")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "manifest.json"), []byte(`{"protocol":"skill.v1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := CapturePortableDirectory(context.Background(), directory, "legacy", toolcontract.SourceRef{URI: "test:legacy"}, t.TempDir())
	if !errors.Is(err, agentpackages.ErrLegacyFormat) {
		t.Fatalf("legacy dispatch changed: %v", err)
	}
}

func TestPortableSnapshotRejectsUnboundedAndLinkedAcquisition(t *testing.T) {
	t.Run("entries", func(t *testing.T) {
		_, source := portableSnapshotFixture(t)
		for index := 0; index <= MaxSnapshotEntries; index++ {
			if err := os.WriteFile(filepath.Join(source, fmt.Sprintf("file-%04d", index)), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := CapturePortableDirectory(context.Background(), source, "package", toolcontract.SourceRef{URI: "test:entry-limit"}, t.TempDir()); err == nil {
			t.Fatal("entry limit was ignored")
		}
	})
	t.Run("bytes", func(t *testing.T) {
		_, source := portableSnapshotFixture(t)
		file, err := os.Create(filepath.Join(source, "too-large.bin"))
		if err != nil {
			t.Fatal(err)
		}
		err = file.Truncate(MaxUncompressedBytes + 1)
		_ = file.Close()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := CapturePortableDirectory(context.Background(), source, "package", toolcontract.SourceRef{URI: "test:oversize"}, t.TempDir()); err == nil {
			t.Fatal("oversized source accepted")
		}
	})
	t.Run("symlink", func(t *testing.T) {
		_, source := portableSnapshotFixture(t)
		if err := os.Symlink(filepath.Join(source, "SKILL.md"), filepath.Join(source, "linked.md")); err != nil {
			t.Skipf("symlink creation unavailable: %v", err)
		}
		if _, err := CapturePortableDirectory(context.Background(), source, "package", toolcontract.SourceRef{URI: "test:link"}, t.TempDir()); err == nil {
			t.Fatal("link silently materialized")
		}
	})
}
