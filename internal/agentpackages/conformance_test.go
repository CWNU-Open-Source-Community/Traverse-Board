package agentpackages

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
)

func TestStandardManifestVersionsAndNames(t *testing.T) {
	for _, name := range []string{"a", "7", "acme.tools", "a.-b", strings.Repeat("a", 64)} {
		f := portablePlugin()
		f["plugin.json"] = &fstest.MapFile{Data: []byte(`{"$schema":"` + pluginSchema + `","name":"` + name + `","version":"not-semver","homepage":"not a URL","license":"not SPDX","author":{"email":"not an email"}}`)}
		if pkg, err := inspectPlugin(context.Background(), f); err != nil || pkg.name != name || pkg.version != "not-semver" {
			t.Fatalf("schema-permitted metadata rejected: %s %v", name, err)
		}
	}
	for _, name := range []string{"", "Upper", ".first", "last-", "two..dots", "two--hyphens", strings.Repeat("a", 65)} {
		f := portablePlugin()
		f["plugin.json"] = &fstest.MapFile{Data: []byte(`{"$schema":"` + pluginSchema + `","name":"` + name + `"}`)}
		if _, err := inspectPlugin(context.Background(), f); err == nil {
			t.Fatalf("invalid name accepted: %s", name)
		}
	}
}

func TestStandardIgnoresInlineComponentsAndPreservesMetadata(t *testing.T) {
	f := portablePlugin()
	setManifest(f, `"mcpServers":{"hidden":{"type":"stdio","command":"never-run"}},"extensions":{"io.unknown.client":{"hooks":{"launch":"never-run"}}}`)
	pkg, err := inspectPlugin(context.Background(), f)
	if err != nil || len(pkg.servers) != 0 || len(pkg.skills) != 1 {
		t.Fatalf("unknown data gained semantics: %v %#v", err, pkg)
	}
	raw, err := readContent(context.Background(), f, pkg.manifest, maxDocumentBytes)
	if err != nil || !bytes.Equal(raw, f["plugin.json"].Data) {
		t.Fatal("original plugin metadata lost")
	}
}

func TestStandardJSONAmbiguitiesHaveLocalFailureBoundaries(t *testing.T) {
	f := portablePlugin()
	f["mcp.json"] = &fstest.MapFile{Data: mcpDocument(`{"bad":{"type":"stdio","command":"a","command":"b"},"ok":{"type":"stdio","command":"node"}}`)}
	pkg, err := inspectPlugin(context.Background(), f)
	if err != nil || len(pkg.servers) != 1 || pkg.servers[0].key != "ok" {
		t.Fatalf("duplicate key did not isolate: %v", err)
	}
	f["mcp.json"] = &fstest.MapFile{Data: append(mcpDocument(`{}`), []byte(` {}`)...)}
	pkg, err = inspectPlugin(context.Background(), f)
	if err != nil || len(pkg.skills) != 1 || len(pkg.servers) != 0 {
		t.Fatalf("trailing JSON affected skills: %v", err)
	}
}

func TestStandardStandaloneSkillAndBinaryEmptyResources(t *testing.T) {
	f := fstest.MapFS{"SKILL.md": {Data: sourceSkill("alpha")}, "assets/data": {Data: []byte{0, 255, 17}}, "scripts/empty": {Data: []byte{}}}
	skill, err := inspectNamedSkill(context.Background(), f, ".", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"assets/data", "scripts/empty"} {
		raw, err := readSkillResource(context.Background(), f, skill, name, testDigest(f[name].Data), 100)
		if err != nil || !bytes.Equal(raw, f[name].Data) {
			t.Fatalf("resource lost: %s %v", name, err)
		}
	}
	f["SKILL.md"].Data = bytes.ReplaceAll(f["SKILL.md"].Data, []byte("Original"), []byte("Modified"))
	if _, err := readContent(context.Background(), f, skill.instructions, 1000); err == nil {
		t.Fatal("same-size instruction drift was accepted")
	}
}

func TestStandardSkillNamesAndClientFields(t *testing.T) {
	for _, name := range []string{"1-example", "分析", "überblick"} {
		f := fstest.MapFS{"SKILL.md": {Data: sourceSkill(name)}}
		if _, err := inspectNamedSkill(context.Background(), f, ".", name); err != nil {
			t.Fatalf("valid lowercase/uncased Unicode name rejected: %s %v", name, err)
		}
	}
	f := portablePlugin()
	f["skills/native/SKILL.md"] = &fstest.MapFile{Data: []byte("---\nname: native\ndescription: Host specific\ndisable-model-invocation: true\n---\nBody")}
	pkg, err := inspectPlugin(context.Background(), f)
	if err != nil || len(pkg.skills) != 1 || len(pkg.diagnostics) != 1 || pkg.diagnostics[0].boundary != "skill" {
		t.Fatalf("native frontmatter was reinterpreted as standard: %v %#v", err, pkg)
	}
}

func TestStandardMCPDoesNotResolveOrExpandHostLaunch(t *testing.T) {
	f := portablePlugin()
	f["mcp.json"] = &fstest.MapFile{Data: mcpDocument(`{"not-installed":{"type":"stdio","command":"./bin/missing executable","cwd":"${PLUGIN_DATA}/later-created","args":["${PLUGIN_ROOT}","${PLUGIN_DATA}","${UNKNOWN}"],"env":{"plugin_root":"case handled by B"}}}`)}
	pkg, err := inspectPlugin(context.Background(), f)
	if err != nil || len(pkg.servers) != 1 || !strings.Contains(string(pkg.servers[0].declaration), "${PLUGIN_DATA}") {
		t.Fatalf("A performed B's runtime resolution: %v %#v", err, pkg.diagnostics)
	}
}

