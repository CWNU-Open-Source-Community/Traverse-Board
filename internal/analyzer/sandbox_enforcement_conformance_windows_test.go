//go:build windows

package analyzer

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func observeAnalyzerSandboxEnforcement(t *testing.T,
	plan AnalyzerLaunchPlan,
) analyzerSandboxEnforcementObservation {
	t.Helper()
	t.Setenv(analyzerSandboxSentinelEnv, "must-not-cross-explicit-environment")
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	jobOpen := true
	t.Cleanup(func() {
		if jobOpen {
			_ = windows.CloseHandle(job)
		}
	})

	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE |
		windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS | windows.JOB_OBJECT_LIMIT_PROCESS_MEMORY |
		windows.JOB_OBJECT_LIMIT_PROCESS_TIME
	limits.BasicLimitInformation.ActiveProcessLimit = uint32(plan.Resources.ProcessCount)
	limits.BasicLimitInformation.PerProcessUserTimeLimit =
		int64(plan.Resources.CPUTimeMilliseconds) * 10_000
	limits.ProcessMemoryLimit = uintptr(plan.Resources.MemoryBytes)
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		t.Fatal(err)
	}
	observed := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	var returned uint32
	if err := windows.QueryInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&observed)), uint32(unsafe.Sizeof(observed)), &returned); err != nil {
		t.Fatal(err)
	}
	requiredFlags := limits.BasicLimitInformation.LimitFlags
	hardLimitsConfigured := observed.BasicLimitInformation.LimitFlags&requiredFlags == requiredFlags
	memoryConfigured := observed.ProcessMemoryLimit == limits.ProcessMemoryLimit
	cpuConfigured := observed.BasicLimitInformation.PerProcessUserTimeLimit ==
		limits.BasicLimitInformation.PerProcessUserTimeLimit

	first := startWindowsSandboxHelper(t)
	if err := assignWindowsSandboxHelper(job, first.command); err != nil {
		first.stop()
		t.Fatal(err)
	}
	ready := first.readReady(t)

	second := startWindowsSandboxHelper(t)
	assignSecondErr := assignWindowsSandboxHelper(job, second.command)
	second.stop()
	processLimitObserved := errors.Is(assignSecondErr, windows.ERROR_NOT_ENOUGH_QUOTA)

	if err := windows.CloseHandle(job); err != nil {
		t.Fatal(err)
	}
	jobOpen = false
	processTreeReaped := first.waitStopped(5 * time.Second)
	return analyzerSandboxEnforcementObservation{
		BackendCandidate:     plan.Sandbox.BackendCandidate,
		ResourcePlanSHA256:   mustAnalyzerConformanceDigest(t, plan.Resources),
		SandboxPlanSHA256:    mustAnalyzerConformanceDigest(t, plan.Sandbox),
		HardLimitsConfigured: hardLimitsConfigured, MemoryLimitConfigured: memoryConfigured,
		CPUTimeLimitConfigured: cpuConfigured, ProcessLimitObserved: processLimitObserved,
		EnvironmentScrubbedObserved: ready == "ready:clean", ProcessTreeReapObserved: processTreeReaped,
		TestConformanceOnly: true,
	}
}

type windowsSandboxHelper struct {
	command *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Reader
}

func startWindowsSandboxHelper(t *testing.T) *windowsSandboxHelper {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestAnalyzerSandboxEnforcementHelper$")
	command.Env = []string{analyzerSandboxHelperModeEnv + "=windows-hold"}
	command.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW,
		HideWindow:    true,
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	return &windowsSandboxHelper{command: command, stdin: stdin, stdout: bufio.NewReader(stdout)}
}

func (helper *windowsSandboxHelper) readReady(t *testing.T) string {
	t.Helper()
	result := make(chan string, 1)
	go func() {
		line, _ := helper.stdout.ReadString('\n')
		result <- line
	}()
	select {
	case line := <-result:
		return stringTrimSpace(line)
	case <-time.After(5 * time.Second):
		helper.stop()
		t.Fatal("Windows sandbox helper did not become ready")
		return ""
	}
}

func (helper *windowsSandboxHelper) stop() {
	_ = helper.stdin.Close()
	if helper.command.Process != nil {
		_ = helper.command.Process.Kill()
	}
	_ = helper.command.Wait()
}

func (helper *windowsSandboxHelper) waitStopped(timeout time.Duration) bool {
	_ = helper.stdin.Close()
	done := make(chan struct{})
	go func() {
		_ = helper.command.Wait()
		close(done)
	}()
	select {
	case <-done:
		alive, err := analyzerWindowsProcessAlive(helper.command.Process.Pid)
		return err == nil && !alive
	case <-time.After(timeout):
		if helper.command.Process != nil {
			_ = helper.command.Process.Kill()
		}
		return false
	}
}

func assignWindowsSandboxHelper(job windows.Handle, command *exec.Cmd) error {
	handle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|
		windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(command.Process.Pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	return windows.AssignProcessToJobObject(job, handle)
}

func runAnalyzerSandboxEnforcementHelper(mode string) error {
	if mode != "windows-hold" {
		return fmt.Errorf("unknown Windows sandbox helper mode %q", mode)
	}
	state := "clean"
	if os.Getenv(analyzerSandboxSentinelEnv) != "" {
		state = "inherited"
	}
	if _, err := fmt.Fprintf(os.Stdout, "ready:%s\n", state); err != nil {
		return err
	}
	_, err := io.Copy(io.Discard, os.Stdin)
	return err
}

func mustAnalyzerConformanceDigest(t *testing.T, value any) string {
	t.Helper()
	digest, ok := canonicalSHA256(value)
	if !ok {
		t.Fatal("cannot digest analyzer conformance value")
	}
	return digest
}

func stringTrimSpace(value string) string {
	for len(value) > 0 && (value[len(value)-1] == '\n' || value[len(value)-1] == '\r') {
		value = value[:len(value)-1]
	}
	return value
}
