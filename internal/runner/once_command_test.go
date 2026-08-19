package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func testExecutable(t *testing.T) string {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func validSpec(executable, root string) OnceCommandSpec {
	return OnceCommandSpec{
		ProtocolVersion:     OnceCommandProtocolVersion,
		ExecutablePath:      executable,
		Argv:                []string{"one", "two"},
		WorkingDirectory:    root,
		Environment:         nil,
		TimeoutMilliseconds: 30_000,
		Purpose:             "validation fixture",
	}
}

func TestValidateOnceCommandSpecAcceptsStructuredRequest(t *testing.T) {
	root := t.TempDir()
	spec := validSpec(testExecutable(t), root)
	if err := ValidateOnceCommandSpec(spec, root); err != nil {
		t.Fatalf("valid spec rejected: %v", err)
	}
	if OnceCommandSpecFingerprint(spec) == "" || len(OnceCommandSpecFingerprint(spec)) != 64 {
		t.Fatal("spec fingerprint missing")
	}
	if OnceCommandRequestFingerprint("run-1", "ws-1", spec) == OnceCommandRequestFingerprint("run-2", "ws-1", spec) {
		t.Fatal("request fingerprint does not bind the Run")
	}
}

func TestValidateOnceCommandSpecFailsClosed(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		name   string
		mutate func(*OnceCommandSpec)
	}{

		{name: "shell interpreter", mutate: func(spec *OnceCommandSpec) {
			spec.ExecutablePath = filepath.Join(os.TempDir(), "powershell.exe")
			_ = os.WriteFile(spec.ExecutablePath, []byte("x"), 0o600)
		}},
		{name: "workspace escape", mutate: func(spec *OnceCommandSpec) {
			spec.WorkingDirectory = t.TempDir()
		}},
		{name: "executable inside workspace", mutate: func(spec *OnceCommandSpec) {
			spec.ExecutablePath = filepath.Join(root, "tool.exe")
			_ = os.WriteFile(spec.ExecutablePath, []byte("x"), 0o600)
		}},
		{name: "env not allowlisted", mutate: func(spec *OnceCommandSpec) {
			spec.Environment = []string{"SECRET_KEY=1"}
		}},
		{name: "bad utf8 argv", mutate: func(spec *OnceCommandSpec) {
			spec.Argv = []string{string([]byte{0xff, 0xfe})}
		}},
		{name: "missing purpose", mutate: func(spec *OnceCommandSpec) { spec.Purpose = "" }},
		{name: "timeout zero", mutate: func(spec *OnceCommandSpec) { spec.TimeoutMilliseconds = 0 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := validSpec(testExecutable(t), root)
			tc.mutate(&spec)
			if err := ValidateOnceCommandSpec(spec, root); err == nil {
				t.Fatalf("hostile spec accepted: %#v", spec)
			}
		})
	}
}

func TestValidateOnceCommandSpecRejectsSymlinkWorkingDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation may require privileges")
	}
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	spec := validSpec(testExecutable(t), root)
	spec.WorkingDirectory = link
	if err := ValidateOnceCommandSpec(spec, root); err == nil {
		t.Fatal("symlinked working directory escaped the workspace")
	}
}

func TestBoundedOnceBufferHashesTheCompleteObservedStream(t *testing.T) {
	prefix := strings.Repeat("p", MaxOnceOutputBytes)
	left := &boundedOnceBuffer{}
	right := &boundedOnceBuffer{}
	_, _ = left.Write([]byte(prefix + "left-tail"))
	_, _ = right.Write([]byte(prefix + "right-tail"))
	leftCapture, rightCapture := left.Capture(), right.Capture()
	if leftCapture.CapturedPrefixSHA256 != rightCapture.CapturedPrefixSHA256 ||
		leftCapture.ObservedSHA256 == rightCapture.ObservedSHA256 ||
		len(leftCapture.ObservedSHA256) != 64 || len(rightCapture.ObservedSHA256) != 64 ||
		!leftCapture.Truncated || !rightCapture.Truncated {
		t.Fatalf("complete stream digests are not distinct: left=%#v right=%#v",
			leftCapture, rightCapture)
	}
}

