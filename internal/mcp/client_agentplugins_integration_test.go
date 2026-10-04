package mcp

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"cyberagent-workbench/internal/agentpackages"
	"cyberagent-workbench/internal/toolcontract"
)

// This is A's controlled shared fixture, not an upstream server integration.
func TestAgentPluginsSharedLaunchHandoff(t *testing.T) {
	for _, basename := range []string{"plugin", "literal-${PLUGIN_DATA}"} {
		t.Run(basename, func(t *testing.T) {
			base := t.TempDir()
			root, data := filepath.Join(base, basename), filepath.Join(base, "data")
			for _, dir := range []string{root, filepath.Join(root, "bin"), filepath.Join(root, "work"), filepath.Join(root, "work", "bin"),
				filepath.Join(data, "work", "bin"), filepath.Join(base, "outside", "bin"), filepath.Join(data, "outside")} {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			// Existing wrong-location executables make a misplaced command or escaped
			// cwd fail the assertion, rather than accidentally fail file lookup.
			for _, path := range []string{filepath.Join(root, "bin", "helper.exe"), filepath.Join(root, "work", "bin", "helper.exe"),
				filepath.Join(data, "work", "bin", "helper.exe"), filepath.Join(base, "outside", "helper.exe"),
				filepath.Join(base, "outside", "bin", "helper.exe"), filepath.Join(data, "outside", "helper.exe")} {
				if err := os.WriteFile(path, []byte("resolution-only-fixture"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			for _, name := range []string{"plugin.json", "mcp.json"} {
				content, err := os.ReadFile(filepath.Join("..", "agentpackages", "testdata", "launch-handoff", name))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, name), content, 0600); err != nil {
					t.Fatal(err)
				}
			}
			pkg, err := agentpackages.OpenDirectory(t.Context(), root, "handoff", toolcontract.SourceRef{URI: "test:launch-handoff", Revision: "fixture-1"})
			if err != nil {
				t.Fatal(err)
			}
			defer pkg.Close()
			if len(pkg.Launches()) != 6 || len(pkg.Diagnostics()) != 1 {
				t.Fatal("A handoff case count changed")
			}
			root, err = canonicalLaunchDirectory(root)
			if err != nil {
				t.Fatal(err)
			}
			data, err = canonicalLaunchDirectory(data)
			if err != nil {
				t.Fatal(err)
			}
			env := map[string]string{"UNKNOWN": "must-not-leak", "HOME": "must-not-leak", "TEMP": "must-not-leak"}
			if runtime.GOOS == "windows" {
				env["SystemRoot"] = os.Getenv("SystemRoot")
			}
			host := toolcontract.LaunchContext{InstanceID: "handoff", InstallRoot: root, DataRoot: data, BaseEnv: env, ProtocolVersions: []string{legacyClientProtocolVersion}}
			for _, declaration := range pkg.Launches() {
				name, _, found := pkg.ServerSource(declaration.Component)
				if !found {
					t.Fatal("A lost source identity")
				}
				t.Run(name, func(t *testing.T) {
					resolved, err := ResolveLaunch(declaration, host)
					if strings.HasPrefix(name, "escape-") {
						if err == nil {
							t.Fatal("escaped package/data path accepted")
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					if name == "http-literals" {
						if resolved.HTTP.Endpoint != declaration.HTTP.Endpoint || resolved.HTTP.Headers["X-Literal"] != declaration.HTTP.Headers["X-Literal"] {
							t.Fatal("HTTP configuration did not remain literal")
						}
						return
					}
					cwd := root
					switch name {
					case "command-versus-data-cwd":
						cwd = filepath.Join(data, "work")
						if !slices.Equal(resolved.Stdio.Args, []string{root + "/entry", data + "/cache", "${UNKNOWN}", "$HOME", "%TEMP%"}) ||
							resolved.Stdio.Env["VALUE"] != root+"|"+data+"|${UNKNOWN}|$HOME|%TEMP%" {
							t.Error("args/env escaped the one-pass placeholder rules")
						}
					case "command-versus-plugin-cwd":
						cwd = filepath.Join(root, "work")
					}
					if resolved.Stdio.Command != filepath.Join(root, "bin", "helper.exe") || resolved.Stdio.Cwd != cwd {
						t.Error("command and cwd were not resolved against their separate roots")
					}
				})
			}
		})
	}
}

// Build against A's actual immutable agentpackages source, either after normal
// integration or with the review evidence's Go overlay. Do not emulate Launches.
func agentPluginLaunchFixture(t *testing.T, server map[string]any) (toolcontract.LaunchDeclaration, toolcontract.LaunchContext) {
	t.Helper()
	base := t.TempDir()
	root, data := filepath.Join(base, "plugin"), filepath.Join(base, "data")
	for _, directory := range []string{root, data, filepath.Join(root, "bin"), filepath.Join(data, "bin"), filepath.Join(base, "outside")} {
		if err := os.MkdirAll(directory, 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range []struct{ path, content string }{
		{filepath.Join(root, "bin", "tool.exe"), "plugin executable"},
		{filepath.Join(data, "bin", "tool.exe"), "data decoy"},
		{filepath.Join(base, "outside", "tool.exe"), "outside decoy"},
	} {
		if err := os.WriteFile(item.path, []byte(item.content), 0755); err != nil {
			t.Fatal(err)
		}
	}
	manifest := []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"integration-fixture"}`)
	if err := os.WriteFile(filepath.Join(root, "plugin.json"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	configuration, err := json.Marshal(map[string]any{"$schema": "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json", "mcpServers": map[string]any{"fixture": server}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "mcp.json"), configuration, 0600); err != nil {
		t.Fatal(err)
	}
	reader, err := agentpackages.OpenDirectory(t.Context(), root, "fixture-package", toolcontract.SourceRef{URI: "fixture://agent-plugin", Revision: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	launches := reader.Launches()
	if len(launches) != 1 {
		t.Fatalf("A did not emit the expected declaration: %d", len(launches))
	}
	baseEnv := map[string]string{"BASE_SECRET": "must-not-be-expanded", "PLUGIN_ROOT": "ambient-root", "PLUGIN_DATA": "ambient-data", "PATH": filepath.Join(root, "bin")}
	if runtime.GOOS == "windows" {
		baseEnv["SystemRoot"] = os.Getenv("SystemRoot")
	}
	return launches[0], toolcontract.LaunchContext{InstanceID: "fixture-instance", InstallRoot: root, DataRoot: data, BaseEnv: baseEnv, ProtocolVersions: []string{legacyClientProtocolVersion}}
}

func TestAgentPluginsLaunchesUseRootCommandWithDataCwd(t *testing.T) {
	declaration, host := agentPluginLaunchFixture(t, map[string]any{"type": "stdio", "command": "./bin/tool.exe", "cwd": "${PLUGIN_DATA}"})
	resolved, err := ResolveLaunch(declaration, host)
	if err != nil {
		t.Fatal(err)
	}
	root, _ := canonicalLaunchDirectory(host.InstallRoot)
	data, _ := canonicalLaunchDirectory(host.DataRoot)
	if resolved.Stdio.Command != filepath.Join(root, "bin", "tool.exe") || resolved.Stdio.Cwd != data {
		t.Fatal("portable command followed cwd instead of plugin root")
	}
}

func TestAgentPluginsLaunchesPreserveHTTPLiterals(t *testing.T) {
	endpoint := "https://example.test/mcp?literal=${PLUGIN_ROOT}&token=${BASE_SECRET}"
	headers := map[string]string{"X-Literal": "${PLUGIN_ROOT}", "X-Unknown": "${BASE_SECRET}", "X-Malformed": "${unfinished"}
	declaration, host := agentPluginLaunchFixture(t, map[string]any{"type": "streamable-http", "url": endpoint, "headers": headers})
	resolved, err := ResolveLaunch(declaration, host)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.HTTP.Endpoint != endpoint {
		t.Fatal("portable HTTP URL was expanded")
	}
	for key, expected := range headers {
		if resolved.HTTP.Headers[key] != expected {
			t.Fatalf("portable HTTP header %s was expanded", key)
		}
	}
}

func TestAgentPluginsLaunchesExpandOnlyReservedPlaceholders(t *testing.T) {
	args := []string{"${PLUGIN_ROOT}", "${PLUGIN_DATA}", "${BASE_SECRET}", "${plugin_root}", "${unfinished", "./../opaque-argument"}
	declaration, host := agentPluginLaunchFixture(t, map[string]any{"type": "stdio", "command": "./bin/tool.exe", "args": args, "env": map[string]string{"VALUE": "${BASE_SECRET}", "ROOT_COPY": "${PLUGIN_ROOT}", "DATA_COPY": "${PLUGIN_DATA}"}})
	resolved, err := ResolveLaunch(declaration, host)
	if err != nil {
		t.Fatal(err)
	}
	root, _ := canonicalLaunchDirectory(host.InstallRoot)
	data, _ := canonicalLaunchDirectory(host.DataRoot)
	want := append([]string{root, data}, args[2:]...)
	for index, expected := range want {
		if resolved.Stdio.Args[index] != expected {
			t.Fatalf("argument %d expansion differs", index)
		}
	}
	for key, expected := range map[string]string{"VALUE": "${BASE_SECRET}", "ROOT_COPY": root, "DATA_COPY": data, "PLUGIN_ROOT": root, "PLUGIN_DATA": data} {
		if resolved.Stdio.Env[key] != expected {
			t.Fatalf("environment %s expansion differs", key)
		}
	}
	value, err := expandAgentPluginValue("${PLUGIN_ROOT}", "literal-${PLUGIN_DATA}", "must-not-expand")
	if err != nil || value != "literal-${PLUGIN_DATA}" {
		t.Fatal("replacement text was recursively expanded")
	}
}

func TestAgentPluginsLaunchesRejectRootEscapes(t *testing.T) {
	for _, cwd := range []string{"./..", "${PLUGIN_ROOT}/..", "${PLUGIN_DATA}/.."} {
		t.Run(cwd, func(t *testing.T) {
			declaration, host := agentPluginLaunchFixture(t, map[string]any{"type": "stdio", "command": "tool.exe", "cwd": cwd})
			if _, err := ResolveLaunch(declaration, host); err == nil {
				t.Fatal("cwd escaped its declared root")
			}
		})
	}
	declaration, host := agentPluginLaunchFixture(t, map[string]any{"type": "stdio", "command": "./../outside/tool.exe"})
	if _, err := ResolveLaunch(declaration, host); err == nil {
		t.Fatal("command escaped its plugin root")
	}
}

func integrationDirectoryLink(t *testing.T, name, target string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		// Both paths are generated beneath this test's controlled temp root.
		// Junction creation needs no symlink privilege or host policy change.
		output, err := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", name, target).CombinedOutput()
		if err != nil {
			t.Fatalf("create fixture junction: %v: %s", err, output)
		}
	} else if err := os.Symlink(target, name); err != nil {
		t.Fatal(err)
	}
}

func TestAgentPluginsLaunchesEnforceFilesystemResolvedContainment(t *testing.T) {
	for _, field := range []string{"command", "root-cwd", "data-cwd"} {
		t.Run(field, func(t *testing.T) {
			server := map[string]any{"type": "stdio", "command": "tool.exe"}
			switch field {
			case "command":
				server["command"] = "./linked/tool.exe"
			case "root-cwd":
				server["cwd"] = "./linked"
			case "data-cwd":
				server["cwd"] = "${PLUGIN_DATA}/linked"
			}
			declaration, host := agentPluginLaunchFixture(t, server)
			linkRoot := host.InstallRoot
			if field == "data-cwd" {
				linkRoot = host.DataRoot
			}
			integrationDirectoryLink(t, filepath.Join(linkRoot, "linked"), filepath.Join(filepath.Dir(host.InstallRoot), "outside"))
			if _, err := ResolveLaunch(declaration, host); err == nil {
				t.Fatal("filesystem alias escaped its declared root")
			}
		})
	}
	declaration, host := agentPluginLaunchFixture(t, map[string]any{"type": "stdio", "command": "./linked/tool.exe", "cwd": "./linked"})
	integrationDirectoryLink(t, filepath.Join(host.InstallRoot, "linked"), filepath.Join(host.InstallRoot, "bin"))
	if _, err := ResolveLaunch(declaration, host); err != nil {
		t.Fatalf("contained filesystem alias rejected: %v", err)
	}
}

func TestAgentPluginsLaunchFormatMustBeExplicitlySupported(t *testing.T) {
	declaration, host := agentPluginLaunchFixture(t, map[string]any{"type": "stdio", "command": "./bin/tool.exe"})
	for _, version := range []string{"1", "2.0.0"} {
		declaration.FormatVersion = version
		if _, err := ResolveLaunch(declaration, host); err == nil {
			t.Fatal("unknown format version accepted")
		}
	}
	declaration.FormatVersion = "1.0.0"
	declaration.Format = "native-other"
	if _, err := ResolveLaunch(declaration, host); err == nil {
		t.Fatal("native format silently used portable rules")
	}
}

func TestAgentPluginsBareCommandDoesNotExpandPlaceholders(t *testing.T) {
	declaration, host := agentPluginLaunchFixture(t, map[string]any{"type": "stdio", "command": "${TOOL}", "args": []string{}})
	host.BaseEnv["TOOL"] = filepath.Join(host.InstallRoot, "bin", "tool.exe")
	if _, err := ResolveLaunch(declaration, host); err == nil {
		t.Fatal("command placeholder expanded into an executable")
	}
}

func TestAgentPluginsWindowsNpxAndUvxResolution(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows executable search fixture")
	}
	declaration, host := agentPluginLaunchFixture(t, map[string]any{"type": "stdio", "command": "uvx", "args": []string{"literal argument"}})
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(host.InstallRoot, "bin", "uvx.exe"), content, 0755); err != nil {
		t.Fatal(err)
	}
	host.BaseEnv["PATH"] = filepath.Join(host.InstallRoot, "bin")
	host.BaseEnv["PATHEXT"] = ".COM;.EXE;.BAT;.CMD"
	resolved, err := ResolveLaunch(declaration, host)
	if err != nil || !strings.HasSuffix(strings.ToLower(resolved.Stdio.Command), "uvx.exe") || !bytes.Equal([]byte(resolved.Stdio.Args[0]), []byte("literal argument")) {
		t.Fatalf("uvx executable fixture failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(host.InstallRoot, "bin", "npx.cmd"), []byte("@echo fixture-only\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	declaration.Stdio.Command = "npx"
	if _, err := ResolveLaunch(declaration, host); err == nil || !strings.Contains(err.Error(), "interpreter") {
		t.Fatalf("npx.cmd must report the explicit unsupported interpreter boundary: %v", err)
	}
}
