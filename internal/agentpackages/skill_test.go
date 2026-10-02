package agentpackages

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func sourceSkill(name string) []byte {
	return []byte("---\nname: " + name + "\ndescription: A useful test skill.\n---\nOriginal instructions.\n")
}

func portablePlugin() fstest.MapFS {
	return fstest.MapFS{
		"plugin.json":          {Data: []byte(`{"$schema":"` + pluginSchema + `","name":"example","version":"author-revision"}`)},
		"skills/good/SKILL.md": {Data: sourceSkill("good")},
	}
}

func mcpDocument(servers string) []byte {
	return []byte(`{"$schema":"` + mcpSchema + `","mcpServers":` + servers + `}`)
}

func TestStandardUpstreamPluginAndSkillRemainUnmodified(t *testing.T) {
	ctx := context.Background()
	pluginRoot, err := os.OpenRoot("testdata/upstream/agent-plugins-example")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pluginRoot.Close() })
	plugin, err := inspectPlugin(ctx, pluginRoot.FS())
	if err != nil {
		t.Fatal(err)
	}
	if plugin.name != "agent-plugins-example" || plugin.version != "1.0.0" || len(plugin.skills) != 1 || len(plugin.diagnostics) != 0 {
		t.Fatalf("unexpected upstream description: %#v", plugin)
	}
	skillRoot, err := os.OpenRoot("testdata/upstream/anthropic-skill-creator")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = skillRoot.Close() })
	skill, err := inspectSkill(ctx, skillRoot.FS(), "skills/skill-creator")
	if err != nil {
		t.Fatal(err)
	}
	if skill.name != "skill-creator" || skill.instructions.bytes != 33168 {
		t.Fatalf("real skill was clipped or rejected: %#v", skill)
	}
	raw, err := readContent(ctx, skillRoot.FS(), skill.instructions, 100000)
	if err != nil {
		t.Fatal(err)
	}
	want, err := fs.ReadFile(skillRoot.FS(), "skills/skill-creator/SKILL.md")
	if err != nil || !bytes.Equal(raw, want) {
		t.Fatal("original SKILL.md did not survive loading")
	}
	for _, resource := range []string{"assets/eval_review.html", "references/schemas.md", "scripts/__init__.py", "scripts/run_eval.py", "agents/grader.md"} {
		want, err := fs.ReadFile(skillRoot.FS(), "skills/skill-creator/"+resource)
		if err != nil {
			t.Fatal(err)
		}
		got, err := readSkillResource(ctx, skillRoot.FS(), skill, resource, testDigest(want), 100000)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("resource %s not preserved: %v", resource, err)
		}
	}
}

type observedFS struct {
	fs.FS
	opened []string
}

func (s *observedFS) Open(name string) (fs.File, error) {
	s.opened = append(s.opened, name)
	return s.FS.Open(name)
}

func TestStandardDiscoveryDoesNotOpenResourcesOrExposeBody(t *testing.T) {
	files := portablePlugin()
	files["skills/good/assets/private.bin"] = &fstest.MapFile{Data: []byte{0, 255, 17}}
	files["skills/good/scripts/never-run.ps1"] = &fstest.MapFile{Data: []byte("throw 'MUST NOT RUN'")}
	files["skills/good/nested/another/SKILL.md"] = &fstest.MapFile{Data: sourceSkill("another")}
	source := &observedFS{FS: files}
	pkg, err := inspectPlugin(context.Background(), source)
	if err != nil || len(pkg.skills) != 1 {
		t.Fatalf("discovery failed: %v %#v", err, pkg)
	}
	for _, opened := range source.opened {
		if strings.Contains(opened, "/assets/") || strings.Contains(opened, "/scripts/") || strings.Contains(opened, "/nested/") {
			t.Fatalf("eager resource discovery: %q", source.opened)
		}
	}
	if strings.Contains(string(pkg.skills[0].frontmatter), "Original instructions") {
		t.Fatal("metadata exposes the instruction body")
	}
}

