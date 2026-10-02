package application

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"

	"cyberagent-workbench/internal/mcp"
	"cyberagent-workbench/internal/plugins"
)

func TestNativeMCPSourceChecksExactInstallationAndMaterialization(t *testing.T) {
	for _, mutation := range []string{"bytes", "extra-file", "repository-administration", "root-replacement"} {
		t.Run(mutation, func(t *testing.T) {
			state, run, _, _, _ := newCommandRuntimeTestRuntime(t, t.Context())
			config := []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/mcp.schema.json","mcpServers":{"peer":{"type":"streamable-http","url":"https://example.invalid/mcp"}}}`)
			installed := importNativeMCPFixture(t, state, config, nil)
			stateRoot := t.TempDir()
			resolver, err := NewNativeMCPSourceResolver(state, stateRoot)
			if err != nil {
				t.Fatal(err)
			}
			manager, err := mcp.NewClientManager(state, nil, mcp.ManagerOptions{NativeSources: resolver})
			if err != nil {
				t.Fatal(err)
			}
			service, _ := plugins.NewService(state)
			records, err := service.StageMCPServers(t.Context(), installed.ID, mcp.ScopeRun, run.ID, "workspace-command-runtime-app", manager)
			if err != nil || len(records) != 1 {
				t.Fatal(err)
			}
			ref := *records[0].Descriptor.NativeSource
			for _, field := range []string{"surface", "generation", "revision", "package"} {
				changed := ref
				switch field {
				case "surface":
					changed.Surface = "cyber"
				case "generation":
					changed.InstallationGeneration++
				case "revision":
					changed.Revision = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
				case "package":
					changed.Component.PackageID = "different-package"
				}
				if resolver.Check(t.Context(), changed, run.ID) == nil {
					t.Fatal("mismatched source passed", field)
				}
			}
			_, _, recheck, closeSource, err := resolver.Resolve(t.Context(), ref, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			defer closeSource()
			roots, err := filepath.Glob(filepath.Join(stateRoot, "runtime", "agent-package-read-*", "native-mcp"))
			if err != nil || len(roots) != 1 {
				t.Fatal("expected a single transient retained object", err, roots)
			}
			root := roots[0]
			name := "mcp.json"
			switch mutation {
			case "extra-file":
				name = "new-file"
			case "repository-administration":
				name = ".git"
			case "root-replacement":
				if err := os.Rename(root, root+"-held"); err != nil {
					if runtime.GOOS == "windows" && errors.Is(err, syscall.Errno(32)) {
						if err := recheck(t.Context()); err != nil {
							t.Fatal("unchanged held root failed recheck", err)
						}
						t.Log("Windows held root prevented rename with ERROR_SHARING_VIOLATION")
						return
					}
					t.Fatal(err)
				}
				if err := os.Mkdir(root, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(root, name), []byte("changed"), 0600); err != nil {
				t.Fatal(err)
			}
			if recheck(t.Context()) == nil {
				t.Fatal("changed source retained launch authority")
			}
		})
	}
}