func TestStandardFixedNamesAndResourceLimits(t *testing.T) {
	f := portablePlugin()
	f["Plugin.json"] = f["plugin.json"]
	delete(f, "plugin.json")
	if _, err := inspectPlugin(context.Background(), f); err == nil {
		t.Fatal("nonstandard manifest case accepted")
	}
	f = portablePlugin()
	f["skills/good/skill.md"] = f["skills/good/SKILL.md"]
	delete(f, "skills/good/SKILL.md")
	pkg, err := inspectPlugin(context.Background(), f)
	if err != nil || len(pkg.skills) != 0 {
		t.Fatalf("nonstandard SKILL.md case discovered: %v", err)
	}
	f = portablePlugin()
	f["mcp.json"] = &fstest.MapFile{Data: mcpDocument(`{"ok":{"type":"stdio","command":"node"}}`)}
	for i := 0; i < maxDirectoryEntries; i++ {
		f[fmt.Sprintf("skills/extra-%04d.txt", i)] = &fstest.MapFile{Data: []byte{}}
	}
	pkg, err = inspectPlugin(context.Background(), f)
	if err != nil || len(pkg.servers) != 1 || len(pkg.skills) != 0 || len(pkg.diagnostics) != 1 || pkg.diagnostics[0].code != "resource_limit_exceeded" {
		t.Fatalf("host discovery limit lost its boundary: %v %#v", err, pkg.diagnostics)
	}
}

func TestStandardAllowsContainedDirectoryLinks(t *testing.T) {
	base := t.TempDir()
	rootPath := filepath.Join(base, "package")
	target := filepath.Join(rootPath, "shared", "mirror")
	if err := os.MkdirAll(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(rootPath, "skills"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootPath, "plugin.json"), portablePlugin()["plugin.json"].Data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "SKILL.md"), sourceSkill("mirror"), 0600); err != nil {
		t.Fatal(err)
	}
	makeDirectoryLink(t, filepath.Join(rootPath, "skills", "mirror"), target)
	root, err := openDirectory(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if _, err := fs.Stat(root, "skills/mirror"); err != nil {
		t.Fatalf("stat contained link: %v", err)
	}
	pkg, err := inspectPlugin(context.Background(), root)
	if err != nil || len(pkg.skills) != 1 || pkg.skills[0].name != "mirror" {
		t.Fatalf("contained link rejected: %v %#v", err, pkg.diagnostics)
	}
}

func TestStandardManySkillsHaveBoundedLocalDiscovery(t *testing.T) {
	f := portablePlugin()
	delete(f, "skills/good/SKILL.md")
	f["mcp.json"] = &fstest.MapFile{Data: mcpDocument(`{"ok":{"type":"stdio","command":"node"}}`)}
	for i := 0; i <= maxSkillComponents; i++ {
		name := fmt.Sprintf("skill-%03d", i)
		f["skills/"+name+"/SKILL.md"] = &fstest.MapFile{Data: sourceSkill(name)}
	}
	pkg, err := inspectPlugin(context.Background(), f)
	if err != nil || len(pkg.skills) != maxSkillComponents || len(pkg.servers) != 1 || len(pkg.diagnostics) != 1 || pkg.diagnostics[0].code != "resource_limit_exceeded" {
		t.Fatalf("component budget lost sibling results: %v %#v", err, pkg.diagnostics)
	}
}

func TestStandardRealDirectoryLinkBoundaries(t *testing.T) {
	base := t.TempDir()
	rootPath := filepath.Join(base, "package")
	outside := filepath.Join(base, "outside")
	for _, directory := range []string{filepath.Join(rootPath, "skills", "good"), outside} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(name string, data []byte) {
		t.Helper()
		if err := os.WriteFile(name, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(rootPath, "plugin.json"), portablePlugin()["plugin.json"].Data)
	write(filepath.Join(rootPath, "skills", "good", "SKILL.md"), sourceSkill("good"))
	write(filepath.Join(rootPath, "mcp.json"), mcpDocument(`{"ok":{"type":"stdio","command":"node"}}`))
	write(filepath.Join(outside, "SKILL.md"), sourceSkill("escape"))
	write(filepath.Join(outside, "secret"), []byte("outside"))
	makeDirectoryLink(t, filepath.Join(rootPath, "skills", "escape"), outside)
	root, err := openDirectory(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	pkg, err := inspectPlugin(context.Background(), root)
	if err != nil || len(pkg.skills) != 1 || len(pkg.servers) != 1 {
		t.Fatalf("escaping skill killed siblings: %v %#v", err, pkg.diagnostics)
	}
	// A delayed read through a newly introduced link must also be confined.
	makeDirectoryLink(t, filepath.Join(rootPath, "skills", "good", "assets"), outside)
	if _, err := readSkillResource(context.Background(), root, pkg.skills[0], "assets/secret", testDigest([]byte("outside")), 100); err == nil {
		t.Fatal("delayed resource read escaped")
	}
	if _, err := fs.Stat(root, "skills/escape/SKILL.md"); err == nil {
		t.Fatal("test root did not enforce containment")
	}
}

func makeDirectoryLink(t *testing.T, name, target string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		// Controlled fixture directories only; no scripts from packages run.
		output, err := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", name, target).CombinedOutput()
		if err != nil {
			t.Skipf("fixture junction unavailable: %v %s", err, output)
		}
		return
	}
	relative, err := filepath.Rel(filepath.Dir(name), target)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(relative, name); err != nil {
		t.Skipf("fixture symlink unavailable: %v", err)
	}
}
