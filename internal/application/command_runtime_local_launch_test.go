//go:build windows

package application

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cyberagent-workbench/internal/commandruntimeadapter"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/drydock"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/sandbox"
)

// These tests only compile a launch manifest. The fixture executable is never
// started, including after its bytes or filesystem path have changed.
func TestLocalCommandLaunchRejectsChangedExecutableAndDirectory(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, runner.CommandRuntimeResolvedSpec)
	}{
		{"executable bytes", func(t *testing.T, spec runner.CommandRuntimeResolvedSpec) {
			file, err := os.OpenFile(spec.ExecutablePath, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			_, writeErr := file.Write([]byte("changed after normalization"))
			if err := errors.Join(writeErr, file.Close()); err != nil {
				t.Fatal(err)
			}
		}},
		{"missing executable", func(t *testing.T, spec runner.CommandRuntimeResolvedSpec) {
			if err := os.Remove(spec.ExecutablePath); err != nil {
				t.Fatal(err)
			}
		}},
		{"missing cwd", func(t *testing.T, spec runner.CommandRuntimeResolvedSpec) {
			if err := os.Remove(spec.AbsoluteDirectory); err != nil {
				t.Fatal(err)
			}
		}},
		{"cwd replaced by file", func(t *testing.T, spec runner.CommandRuntimeResolvedSpec) {
			if err := os.Remove(spec.AbsoluteDirectory); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(spec.AbsoluteDirectory, []byte("not a directory"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			spec := localLaunchTestSpec(t)
			if _, err := localLaunchTestCompile(spec); err != nil {
				t.Fatalf("unchanged normalized launch failed: %v", err)
			}
			test.mutate(t, spec)
			request, err := localLaunchTestCompile(spec)
			if !errors.Is(err, runner.ErrCommandRuntimeBoundary) || request.Manifest.Command.Executable != "" {
				t.Fatalf("changed launch produced a runnable manifest: executable=%q err=%v", request.Manifest.Command.Executable, err)
			}
		})
	}
}

func localLaunchTestSpec(t *testing.T) runner.CommandRuntimeResolvedSpec {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(t.TempDir(), "tools", filepath.Base(self))
	if err := os.Mkdir(filepath.Dir(executable), 0o700); err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(self)
	if err != nil {
		t.Fatal(err)
	}
	target, err := os.OpenFile(executable, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		_ = source.Close()
		t.Fatal(err)
	}
	_, copyErr := io.Copy(target, source)
	if err := errors.Join(copyErr, target.Close(), source.Close()); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "work"), 0o700); err != nil {
		t.Fatal(err)
	}
	spec, err := runner.NormalizeCommandRuntimeSpec(runner.CommandRuntimeSpec{
		Version: runner.CommandRuntimeProtocolVersion, Profile: runner.CommandRuntimeProcess,
		Executable: executable, Arguments: []string{}, WorkingDirectory: "work",
		Environment: []runner.CommandRuntimeEnvironment{},
		StdinPolicy: runner.CommandRuntimeStdinClosed, CloseInitialStdin: true,
		TimeoutMilliseconds: 1000,
		Output: runner.CommandRuntimeOutputPolicy{InlineBytes: runner.MinCommandRuntimeInlineBytes,
			ArtifactBytes: runner.MinCommandRuntimeInlineBytes},
		Network: runner.CommandRuntimeNetworkDisabled, Credentials: runner.CommandRuntimeCredentialsNone,
		Purpose: "verify Local launch freshness without starting a process",
	}, root)
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func localLaunchTestCompile(spec runner.CommandRuntimeResolvedSpec) (sandbox.LocalRunRequest, error) {
	digest := strings.Repeat("a", 64)
	executor := &LocalSandboxCommandRuntimeExecutor{backend: localCompileBackend{},
		identity: commandruntimeadapter.Identity{Generation: digest}}
	workspace := drydock.Workspace{ID: "drydock-test", Path: spec.WorkspaceRoot,
		Generation: 1, RootFingerprint: digest, ExpectedBindingFingerprint: digest}
	return executor.compile(runner.CommandRuntimeScope{RunID: "run-test", MissionID: "mission-test",
		SessionID: "session-test", WorkspaceID: "source-workspace", OperationKey: "test-operation"},
		spec, workspace, domain.RunExecutionProfileSnapshot{ID: "profile-test", Revision: 1},
		domain.RunExecutionPermissionSnapshot{ID: "permission-test", Revision: 1},
		domain.RunExecutionInteractionSnapshot{ID: "interaction-test", Revision: 1},
		domain.RunExecutionLease{LeaseID: "lease-test", Generation: 1})
}
