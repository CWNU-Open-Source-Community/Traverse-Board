//go:build windows

package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCommandRuntimeNativeDevelopmentRuntimeRetainsLaunchBindings(t *testing.T) {
	for _, fixture := range []struct{ name, variable string }{
		{"node", "TRAVERSE_TEST_NODE_PATH"},
		{"python", "TRAVERSE_TEST_PYTHON_PATH"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			executable := os.Getenv(fixture.variable)
			if executable == "" {
				t.Skipf("set %s to test an installed native runtime without launching it", fixture.variable)
			}
			file, err := os.Open(executable)
			if err != nil {
				t.Fatal(err)
			}
			digest := sha256.New()
			_, readErr := io.Copy(digest, file)
			closeErr := file.Close()
			if readErr != nil || closeErr != nil {
				t.Fatal(errors.Join(readErr, closeErr))
			}
			root := t.TempDir()
			arguments := []string{"--version", "argument with spaces", "$(literal-not-a-shell)", ";"}
			spec := CommandRuntimeSpec{
				Version: CommandRuntimeProtocolVersion, Profile: CommandRuntimeProcess,
				Executable: executable, Arguments: arguments, WorkingDirectory: ".",
				Environment: []CommandRuntimeEnvironment{},
				StdinPolicy: CommandRuntimeStdinClosed, CloseInitialStdin: true,
				TimeoutMilliseconds: 1000,
				Output:              CommandRuntimeOutputPolicy{InlineBytes: MinCommandRuntimeInlineBytes, ArtifactBytes: MinCommandRuntimeInlineBytes},
				Network:             CommandRuntimeNetworkDisabled, Credentials: CommandRuntimeCredentialsNone,
				Purpose: "inspect native development runtime launch bindings without execution",
			}
			for _, normalize := range []func(CommandRuntimeSpec, string) (CommandRuntimeResolvedSpec, error){NormalizeCommandRuntimeSpec, NormalizeLocalSandboxCommandRuntimeSpec} {
				resolved, err := normalize(spec, root)
				if err != nil {
					t.Fatal(err)
				}
				if !commandRuntimePathEqual(resolved.ExecutablePath, executable) ||
					resolved.ExecutableSHA256 != hex.EncodeToString(digest.Sum(nil)) ||
					!resolved.ExecutablePinned || resolved.ProfileStartupFiles || resolved.EnvironmentInherited ||
					resolved.Spec.Profile != CommandRuntimeProcess || !reflect.DeepEqual(resolved.CanonicalArgv, arguments) ||
					!commandRuntimePathEqual(resolved.WorkspaceRoot, root) || !commandRuntimePathEqual(resolved.AbsoluteDirectory, root) ||
					resolved.Spec.Network != CommandRuntimeNetworkDisabled || resolved.Spec.Credentials != CommandRuntimeCredentialsNone {
					t.Fatalf("native runtime changed the existing launch contract: %#v", resolved)
				}
			}
			for _, change := range []string{"relative executable", "workspace executable", "script target"} {
				t.Run(change, func(t *testing.T) {
					invalid, workspace := spec, root
					switch change {
					case "relative executable":
						invalid.Executable = filepath.Base(executable)
					case "workspace executable":
						workspace = filepath.Dir(executable)
					case "script target":
						invalid.Executable = filepath.Join(t.TempDir(), "test-script.py")
						if err := os.WriteFile(invalid.Executable, []byte("print('not a native image')\n"), 0o600); err != nil {
							t.Fatal(err)
						}
					}
					if _, err := NormalizeCommandRuntimeSpec(invalid, workspace); !errors.Is(err, ErrCommandRuntimeBoundary) {
						t.Fatalf("native runtime widened %s boundary: %v", change, err)
					}
				})
			}
		})
	}
}
