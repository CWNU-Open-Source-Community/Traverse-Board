//go:build windows

package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

const commandRuntimeWindowsExitCode = 125

type windowsCommandRuntimeStarter struct{}

func newPlatformCommandRuntimeStarter() commandRuntimeStarter {
	return windowsCommandRuntimeStarter{}
}

func (windowsCommandRuntimeStarter) Name() string    { return "windows-command-runtime-v2" }
func (windowsCommandRuntimeStarter) Available() bool { return true }

type windowsCommandRuntimeProcess struct {
	mu      sync.Mutex
	process windows.Handle
	job     windows.Handle
	stdin   *os.File
	stdout  *os.File
	stderr  *os.File
	pid     int
	closed  bool
}

func (windowsCommandRuntimeStarter) Start(_ context.Context, _ CommandRuntimeScope,
	spec CommandRuntimeResolvedSpec,
) (
	commandRuntimeProcess, error,
) {
	if spec.Spec.Version != CommandRuntimeProtocolVersion ||
		validateCommandRuntimeLaunchDirectory(spec) != nil ||
		commandRuntimeFileDigestMatches(spec.ExecutablePath, spec.ExecutableSHA256) != nil ||
		commandRuntimeExecutableAttributes(spec.ExecutablePath) != nil {
		return nil, ErrCommandRuntimeBoundary
	}
	job, err := newHostJob()
	if err != nil {
		return nil, fmt.Errorf("command runtime Job Object: %w", err)
	}
	stdoutPipe, err := newControlledPipe()
	if err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	stderrPipe, err := newControlledPipe()
	if err != nil {
		stdoutPipe.close()
		_ = windows.CloseHandle(job)
		return nil, err
	}
	var stdinChild windows.Handle
	var stdinParent windows.Handle
	if spec.Spec.StdinPolicy == CommandRuntimeStdinPipe {
		stdinChild, stdinParent, err = newCommandRuntimeInputPipe()
	} else {
		stdinChild, err = openControlledNullInput()
	}
	if err != nil {
		stdoutPipe.close()
		stderrPipe.close()
		_ = windows.CloseHandle(job)
		return nil, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			closeCommandRuntimeWindowsHandles(job, stdinChild, stdinParent)
			stdoutPipe.close()
			stderrPipe.close()
		}
	}()

	attributes, err := windows.NewProcThreadAttributeList(2)
	if err != nil {
		return nil, err
	}
	defer attributes.Delete()
	jobHandles := []windows.Handle{job}
	if err := attributes.Update(procThreadAttributeJobList,
		unsafe.Pointer(&jobHandles[0]), unsafe.Sizeof(jobHandles[0])); err != nil {
		return nil, err
	}
	inherited := []windows.Handle{stdinChild, stdoutPipe.write, stderrPipe.write}
	if err := attributes.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST,
		unsafe.Pointer(&inherited[0]), uintptr(len(inherited))*unsafe.Sizeof(inherited[0])); err != nil {
		return nil, err
	}
	applicationName, err := windows.UTF16PtrFromString(spec.ExecutablePath)
	if err != nil {
		return nil, ErrCommandRuntimeBoundary
	}
	commandLine, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(
		append([]string{spec.ExecutablePath}, spec.CanonicalArgv...)))
	if err != nil {
		return nil, ErrCommandRuntimeBoundary
	}
	directory, err := windows.UTF16PtrFromString(spec.AbsoluteDirectory)
	if err != nil {
		return nil, ErrCommandRuntimeBoundary
	}
	environment, err := commandRuntimeWindowsEnvironment(spec.Environment)
	if err != nil {
		return nil, err
	}
	startup := windows.StartupInfoEx{StartupInfo: windows.StartupInfo{
		Cb:    uint32(unsafe.Sizeof(windows.StartupInfoEx{})),
		Flags: windows.STARTF_USESTDHANDLES, StdInput: stdinChild,
		StdOutput: stdoutPipe.write, StdErr: stderrPipe.write,
	}, ProcThreadAttributeList: attributes.List()}
	processInfo := windows.ProcessInformation{}
	flags := uint32(windows.CREATE_SUSPENDED | windows.CREATE_NO_WINDOW |
		windows.CREATE_UNICODE_ENVIRONMENT | windows.EXTENDED_STARTUPINFO_PRESENT)
	if err := windows.CreateProcess(applicationName, commandLine, nil, nil, true,
		flags, &environment[0], directory, &startup.StartupInfo, &processInfo); err != nil {
		return nil, err
	}
	_ = windows.CloseHandle(stdinChild)
	stdinChild = 0
	_ = windows.CloseHandle(stdoutPipe.write)
	stdoutPipe.write = 0
	_ = windows.CloseHandle(stderrPipe.write)
	stderrPipe.write = 0
	if _, err := windows.ResumeThread(processInfo.Thread); err != nil {
		_ = windows.TerminateJobObject(job, commandRuntimeWindowsExitCode)
		_ = windows.CloseHandle(processInfo.Thread)
		_ = windows.CloseHandle(processInfo.Process)
		return nil, err
	}
	_ = windows.CloseHandle(processInfo.Thread)
	stdoutFile := os.NewFile(uintptr(stdoutPipe.read), "command-runtime-stdout")
	stderrFile := os.NewFile(uintptr(stderrPipe.read), "command-runtime-stderr")
	stdoutPipe.read = 0
	stderrPipe.read = 0
	var stdinFile *os.File
	if stdinParent != 0 {
		stdinFile = os.NewFile(uintptr(stdinParent), "command-runtime-stdin")
		stdinParent = 0
	}
	if stdoutFile == nil || stderrFile == nil ||
		spec.Spec.StdinPolicy == CommandRuntimeStdinPipe && stdinFile == nil {
		_ = windows.TerminateJobObject(job, commandRuntimeWindowsExitCode)
		_ = windows.CloseHandle(processInfo.Process)
		if stdoutFile != nil {
			_ = stdoutFile.Close()
		}
		if stderrFile != nil {
			_ = stderrFile.Close()
		}
		if stdinFile != nil {
			_ = stdinFile.Close()
		}
		return nil, ErrCommandRuntimeUnavailable
	}
	cleanup = false
	return &windowsCommandRuntimeProcess{process: processInfo.Process, job: job,
		stdin: stdinFile, stdout: stdoutFile, stderr: stderrFile,
		pid: int(processInfo.ProcessId)}, nil
}

