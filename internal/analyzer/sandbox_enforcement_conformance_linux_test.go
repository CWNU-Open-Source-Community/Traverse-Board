//go:build linux

package analyzer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

type linuxSandboxHelperObservation struct {
	MemoryLimitConfigured       bool `json:"memory_limit_configured"`
	CPUTimeLimitConfigured      bool `json:"cpu_time_limit_configured"`
	EnvironmentScrubbedObserved bool `json:"environment_scrubbed_observed"`
	NoNewPrivilegesObserved     bool `json:"no_new_privileges_observed"`
	NetworkDenyObserved         bool `json:"network_deny_observed"`
}

func observeAnalyzerSandboxEnforcement(t *testing.T,
	plan AnalyzerLaunchPlan,
) analyzerSandboxEnforcementObservation {
	t.Helper()
	if linuxAuditArchitecture() == 0 {
		t.Skipf("seccomp audit architecture is not defined for %s", runtime.GOARCH)
	}
	t.Setenv(analyzerSandboxSentinelEnv, "must-not-cross-explicit-environment")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0],
		"-test.run=^TestAnalyzerSandboxEnforcementHelper$")
	command.Env = []string{
		analyzerSandboxHelperModeEnv + "=linux-seccomp",
		analyzerSandboxMemoryEnv + "=" + strconv.Itoa(plan.Resources.MemoryBytes),
		analyzerSandboxCPUEnv + "=" + strconv.Itoa(plan.Resources.CPUTimeMilliseconds),
	}
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	output, err := command.Output()
	if err != nil {
		t.Fatalf("Linux sandbox helper failed: %v", err)
	}
	var helper linuxSandboxHelperObservation
	if err := json.Unmarshal(output, &helper); err != nil {
		t.Fatalf("decode Linux sandbox observation: %v output=%q", err, output)
	}
	processTreeReaped := true
	if command.Process != nil {
		err := syscall.Kill(-command.Process.Pid, 0)
		processTreeReaped = errors.Is(err, syscall.ESRCH)
	}
	return analyzerSandboxEnforcementObservation{
		BackendCandidate:            plan.Sandbox.BackendCandidate,
		ResourcePlanSHA256:          mustAnalyzerConformanceDigest(t, plan.Resources),
		SandboxPlanSHA256:           mustAnalyzerConformanceDigest(t, plan.Sandbox),
		HardLimitsConfigured:        helper.MemoryLimitConfigured && helper.CPUTimeLimitConfigured,
		MemoryLimitConfigured:       helper.MemoryLimitConfigured,
		CPUTimeLimitConfigured:      helper.CPUTimeLimitConfigured,
		EnvironmentScrubbedObserved: helper.EnvironmentScrubbedObserved,
		NoNewPrivilegesObserved:     helper.NoNewPrivilegesObserved,
		NetworkDenyObserved:         helper.NetworkDenyObserved,
		ProcessTreeReapObserved:     processTreeReaped, TestConformanceOnly: true,
	}
}

func runAnalyzerSandboxEnforcementHelper(mode string) error {
	if mode != "linux-seccomp" {
		return fmt.Errorf("unknown Linux sandbox helper mode %q", mode)
	}
	memoryBytes, err := strconv.ParseUint(os.Getenv(analyzerSandboxMemoryEnv), 10, 64)
	if err != nil || memoryBytes == 0 {
		return errors.New("invalid Linux sandbox memory limit")
	}
	cpuMilliseconds, err := strconv.ParseUint(os.Getenv(analyzerSandboxCPUEnv), 10, 64)
	if err != nil || cpuMilliseconds == 0 {
		return errors.New("invalid Linux sandbox CPU limit")
	}
	memory := &unix.Rlimit{Cur: memoryBytes, Max: memoryBytes}
	if err := unix.Setrlimit(unix.RLIMIT_DATA, memory); err != nil {
		return fmt.Errorf("set memory rlimit: %w", err)
	}
	var observedMemory unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_DATA, &observedMemory); err != nil {
		return fmt.Errorf("get memory rlimit: %w", err)
	}
	cpuSeconds := (cpuMilliseconds + 999) / 1000
	if cpuSeconds == 0 {
		cpuSeconds = 1
	}
	cpu := &unix.Rlimit{Cur: cpuSeconds, Max: cpuSeconds}
	if err := unix.Setrlimit(unix.RLIMIT_CPU, cpu); err != nil {
		return fmt.Errorf("set CPU rlimit: %w", err)
	}
	var observedCPU unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_CPU, &observedCPU); err != nil {
		return fmt.Errorf("get CPU rlimit: %w", err)
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("set no_new_privs: %w", err)
	}
	noNewPrivileges, err := unix.PrctlRetInt(unix.PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0)
	if err != nil {
		return fmt.Errorf("get no_new_privs: %w", err)
	}
	if err := installAnalyzerNetworkDenySeccomp(); err != nil {
		return err
	}
	fd, socketErr := unix.Socket(unix.AF_INET, unix.SOCK_STREAM, 0)
	if fd >= 0 {
		_ = unix.Close(fd)
	}
	observation := linuxSandboxHelperObservation{
		MemoryLimitConfigured:       observedMemory.Cur == memoryBytes && observedMemory.Max == memoryBytes,
		CPUTimeLimitConfigured:      observedCPU.Cur == cpuSeconds && observedCPU.Max == cpuSeconds,
		EnvironmentScrubbedObserved: os.Getenv(analyzerSandboxSentinelEnv) == "",
		NoNewPrivilegesObserved:     noNewPrivileges == 1,
		NetworkDenyObserved:         errors.Is(socketErr, syscall.EPERM),
	}
	return json.NewEncoder(os.Stdout).Encode(observation)
}

func installAnalyzerNetworkDenySeccomp() error {
	arch := linuxAuditArchitecture()
	if arch == 0 {
		return fmt.Errorf("unsupported seccomp architecture %q", runtime.GOARCH)
	}
	deny := uint32(unix.SECCOMP_RET_ERRNO) | uint32(syscall.EPERM)
	filter := []unix.SockFilter{
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 4},
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, Jt: 1, K: arch},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_KILL_PROCESS},
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 0},
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, Jf: 1, K: uint32(unix.SYS_SOCKET)},
		{Code: unix.BPF_RET | unix.BPF_K, K: deny},
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, Jf: 1, K: uint32(unix.SYS_CONNECT)},
		{Code: unix.BPF_RET | unix.BPF_K, K: deny},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ALLOW},
	}
	program := unix.SockFprog{Len: uint16(len(filter)), Filter: &filter[0]}
	if err := unix.Prctl(unix.PR_SET_SECCOMP, unix.SECCOMP_MODE_FILTER,
		uintptr(unsafe.Pointer(&program)), 0, 0); err != nil {
		return fmt.Errorf("install seccomp network deny filter: %w", err)
	}
	return nil
}

func linuxAuditArchitecture() uint32 {
	switch runtime.GOARCH {
	case "amd64":
		return 0xc000003e
	case "arm64":
		return 0xc00000b7
	case "386":
		return 0x40000003
	case "arm":
		return 0x40000028
	default:
		return 0
	}
}

func mustAnalyzerConformanceDigest(t *testing.T, value any) string {
	t.Helper()
	digest, ok := canonicalSHA256(value)
	if !ok {
		t.Fatal("cannot digest analyzer conformance value")
	}
	return digest
}
