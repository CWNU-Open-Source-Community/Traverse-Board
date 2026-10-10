//go:build windows

package sandbox

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func localProcessProofFixture(t *testing.T) (*windowsLocalBackend, LocalRunRequest) {
	t.Helper()
	base, err := os.MkdirTemp("", "local-proof-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(base); err != nil {
			t.Error(err)
		}
	})
	base = windowsTestCanonicalRoot(t, base)
	drydock := filepath.Join(base, "drydock")
	if err := os.Mkdir(drydock, 0700); err != nil {
		t.Fatal(err)
	}
	backend, err := NewPlatformLocalBackend(WithLocalOwnerRoot(filepath.Join(base, "owners")))
	if err != nil {
		t.Fatal(err)
	}
	b := backend.(*windowsLocalBackend)
	t.Cleanup(func() {
		if err := b.Close(); err != nil {
			t.Error(err)
		}
	})
	systemDirectory, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	tools := filepath.Join(base, "tools")
	if err := os.Mkdir(tools, 0700); err != nil {
		t.Fatal(err)
	}
	// A copied native executable keeps this fixture independent of unrelated,
	// unreadable directories below an installed Windows runtime root.
	executable, err := os.ReadFile(filepath.Join(systemDirectory, "cmd.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tools, "cmd.exe"), executable, 0700); err != nil {
		t.Fatal(err)
	}
	request := localWindowsTestRequest(t, b, drydock, tools, "/tools", "/tools/cmd.exe",
		[]string{"/d", "/c", "echo started>dispatch-proof.txt"})
	return b, request
}

func TestWindowsLocalBackendPreDispatchRejectionHasNoProcessTree(t *testing.T) {
	for _, kind := range []string{"closed", "stale", "cancelled", "nil_context", "stdin_policy", "missing_stdin"} {
		t.Run(kind, func(t *testing.T) {
			b, request := localProcessProofFixture(t)
			ctx := t.Context()
			expected := ErrLocalSandboxBoundary
			switch kind {
			case "closed":
				if err := b.Close(); err != nil {
					t.Fatal(err)
				}
				expected = ErrLocalSandboxUnavailable
			case "stale":
				request.Binding.RuntimeGeneration = localFingerprint("different-runtime")
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				expected = context.Canceled
			case "nil_context":
				ctx = nil
				expected = ErrLocalSandboxUnavailable
			case "stdin_policy", "missing_stdin":
				request.StdinPipe = true
			}
			var result LocalExecutionResult
			var err error
			if kind == "missing_stdin" {
				result, err = b.RunWithStdin(ctx, request, nil)
			} else {
				result, err = b.Run(ctx, request)
			}
			if !errors.Is(err, expected) || !result.TreeReaped || result.ExitCode != 125 || !result.StartedAt.IsZero() {
				t.Fatalf("known pre-dispatch rejection became uncertain: result=%+v error=%v", result, err)
			}
			if _, err := os.Stat(filepath.Join(request.Binding.DrydockRoot, "dispatch-proof.txt")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("rejected command was dispatched: %v", err)
			}
		})
	}
}