func (p *windowsCommandRuntimeProcess) Ownership() CommandRuntimeProcessOwnership {
	return CommandRuntimeProcessOwnership{PID: p.pid, ProcessGroup: p.pid,
		JobAssignedAtCreation: true, KillOnClose: true}
}
func (p *windowsCommandRuntimeProcess) Stdout() io.ReadCloser { return p.stdout }
func (p *windowsCommandRuntimeProcess) Stderr() io.ReadCloser { return p.stderr }
func (p *windowsCommandRuntimeProcess) WriteStdin(data []byte) (int, error) {
	p.mu.Lock()
	input := p.stdin
	closed := p.closed
	p.mu.Unlock()
	if closed || input == nil {
		return 0, ErrCommandRuntimeJobClosed
	}
	return input.Write(data)
}
func (p *windowsCommandRuntimeProcess) CloseStdin() error {
	p.mu.Lock()
	input := p.stdin
	p.stdin = nil
	p.mu.Unlock()
	if input == nil {
		return nil
	}
	return input.Close()
}
func (p *windowsCommandRuntimeProcess) Wait() (int, error) {
	p.mu.Lock()
	process, job := p.process, p.job
	closed := p.closed
	p.mu.Unlock()
	if closed || process == 0 || job == 0 {
		return commandRuntimeWindowsExitCode, ErrCommandRuntimeJobClosed
	}
	code, err := waitControlledProcess(context.Background(), process,
		MaxCommandRuntimeTimeout+time.Minute)
	// Descendants are not allowed to outlive the main command. This also
	// closes inherited output handles so collectors cannot hang forever.
	_ = p.terminateJob()
	_, reapErr := waitControlledJobReaped(context.Background(), job, 5*time.Second)
	return code, errors.Join(err, reapErr)
}
func (p *windowsCommandRuntimeProcess) Cancel(time.Duration) error {
	return p.terminateJob()
}
func (p *windowsCommandRuntimeProcess) Kill() error {
	return p.terminateJob()
}
func (p *windowsCommandRuntimeProcess) terminateJob() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.job == 0 {
		return nil
	}
	return windows.TerminateJobObject(p.job, commandRuntimeWindowsExitCode)
}
func (p *windowsCommandRuntimeProcess) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	stdin, stdout, stderr := p.stdin, p.stdout, p.stderr
	p.stdin, p.stdout, p.stderr = nil, nil, nil
	process, job := p.process, p.job
	p.process, p.job = 0, 0
	p.mu.Unlock()
	var result error
	if stdin != nil {
		result = errors.Join(result, stdin.Close())
	}
	if stdout != nil {
		result = errors.Join(result, stdout.Close())
	}
	if stderr != nil {
		result = errors.Join(result, stderr.Close())
	}
	if process != 0 {
		result = errors.Join(result, windows.CloseHandle(process))
	}
	if job != 0 {
		result = errors.Join(result, windows.CloseHandle(job))
	}
	return result
}

func newCommandRuntimeInputPipe() (windows.Handle, windows.Handle, error) {
	security := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), InheritHandle: 1}
	var childRead windows.Handle
	var parentWrite windows.Handle
	if err := windows.CreatePipe(&childRead, &parentWrite, &security, 0); err != nil {
		return 0, 0, err
	}
	if err := windows.SetHandleInformation(parentWrite, windows.HANDLE_FLAG_INHERIT, 0); err != nil {
		_ = windows.CloseHandle(childRead)
		_ = windows.CloseHandle(parentWrite)
		return 0, 0, err
	}
	return childRead, parentWrite, nil
}

func commandRuntimeWindowsEnvironment(values []string) ([]uint16, error) {
	values = append([]string(nil), values...)
	sort.Slice(values, func(left, right int) bool {
		return strings.ToLower(values[left]) < strings.ToLower(values[right])
	})
	block := make([]uint16, 0, 4096)
	for _, value := range values {
		if value == "" || strings.ContainsRune(value, 0) {
			return nil, ErrCommandRuntimeBoundary
		}
		block = append(block, utf16.Encode([]rune(value))...)
		block = append(block, 0)
	}
	return append(block, 0), nil
}

func commandRuntimeFileDigestMatches(path, expected string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return err
	}
	if hex.EncodeToString(digest.Sum(nil)) != expected {
		return ErrCommandRuntimeBoundary
	}
	return nil
}

func closeCommandRuntimeWindowsHandles(handles ...windows.Handle) {
	for _, handle := range handles {
		if handle != 0 {
			_ = windows.CloseHandle(handle)
		}
	}
}

func cleanupCommandRuntimeOrphan(_, _ int) {
	// The process was assigned to a kill-on-close Job Object in the same
	// CreateProcess call. Windows closes the process owner's handles on crash,
	// so a restart must never terminate a recycled PID from durable metadata.
}