func TestStandardSkillFrontmatterAndValidation(t *testing.T) {
	valid := "---\r\nname: alpha\r\ndescription: |\r\n  Useful multiline\r\n  description.\r\nlicense: LICENSE.txt\r\ncompatibility: A host requirement\r\nallowed-tools: Shell Read\r\nmetadata:\r\n  revision: v-next\r\n---\r\nUntouched body.\r\n"
	skill, err := inspectSkill(context.Background(), fstest.MapFS{"alpha/SKILL.md": {Data: []byte(valid)}}, "alpha")
	if err != nil || skill.metadata["revision"] != "v-next" || skill.allowedTools != "Shell Read" {
		t.Fatalf("frontmatter lost: %#v %v", skill, err)
	}
	if !bytes.Contains(skill.frontmatter, []byte("\r\n")) {
		t.Fatal("frontmatter was normalized")
	}
	for name, raw := range map[string]string{
		"missing_frontmatter": "# Plain markdown", "missing_end": "---\nname: alpha\ndescription: hi",
		"duplicate_name": "---\nname: alpha\nname: alpha\ndescription: hi\n---\n",
		"not_mapping":    "---\n[alpha, hi]\n---\n", "name_type": "---\nname: 12\ndescription: hi\n---\n",
		"wrong_directory":     "---\nname: beta\ndescription: hi\n---\n",
		"repeated_hyphen":     "---\nname: a--b\ndescription: hi\n---\n",
		"description_type":    "---\nname: alpha\ndescription: 12\n---\n",
		"description_long":    "---\nname: alpha\ndescription: " + strings.Repeat("a", 1025) + "\n---\n",
		"compatibility_empty": "---\nname: alpha\ndescription: hi\ncompatibility: ''\n---\n",
		"metadata_type":       "---\nname: alpha\ndescription: hi\nmetadata:\n  count: 12\n---\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := inspectSkill(context.Background(), fstest.MapFS{"alpha/SKILL.md": {Data: []byte(raw)}}, "alpha"); err == nil {
				t.Fatal("invalid frontmatter accepted")
			}
		})
	}
}

func TestStandardLazyReadsVerifyDigestAndLimits(t *testing.T) {
	ctx := context.Background()
	files := fstest.MapFS{"alpha/SKILL.md": {Data: sourceSkill("alpha")}, "alpha/assets/data.bin": {Data: []byte{0, 255, 3}}}
	skill, err := inspectSkill(ctx, files, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"../outside", "/absolute", "assets/../../outside", `C:\outside`, "."} {
		if _, err := readSkillResource(ctx, files, skill, bad, strings.Repeat("a", 64), 100); err == nil {
			t.Fatalf("unsafe resource accepted: %s", bad)
		}
	}
	if _, err := readSkillResource(ctx, files, skill, "assets/data.bin", testDigest([]byte{0, 255, 3}), 2); err == nil {
		t.Fatal("resource limit ignored")
	}
	if _, err := readSkillResource(ctx, files, skill, "assets/data.bin", strings.Repeat("a", 64), 100); err == nil {
		t.Fatal("resource drift ignored")
	}
	files["alpha/SKILL.md"].Data = sourceSkill("beta")
	if _, err := readContent(ctx, files, skill.instructions, 1000); err == nil {
		t.Fatal("instruction drift ignored")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := inspectSkill(cancelled, files, "alpha"); err == nil {
		t.Fatal("cancellation ignored")
	}
}

func TestStandardRootRejectsEscapingLinks(t *testing.T) {
	base := t.TempDir()
	packagePath := filepath.Join(base, "package")
	outsidePath := filepath.Join(base, "outside")
	if err := os.MkdirAll(filepath.Join(packagePath, "alpha"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outsidePath, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packagePath, "alpha", "SKILL.md"), sourceSkill("alpha"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outsidePath, "secret"), []byte("outside bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "..", "outside"), filepath.Join(packagePath, "alpha", "escape")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	root, err := os.OpenRoot(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	skill, err := inspectSkill(context.Background(), root.FS(), "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readSkillResource(context.Background(), root.FS(), skill, "escape/secret", testDigest([]byte("outside bytes")), 100); err == nil {
		t.Fatal("package read escaped root")
	}
}

func TestUpstreamFixtureHashes(t *testing.T) {
	raw, err := os.ReadFile("testdata/upstream/pins.json")
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.TrimPrefix(raw, []byte{0xef, 0xbb, 0xbf})
	var pins []struct {
		Repository string `json:"repository"`
		Files      []struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
			Bytes  int    `json:"bytes"`
		} `json:"files"`
	}
	if err := json.Unmarshal(raw, &pins); err != nil {
		t.Fatal(err)
	}
	for _, pin := range pins {
		directory := "agent-plugins-example"
		if pin.Repository == "anthropics/skills" {
			directory = "anthropic-skill-creator"
		}
		for _, file := range pin.Files {
			raw, err := os.ReadFile(filepath.FromSlash(path.Join("testdata/upstream", directory, file.Path)))
			if err != nil || len(raw) != file.Bytes || testDigest(raw) != file.SHA256 {
				t.Fatalf("fixture changed: %s %v", file.Path, err)
			}
		}
	}
}

func testDigest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
