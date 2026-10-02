package agentpackages

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"cyberagent-workbench/internal/toolcontract"
)

func TestStandardYAMLDocumentAndAliasBoundaries(t *testing.T) {
	for _, test := range []struct {
		name, yaml string
		valid      bool
	}{
		{"scalar_alias", "name: &identity good\ndescription: *identity\nmetadata:\n  owner: &owner team\n  contact: *owner\n", true},
		{"recursive_alias", "name: good\ndescription: OK\nmetadata: &cycle\n  owner: *cycle\n", false},
		{"non_string_alias", "name: &identity [good]\ndescription: *identity\n", false},
		{"unknown_alias", "name: good\ndescription: *undefined\n", false},
		{"hidden_second_document", "name: good\ndescription: first\n...\n--- # second document\nname: alternate\ndescription: ignored second document\n", false},
		{"junk_after_document_end", "name: good\ndescription: first\n...\nnot another document\n", false},
		{"single_explicit_end", "name: good\ndescription: first\n...\n", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := []byte("---\n" + test.yaml + "---\nOriginal instructions.\n")
			f := portablePlugin()
			f["skills/good/SKILL.md"] = &fstest.MapFile{Data: raw}
			f["mcp.json"] = &fstest.MapFile{Data: mcpDocument(`{"ok":{"type":"stdio","command":"node"}}`)}
			pkg, err := inspectPlugin(context.Background(), f)
			if err != nil || len(pkg.servers) != 1 {
				t.Fatalf("frontmatter failure escaped its component: %v", err)
			}
			if (len(pkg.skills) == 1) != test.valid {
				t.Fatalf("YAML valid=%v, loaded skills=%d; diagnostics=%v", test.valid, len(pkg.skills), pkg.diagnostics)
			}
			if test.valid {
				loaded, err := readContent(context.Background(), f, pkg.skills[0].instructions, 10000)
				if err != nil || !bytes.Equal(loaded, raw) {
					t.Fatal("YAML aliases altered original content")
				}
			}
		})
	}
}

func TestStandardFormatPriority(t *testing.T) {
	for _, test := range []struct {
		name    string
		files   fstest.MapFS
		format  string
		failure bool
	}{
		{"portable_plugin_before_root_skill", fstest.MapFS{"plugin.json": portablePlugin()["plugin.json"], "SKILL.md": {Data: sourceSkill("good")}}, FormatAgentPlugin, false},
		{"legacy_skill_before_frontmatter", fstest.MapFS{"manifest.json": {Data: []byte(`{"protocol":"skill.v1"}`)}, "SKILL.md": {Data: sourceSkill("good")}}, FormatLegacySkill, false},
		{"unknown_legacy_version", fstest.MapFS{"manifest.json": {Data: []byte(`{"protocol":"skill.v99"}`)}, "SKILL.md": {Data: sourceSkill("good")}}, "", true},
		{"unrelated_manifest_resource", fstest.MapFS{"manifest.json": {Data: []byte{0, 255, 1}}, "SKILL.md": {Data: sourceSkill("good")}}, FormatAgentSkill, false},
		{"native_marketplace_is_not_portable", fstest.MapFS{".claude-plugin/plugin.json": {Data: []byte(`{"name":"native"}`)}}, "", true},
		{"broken_root_plugin_cannot_downgrade", fstest.MapFS{"plugin.json": {Data: []byte(`not json`)}, "SKILL.md": {Data: sourceSkill("good")}}, "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			format, err := detectFormat(context.Background(), test.files)
			if (err != nil) != test.failure || format != test.format {
				t.Fatalf("format=%s error=%v", format, err)
			}
		})
	}
}

func TestPublicResourceLinkReplacementIsConfined(t *testing.T) {
	rootPath := filepath.Join(t.TempDir(), "package")
	inside := filepath.Join(rootPath, "shared")
	outside := t.TempDir()
	for _, dir := range []string{inside, filepath.Join(rootPath, "skills", "good")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range map[string][]byte{
		filepath.Join(rootPath, "plugin.json"):                portablePlugin()["plugin.json"].Data,
		filepath.Join(rootPath, "skills", "good", "SKILL.md"): sourceSkill("good"),
		filepath.Join(inside, "data.bin"):                     {0, 255, 3},
		filepath.Join(outside, "data.bin"):                    {0, 255, 3},
	} {
		if err := os.WriteFile(name, content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(rootPath, "skills", "good", "assets")
	makeDirectoryLink(t, link, inside)
	pkg, err := OpenDirectory(context.Background(), rootPath, "host-installation", toolcontract.SourceRef{URI: "test:immutable-snapshot", Revision: "revision-1"})
	if err != nil {
		t.Fatal(err)
	}
	defer pkg.Close()
	ref, err := pkg.Resource(pkg.Skills()[0], "assets/data.bin", testDigest([]byte{0, 255, 3}))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := pkg.Read(context.Background(), ref, 3)
	if err != nil || !bytes.Equal(raw, []byte{0, 255, 3}) {
		t.Fatalf("valid contained resource link rejected: %v", err)
	}
	// This removes only our fixture link, not its target or an external tree.
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	makeDirectoryLink(t, link, outside)
	if _, err := pkg.Read(context.Background(), ref, 3); err == nil {
		t.Fatal("matching bytes/digest bypassed root containment after link replacement")
	}
}

func TestStandardYAMLAliasExpansionRemainsBounded(t *testing.T) {
	var header strings.Builder
	header.WriteString("---\nname: good\ndescription: OK\nmetadata:\n  a: &a [x,x,x,x,x,x,x,x,x,x]\n")
	previous := "a"
	for _, current := range []string{"b", "c", "d", "e", "f", "g", "h"} {
		header.WriteString("  " + current + ": &" + current + " [" + strings.Repeat("*"+previous+",", 9) + "*" + previous + "]\n")
		previous = current
	}
	header.WriteString("---\nbody\n")
	f := fstest.MapFS{"SKILL.md": {Data: []byte(header.String())}}
	if _, err := inspectNamedSkill(context.Background(), f, ".", "good"); err == nil {
		t.Fatal("amplifying non-string metadata aliases accepted")
	}
}