func TestOnceExecutorRunsCommandWithBoundedOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix echo fixture")
	}
	echo, err := exec.LookPath("echo")
	if err != nil {
		t.Skip("echo unavailable")
	}
	root := t.TempDir()
	executor, err := NewPlatformOnceExecutor()
	if err != nil || !executor.Available() {
		t.Fatalf("executor unavailable: %v", err)
	}
	spec := validSpec(echo, root)
	spec.Argv = []string{"hello-once"}
	spec.TimeoutMilliseconds = 10_000
	result, err := executor.Execute(context.Background(), OnceCommandRequest{
		Spec: spec, RunID: "run-1", MissionID: "mission-1", WorkspaceID: "ws-1",
		WorkspaceRoot: root, RequestedBy: "operator",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || !strings.Contains(result.Stdout.CapturedPrefix, "hello-once") || result.Stdout.Truncated {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestOnceExecutorRunsNativeBinaryOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows fixture")
	}
	root := t.TempDir()
	executor, err := NewPlatformOnceExecutor()
	if err != nil || !executor.Available() {
		t.Fatalf("executor unavailable: %v", err)
	}
	// The test binary itself is a native .exe outside the workspace; running
	// it with -test.run '^$' exits 0 without executing any test.
	spec := validSpec(testExecutable(t), root)
	spec.Argv = []string{"-test.run", "^$"}
	spec.TimeoutMilliseconds = 30_000
	result, err := executor.Execute(context.Background(), OnceCommandRequest{
		Spec: spec, RunID: "run-1", MissionID: "mission-1", WorkspaceID: "ws-1",
		WorkspaceRoot: root, RequestedBy: "operator",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || !result.TreeReaped {
		t.Fatalf("unexpected windows result: %#v", result)
	}
}

func TestOnceExecutorTimeoutKillsProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix sleep fixture")
	}
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("sleep unavailable")
	}
	root := t.TempDir()
	executor, err := NewPlatformOnceExecutor()
	if err != nil {
		t.Fatal(err)
	}
	spec := validSpec(sleep, root)
	spec.Argv = []string{"30"}
	spec.TimeoutMilliseconds = 200
	start := time.Now()
	result, err := executor.Execute(context.Background(), OnceCommandRequest{
		Spec: spec, RunID: "run-1", MissionID: "m", WorkspaceID: "ws-1",
		WorkspaceRoot: root, RequestedBy: "operator",
	})
	if err == nil {
		t.Fatal("timed-out command reported success")
	}
	if !result.TimedOut || !result.TreeReaped {
		t.Fatalf("timeout flags missing: %#v", result)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("timeout did not terminate the process promptly")
	}
}

func TestOnceProcessHelper(t *testing.T) {
	switch os.Getenv("CYBERAGENT_ONCE_HELPER") {
	case "parent":
		marker := os.Getenv("CYBERAGENT_ONCE_MARKER")
		command := exec.Command(os.Args[0], "-test.run=^TestOnceProcessHelper$")
		command.Env = append([]string(nil),
			"CYBERAGENT_ONCE_HELPER=child",
			"CYBERAGENT_ONCE_MARKER="+marker,
			"SystemRoot="+os.Getenv("SystemRoot"),
		)
		if err := command.Start(); err != nil {
			_ = os.WriteFile(marker+".start-error", []byte(err.Error()), 0o600)
		}
	case "child":
		time.Sleep(1200 * time.Millisecond)
		_ = os.WriteFile(os.Getenv("CYBERAGENT_ONCE_MARKER"), []byte("escaped"), 0o600)
	}
}

func TestPlatformOnceStarterReapsDescendantsAfterSuccessfulParentExit(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "descendant-survived")
	starter := NewPlatformOnceProcessStarter()
	if starter == nil || !starter.Available() {
		t.Skip("platform once starter is unavailable")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	result, err := starter.Start(ctx, OnceStartSpec{
		RequestFingerprint: strings.Repeat("a", 64),
		ExecutablePath:     testExecutable(t),
		Argv:               []string{"-test.run=^TestOnceProcessHelper$"},
		WorkingDirectory:   t.TempDir(),
		Environment: []string{
			"CYBERAGENT_ONCE_HELPER=parent",
			"CYBERAGENT_ONCE_MARKER=" + marker,
			"SystemRoot=" + os.Getenv("SystemRoot"),
		},
	})
	if err != nil || result.ExitCode != 0 || !result.TreeReaped || !result.StdinClosed {
		t.Fatalf("starter result=%#v err=%v", result, err)
	}
	if data, readErr := os.ReadFile(marker + ".start-error"); readErr == nil {
		t.Fatalf("helper could not start descendant: %s", data)
	}
	time.Sleep(1600 * time.Millisecond)
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("background descendant escaped one-shot authority: %v", statErr)
	}
}
