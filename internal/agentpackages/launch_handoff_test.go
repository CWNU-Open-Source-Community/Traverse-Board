package agentpackages_test

import (
	"context"
	"slices"
	"testing"

	"cyberagent-workbench/internal/agentpackages"
	"cyberagent-workbench/internal/toolcontract"
)

// B can consume this same directory through the public loader before exercising
// its own resolver. This test checks the seam's raw inputs, not B's algorithm.
func TestPortableLaunchHandoffPreservesResolverInputs(t *testing.T) {
	pkg, err := agentpackages.OpenDirectory(context.Background(), "testdata/launch-handoff", "interop-installation", toolcontract.SourceRef{URI: "test:launch-handoff", Revision: "fixture-1"})
	if err != nil {
		t.Fatal(err)
	}
	defer pkg.Close()
	launches := pkg.Launches()
	if len(launches) != 6 || len(pkg.Diagnostics()) != 1 || pkg.Diagnostics()[0].Code != "mcp_server_invalid" {
		t.Fatal("unexpected valid/invalid component split")
	}
	seen := map[string]bool{}
	for _, launch := range launches {
		key, source, found := pkg.ServerSource(launch.Component)
		if !found || source.Path != "mcp.json" || source.SHA256 == "" || launch.Validate() != nil || launch.Format != agentpackages.FormatAgentPlugin || launch.FormatVersion != "1.0.0" {
			t.Fatal("public launch/source contract lost")
		}
		seen[key] = true
		switch key {
		case "http-literals":
			if launch.HTTP == nil || launch.HTTP.Endpoint != "https://example.invalid/mcp?root=${PLUGIN_ROOT}&data=${PLUGIN_DATA}&unknown=${UNKNOWN}" || launch.HTTP.Headers["X-Literal"] != "${PLUGIN_ROOT}|${PLUGIN_DATA}|${UNKNOWN}|$HOME|%TEMP%" {
				t.Fatal("HTTP literals were expanded or normalized")
			}
		case "command-versus-data-cwd":
			if launch.Stdio == nil || launch.Stdio.Command != "./bin/helper.exe" || launch.Stdio.Cwd != "${PLUGIN_DATA}/work" || !slices.Equal(launch.Stdio.Args, []string{"${PLUGIN_ROOT}/entry", "${PLUGIN_DATA}/cache", "${UNKNOWN}", "$HOME", "%TEMP%"}) || launch.Stdio.Env["VALUE"] != "${PLUGIN_ROOT}|${PLUGIN_DATA}|${UNKNOWN}|$HOME|%TEMP%" {
				t.Fatal("A rewrote B's command/cwd/placeholder inputs")
			}
		case "command-versus-plugin-cwd":
			if launch.Stdio == nil || launch.Stdio.Command != "./bin/helper.exe" || launch.Stdio.Cwd != "./work" {
				t.Fatal("plugin-relative launch rewritten")
			}
		case "default-cwd":
			if launch.Stdio == nil || launch.Stdio.Cwd != "" {
				t.Fatal("A supplied an ambient working directory")
			}
		case "escape-command":
			if launch.Stdio == nil || launch.Stdio.Command != "./../outside/helper.exe" {
				t.Fatal("A consumed B's resolution boundary")
			}
		case "escape-data-cwd":
			if launch.Stdio == nil || launch.Stdio.Cwd != "${PLUGIN_DATA}/../outside" {
				t.Fatal("A consumed B's data-root resolution boundary")
			}
		default:
			t.Fatalf("unexpected resolver input case %q", key)
		}
	}
	if len(seen) != 6 {
		t.Fatal("launch cases are not independently identifiable")
	}
}
