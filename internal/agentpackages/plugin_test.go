package agentpackages

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"
)

func TestStandardPluginFailureBoundaries(t *testing.T) {
	tests := []struct {
		name            string
		alter           func(fstest.MapFS)
		skills, servers int
		boundary        string
		fatal           bool
	}{
		{"bad_skill", func(f fstest.MapFS) {
			f["skills/bad/SKILL.md"] = &fstest.MapFile{Data: []byte("no yaml")}
			f["mcp.json"] = &fstest.MapFile{Data: mcpDocument(`{"ok":{"type":"stdio","command":"node"}}`)}
		}, 1, 1, "skill", false},
		{"wrong_skills_kind", func(f fstest.MapFS) {
			delete(f, "skills/good/SKILL.md")
			f["skills"] = &fstest.MapFile{Data: []byte("not a directory")}
			f["mcp.json"] = &fstest.MapFile{Data: mcpDocument(`{"ok":{"type":"stdio","command":"node"}}`)}
		}, 0, 1, "skills", false},
		{"bad_mcp_json", func(f fstest.MapFS) { f["mcp.json"] = &fstest.MapFile{Data: []byte("{")} }, 1, 0, "mcp", false},
		{"wrong_mcp_kind", func(f fstest.MapFS) { f["mcp.json/file"] = &fstest.MapFile{Data: []byte("x")} }, 1, 0, "mcp", false},
		{"mcp_schema_mismatch", func(f fstest.MapFS) {
			f["mcp.json"] = &fstest.MapFile{Data: []byte(`{"$schema":"https://agent-plugins.org/schemas/2.0.0/mcp.schema.json","mcpServers":{}}`)}
		}, 1, 0, "mcp", false},
		{"mcp_unknown_field", func(f fstest.MapFS) {
			f["mcp.json"] = &fstest.MapFile{Data: []byte(`{"$schema":"` + mcpSchema + `","mcpServers":{},"extra":1}`)}
		}, 1, 0, "mcp", false},
		{"one_invalid_server", func(f fstest.MapFS) {
			f["mcp.json"] = &fstest.MapFile{Data: mcpDocument(`{"bad":{"type":"stdio","command":"node","url":"https://example.org"},"ok":{"type":"stdio","command":"node"}}`)}
		}, 1, 1, "server", false},
		{"unknown_manifest_field", func(f fstest.MapFS) { setManifest(f, `"unknown":{"secret":"not-in-diagnostic"}`) }, 1, 0, "manifest", false},
		{"non_object_extensions", func(f fstest.MapFS) { setManifest(f, `"extensions":false`) }, 1, 0, "manifest", false},
		{"unknown_extension_ignored", func(f fstest.MapFS) { setManifest(f, `"extensions":{"com.example.host":false}`) }, 1, 0, "", false},
		{"bad_author", func(f fstest.MapFS) { setManifest(f, `"author":{"unexpected":"x"}`) }, 0, 0, "", true},
		{"null_metadata", func(f fstest.MapFS) { setManifest(f, `"version":null`) }, 0, 0, "", true},
		{"invalid_schema", func(f fstest.MapFS) {
			f["plugin.json"] = &fstest.MapFile{Data: []byte(`{"$schema":"https://example.org/schema.json","name":"example"}`)}
		}, 0, 0, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := portablePlugin()
			tt.alter(files)
			source := &observedFS{FS: files}
			pkg, err := inspectPlugin(context.Background(), source)
			if tt.fatal {
				if err == nil {
					t.Fatal("invalid manifest accepted")
				}
				for _, opened := range source.opened {
					if strings.HasPrefix(opened, "skills") || opened == "mcp.json" {
						t.Fatal("components read after invalid manifest")
					}
				}
				return
			}
			if err != nil || len(pkg.skills) != tt.skills || len(pkg.servers) != tt.servers {
				t.Fatalf("wrong failure boundary: %v, skills=%d servers=%d", err, len(pkg.skills), len(pkg.servers))
			}
			found := tt.boundary == ""
			for _, d := range pkg.diagnostics {
				if d.boundary == tt.boundary {
					found = true
				}
				if strings.Contains(d.code, "secret") || strings.Contains(d.code, "not-in-diagnostic") {
					t.Fatal("configuration leaked")
				}
			}
			if !found {
				t.Fatalf("missing diagnostic: %#v", pkg.diagnostics)
			}
		})
	}
}