func TestWindowsLocalProcessPreCreationFailureHasNoProcessTree(t *testing.T) {
	profile, err := prepareLocalProfile(localFingerprint(t.Name()))
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"invalid_spec", "cancelled", "missing_executable", "invalid_executable"} {
		t.Run(kind, func(t *testing.T) {
			ctx := t.Context()
			spec := localProcessSpec{profile: profile, timeout: time.Second, writeMaximum: 4096,
				resources: ResourceLimits{MaxOutputBytes: 4096}, executable: filepath.Join(windowsTestTempDir(t), "missing.exe")}
			switch kind {
			case "invalid_spec":
				spec.profile = localAppContainerProfile{}
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "invalid_executable":
				if err := os.WriteFile(spec.executable, []byte("not a Windows executable"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			result, err := runLocalProcess(ctx, spec)
			if err == nil || !result.treeReaped || result.exitCode != 125 || !result.startedAt.IsZero() {
				t.Fatalf("native pre-creation failure became uncertain: result=%+v error=%v", result, err)
			}
		})
	}
}

func TestWindowsLocalBackendPreservesFailedNativeJobCleanupProof(t *testing.T) {
	b, request := localProcessProofFixture(t)
	queried := false
	result, err := b.runWithProcess(t.Context(), request, nil,
		func(ctx context.Context, spec localProcessSpec) (localProcessResult, error) {
			return runLocalProcessWithJobWait(ctx, spec,
				func(job windows.Handle, maximum time.Duration) (bool, error) {
					queried = true
					// The child and owned Job are real. Fault only the final
					// absence query using an invalid native handle; a failed
					// Windows query must not become a successful cleanup proof.
					if job == 0 || job == windows.InvalidHandle {
						t.Fatal("process transport did not create an owned Job")
					}
					return waitLocalJobReaped(windows.InvalidHandle, maximum)
				})
		})
	if !queried || !errors.Is(err, windows.ERROR_INVALID_HANDLE) || result.TreeReaped || result.StartedAt.IsZero() {
		t.Fatalf("native cleanup failure was hidden: queried=%t result=%+v error=%v", queried, result, err)
	}
	if data, err := os.ReadFile(filepath.Join(request.Binding.DrydockRoot, "dispatch-proof.txt")); err != nil || strings.TrimSpace(string(data)) != "started" {
		t.Fatalf("test did not dispatch its real Windows child: data=%q error=%v", data, err)
	}
}

func TestWindowsLocalProcessCreationFailureHasNoProcessTree(t *testing.T) {
	b, request := localProcessProofFixture(t)
	called := false
	result, err := b.runWithProcess(t.Context(), request, nil,
		func(ctx context.Context, spec localProcessSpec) (localProcessResult, error) {
			called = true
			// Reach the real CreateProcessAsUser call with valid native image,
			// profile, token, Job and handles, then reject its missing cwd.
			spec.workingDir = filepath.Join(spec.workingDir, "not-created")
			return runLocalProcess(ctx, spec)
		})
	if !called || !errors.Is(err, windows.ERROR_DIRECTORY) || !result.TreeReaped || result.ExitCode != 125 || !result.StartedAt.IsZero() {
		t.Fatalf("failed native creation claimed an owned tree: called=%t result=%+v error=%v", called, result, err)
	}
}

func TestWindowsLocalFailedStartCleanupRequiresWholeJobProof(t *testing.T) {
	for _, failedQuery := range []bool{false, true} {
		name := "confirmed"
		if failedQuery {
			name = "query_failed"
		}
		t.Run(name, func(t *testing.T) {
			job, err := newLocalJob(ResourceLimits{CPUQuotaMillis: 1000, MemoryBytes: 256 * 1024 * 1024, PIDs: 4})
			if err != nil {
				t.Fatal(err)
			}
			defer windows.CloseHandle(job)
			systemDirectory, err := windows.GetSystemDirectory()
			if err != nil {
				t.Fatal(err)
			}
			// Keep a real native child blocked on its owned stdin until Job
			// termination. This exercises failed-start cleanup independently
			// of the AppContainer token proof that rejected the startup.
			child := exec.Command(filepath.Join(systemDirectory, "cmd.exe"), "/d", "/c", "set /p ignored=")
			stdin, err := child.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer stdin.Close()
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = child.Process.Kill(); _ = child.Wait() }()
			process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|
				windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(child.Process.Pid))
			if err != nil {
				t.Fatal(err)
			}
			defer windows.CloseHandle(process)
			if err := windows.AssignProcessToJobObject(job, process); err != nil {
				t.Fatal(err)
			}
			waitJob := waitLocalJobReaped
			if failedQuery {
				waitJob = func(_ windows.Handle, maximum time.Duration) (bool, error) {
					return waitLocalJobReaped(windows.InvalidHandle, maximum)
				}
			}
			cause := errors.New("controlled failure after native process creation")
			result, err := cleanupLocalFailedStart(job, process, time.Now().UTC(), localTokenProof{}, waitJob, cause)
			if !errors.Is(err, cause) || result.treeReaped == failedQuery || result.exitCode != 125 || result.completedAt.IsZero() ||
				(failedQuery && !errors.Is(err, windows.ERROR_INVALID_HANDLE)) {
				t.Fatalf("failed-start cleanup fabricated or lost Job proof: result=%+v error=%v", result, err)
			}
			if err := child.Wait(); err == nil || child.ProcessState.ExitCode() != 125 {
				t.Fatalf("owned child was not terminated by its Job: state=%v error=%v", child.ProcessState, err)
			}
		})
	}
}

func TestWindowsLocalBackendPreDispatchStdinRejectionClosesPipe(t *testing.T) {
	b, request := localProcessProofFixture(t)
	reader, writer := io.Pipe()
	defer writer.Close()
	result, err := b.RunWithStdin(t.Context(), request, reader)
	if !errors.Is(err, ErrLocalSandboxBoundary) || !result.TreeReaped || result.ExitCode != 125 {
		t.Fatalf("stdin pre-dispatch rejection became uncertain: result=%+v error=%v", result, err)
	}
	if _, err := writer.Write([]byte("closed")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("rejected stdin remained open: %v", err)
	}
}