func setManifest(f fstest.MapFS, fields string) {
	f["plugin.json"] = &fstest.MapFile{Data: []byte(`{"$schema":"` + pluginSchema + `","name":"example",` + fields + `}`)}
}

func TestStandardMCPPreservesUnexpandedDeclarations(t *testing.T) {
	declarations := `{"local":{"type":"stdio","command":"node","args":["${PLUGIN_ROOT}/index.js","--value=a b"],"cwd":"${PLUGIN_DATA}/cache","env":{"CACHE":"${PLUGIN_DATA}","UNRECOGNIZED":"${HOME}"}},"remote":{"type":"streamable-http","url":"https://example.org/mcp","headers":{"X-Literal":"${PLUGIN_ROOT}"}},"legacy":{"type":"sse","url":"http://127.0.0.1/events"}}`
	files := portablePlugin()
	files["mcp.json"] = &fstest.MapFile{Data: mcpDocument(declarations)}
	pkg, err := inspectPlugin(context.Background(), files)
	if err != nil || len(pkg.servers) != 3 {
		t.Fatalf("portable declarations rejected: %v %#v", err, pkg.diagnostics)
	}
	var originals map[string]json.RawMessage
	if err := json.Unmarshal([]byte(declarations), &originals); err != nil {
		t.Fatal(err)
	}
	for _, server := range pkg.servers {
		if !bytes.Equal(server.declaration, originals[server.key]) || server.sha256 != testDigest(server.declaration) {
			t.Fatalf("launch declaration was rewritten: %s", server.key)
		}
	}
}

func TestStandardMCPInvalidEntriesAreIsolated(t *testing.T) {
	for name, invalid := range map[string]string{
		"missing_type": `{"command":"node"}`, "unknown_type": `{"type":"custom","command":"node"}`,
		"null_args": `{"type":"stdio","command":"node","args":null}`, "wrong_arg_type": `{"type":"stdio","command":"node","args":[1]}`,
		"bad_cwd": `{"type":"stdio","command":"node","cwd":"tmp"}`, "absolute_command": `{"type":"stdio","command":"/bin/node"}`,
		"reserved_env": `{"type":"stdio","command":"node","env":{"PLUGIN_DATA":"x"}}`, "command_placeholder": `{"type":"stdio","command":"${PLUGIN_ROOT}/bin/run"}`,
		"mixed_variant": `{"type":"stdio","command":"node","headers":{}}`, "remote_http": `{"type":"streamable-http","url":"http://example.org/mcp"}`,
		"remote_userinfo": `{"type":"streamable-http","url":"https://name:secret@example.org/mcp"}`, "remote_fragment": `{"type":"streamable-http","url":"https://example.org/mcp#x"}`,
		"duplicate_header_case": `{"type":"streamable-http","url":"https://example.org/mcp","headers":{"X-Key":"a","x-key":"b"}}`,
		"header_newline":        `{"type":"streamable-http","url":"https://example.org/mcp","headers":{"X-Key":"a\r\nb"}}`,
		"null_header":           `{"type":"streamable-http","url":"https://example.org/mcp","headers":{"X-Key":null}}`,
	} {
		t.Run(name, func(t *testing.T) {
			files := portablePlugin()
			files["mcp.json"] = &fstest.MapFile{Data: mcpDocument(`{"bad":` + invalid + `,"ok":{"type":"stdio","command":"node"}}`)}
			pkg, err := inspectPlugin(context.Background(), files)
			if err != nil || len(pkg.servers) != 1 || pkg.servers[0].key != "ok" || len(pkg.diagnostics) != 1 {
				t.Fatalf("invalid member affected sibling: %v %#v", err, pkg)
			}
		})
	}
}
