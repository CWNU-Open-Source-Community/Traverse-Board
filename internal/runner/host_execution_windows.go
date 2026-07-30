//go:build windows

package runner

import (
	"context"
	"crypto/sha256"
	"debug/pe"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

const hostJobExitCode = 126

type windowsHostStarter struct{}

func newPlatformHostStarter() HostProcessStarter {
	return windowsHostStarter{}
}

func (windowsHostStarter) Name() string {
	return "windows-host-job-v1"
}

func (windowsHostStarter) Available() bool {
	return runtime.GOOS == "windows"
}

func (windowsHostStarter) Start(
	ctx context.Context,
	spec HostStartSpec,
) (HostStartResult, error) {
	if err := spec.Validate(); err != nil {
		return HostStartResult{}, err
	}
	if ctx == nil {
		return HostStartResult{}, ErrHostCommandBoundary
	}
	if err := ctx.Err(); err != nil {
		return HostStartResult{}, err
	}
	workingDirectory, err := openHostWorkingDirectory(
		spec.Command.WorkingDirectory)
	if err != nil {
		return HostStartResult{}, err
	}
	defer workingDirectory.Close()
	executable, err := pinHostExecutable(
		spec.Command.ExecutablePath, spec.Command.ExecutableSHA256)
	if err != nil {
		return HostStartResult{}, err
	}
	defer executable.Close()
	job, err := newHostJob()
	if err != nil {
		return HostStartResult{}, fmt.Errorf(
			"%w: Job Object creation failed", ErrHostCommandPlatform)
	}
	defer windows.CloseHandle(job)

	stdout, err := newControlledPipe()
	if err != nil {
		return HostStartResult{}, err
	}
	defer stdout.close()
	stderr, err := newControlledPipe()
	if err != nil {
		return HostStartResult{}, err
	}
	defer stderr.close()
	stdin, err := openControlledNullInput()
	if err != nil {
		return HostStartResult{}, err
	}
	defer windows.CloseHandle(stdin)

	attributes, err := windows.NewProcThreadAttributeList(2)
	if err != nil {
		return HostStartResult{}, err
	}
	defer attributes.Delete()
	jobHandles := []windows.Handle{job}
	if err := attributes.Update(procThreadAttributeJobList,
		unsafe.Pointer(&jobHandles[0]), unsafe.Sizeof(jobHandles[0])); err != nil {
		return HostStartResult{}, fmt.Errorf(
			"%w: creation-time Job Object binding failed",
			ErrHostCommandPlatform)
	}
	inheritedHandles := []windows.Handle{stdin, stdout.write, stderr.write}
	if err := attributes.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST,
		unsafe.Pointer(&inheritedHandles[0]),
		uintptr(len(inheritedHandles))*unsafe.Sizeof(inheritedHandles[0])); err != nil {
		return HostStartResult{}, fmt.Errorf(
			"%w: inherited handle boundary failed", ErrHostCommandPlatform)
	}

	applicationName, err := windows.UTF16PtrFromString(
		spec.Command.ExecutablePath)
	if err != nil {
		return HostStartResult{}, ErrHostCommandBoundary
	}
	commandLine := windows.ComposeCommandLine(append(
		[]string{spec.Command.ExecutablePath}, spec.Command.Argv...))
	commandLinePointer, err := windows.UTF16PtrFromString(commandLine)
	if err != nil {
		return HostStartResult{}, ErrHostCommandBoundary
	}
	directoryPointer, err := windows.UTF16PtrFromString(
		spec.Command.WorkingDirectory)
	if err != nil {
		return HostStartResult{}, ErrHostCommandBoundary
	}
	environment, err := hostEnvironmentBlock(spec.Environment)
	if err != nil {
		return HostStartResult{}, err
	}
	startup := windows.StartupInfoEx{
		StartupInfo: windows.StartupInfo{
			Cb:       uint32(unsafe.Sizeof(windows.StartupInfoEx{})),
			Flags:    windows.STARTF_USESTDHANDLES,
			StdInput: stdin, StdOutput: stdout.write, StdErr: stderr.write,
		},
		ProcThreadAttributeList: attributes.List(),
	}
	process := windows.ProcessInformation{}
	creationFlags := uint32(windows.CREATE_SUSPENDED |
		windows.CREATE_NO_WINDOW | windows.CREATE_UNICODE_ENVIRONMENT |
		windows.EXTENDED_STARTUPINFO_PRESENT)
	startedAt := time.Now().UTC()
	if err := windows.CreateProcess(applicationName, commandLinePointer,
		nil, nil, true, creationFlags, &environment[0], directoryPointer,
		&startup.StartupInfo, &process); err != nil {
		return HostStartResult{}, fmt.Errorf(
			"%w: host process creation failed", ErrHostCommandPlatform)
	}
	defer windows.CloseHandle(process.Process)
	defer windows.CloseHandle(process.Thread)
	if _, err := windows.ResumeThread(process.Thread); err != nil {
		_ = windows.TerminateJobObject(job, hostJobExitCode)
		_, _ = waitControlledProcess(context.WithoutCancel(ctx),
			process.Process, 2*time.Second)
		return HostStartResult{}, fmt.Errorf(
			"%w: host process resume failed", ErrHostCommandPlatform)
	}
	_ = windows.CloseHandle(stdout.write)
	stdout.write = 0
	_ = windows.CloseHandle(stderr.write)
	stderr.write = 0

	timeout := time.Duration(spec.Command.TimeoutMilliseconds) * time.Millisecond
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	stdoutChannel := make(chan controlledOutputResult, 1)
	stderrChannel := make(chan controlledOutputResult, 1)
	outputErrorChannel := make(chan error, 2)
	stdoutRead := stdout.read
	stderrRead := stderr.read
	stdout.read = 0
	stderr.read = 0
	go readControlledOutput(stdoutRead, stdoutChannel, outputErrorChannel)
	go readControlledOutput(stderrRead, stderrChannel, outputErrorChannel)
	waitChannel := make(chan controlledWaitResult, 1)
	go func() {
		code, waitErr := waitControlledProcess(context.Background(),
			process.Process, timeout+5*time.Second)
		waitChannel <- controlledWaitResult{exitCode: code, err: waitErr}
	}()

	result := HostStartResult{
		StartedAt: startedAt, NonSandboxed: true,
		JobAssignedAtCreation: true, KillOnJobClose: true,
		ActiveProcessLimit: MaxHostActiveProcesses,
		JobMemoryLimit:     MaxHostProcessMemoryBytes,
		StdinClosed:        true, NetworkRequested: true,
		ProductExecutionEnabled: true,
	}
	var waitResult controlledWaitResult
	var terminalErr error
	select {
	case waitResult = <-waitChannel:
		terminalErr = waitResult.err
	case <-runCtx.Done():
		if errors.Is(ctx.Err(), context.Canceled) {
			result.Cancelled = true
			terminalErr = context.Canceled
		} else {
			result.TimedOut = true
			terminalErr = context.DeadlineExceeded
		}
		_ = windows.TerminateJobObject(job, hostJobExitCode)
		waitResult = <-waitChannel
	case outputErr := <-outputErrorChannel:
		result.OutputLimitExceeded =
			errors.Is(outputErr, ErrControlledOutputLimit)
		terminalErr = outputErr
		_ = windows.TerminateJobObject(job, hostJobExitCode)
		waitResult = <-waitChannel
	}
	result.ExitCode = waitResult.exitCode
	if terminalErr == nil && waitResult.err != nil {
		terminalErr = waitResult.err
	}

	// A one-shot host command never leaves descendants running. Closing the
	// job only at function exit is too late because descendants may retain
	// stdout/stderr handles and block output collection.
	_ = windows.TerminateJobObject(job, hostJobExitCode)
	stdoutResult, stdoutReceiveErr := receiveControlledOutput(stdoutChannel)
	stderrResult, stderrReceiveErr := receiveControlledOutput(stderrChannel)
	result.Stdout = stdoutResult.output
	result.Stderr = stderrResult.output
	for _, outputErr := range []error{
		stdoutResult.err, stderrResult.err, stdoutReceiveErr, stderrReceiveErr,
	} {
		if outputErr == nil {
			continue
		}
		if errors.Is(outputErr, ErrControlledOutputLimit) {
			result.OutputLimitExceeded = true
		}
		terminalErr = errors.Join(terminalErr, outputErr)
	}
	reaped, reapErr := waitControlledJobReaped(
		context.WithoutCancel(ctx), job, 2*time.Second)
	result.TreeReaped = reaped
	result.CompletedAt = time.Now().UTC()
	if reapErr != nil {
		return result, errors.Join(terminalErr, fmt.Errorf(
			"%w: Job Object was not reaped", ErrHostCommandPlatform))
	}
	if result.OutputLimitExceeded {
		return result, ErrHostOutputLimit
	}
	return result, terminalErr
}

func openHostWorkingDirectory(path string) (*os.Root, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf(
			"%w: working directory is unavailable", ErrHostCommandBoundary)
	}
	pathPointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, ErrHostCommandBoundary
	}
	attributes, err := windows.GetFileAttributes(pathPointer)
	if err != nil || attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return nil, fmt.Errorf(
			"%w: working directory cannot be a reparse point",
			ErrHostCommandBoundary)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: working directory cannot be pinned", ErrHostCommandBoundary)
	}
	return root, nil
}

func pinHostExecutable(path string, expectedSHA256 string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() ||
		info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf(
			"%w: executable is unavailable", ErrHostCommandBoundary)
	}
	pathPointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, ErrHostCommandBoundary
	}
	attributes, err := windows.GetFileAttributes(pathPointer)
	if err != nil || attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return nil, fmt.Errorf(
			"%w: executable cannot be a reparse point",
			ErrHostCommandBoundary)
	}
	handle, err := windows.CreateFile(pathPointer,
		windows.GENERIC_READ|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: executable cannot be pinned", ErrHostCommandPlatform)
	}
	file := os.NewFile(uintptr(handle), "host-executable")
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, ErrHostCommandPlatform
	}
	if err := validatePinnedHostExecutable(file, expectedSHA256); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func validatePinnedHostExecutable(file *os.File, expectedSHA256 string) error {
	if file == nil || !validSHA256(expectedSHA256) {
		return ErrHostCommandBoundary
	}
	info, err := file.Stat()
	if err != nil || info.Size() < 512 || info.Size() > 1024*1024*1024 {
		return ErrHostCommandBoundary
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil ||
		hex.EncodeToString(digest.Sum(nil)) != expectedSHA256 {
		return fmt.Errorf(
			"%w: executable SHA-256 changed", ErrHostCommandDenied)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	parsed, err := pe.NewFile(file)
	if err != nil {
		return fmt.Errorf(
			"%w: executable is not a valid PE image", ErrHostCommandBoundary)
	}
	return parsed.Close()
}

func newHostJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags =
		windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE |
			windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS |
			windows.JOB_OBJECT_LIMIT_JOB_MEMORY |
			windows.JOB_OBJECT_LIMIT_DIE_ON_UNHANDLED_EXCEPTION
	limits.BasicLimitInformation.ActiveProcessLimit = MaxHostActiveProcesses
	limits.JobMemoryLimit = MaxHostProcessMemoryBytes
	if _, err := windows.SetInformationJobObject(job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)),
		uint32(unsafe.Sizeof(limits))); err != nil {
		_ = windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

func hostEnvironmentBlock(values []string) ([]uint16, error) {
	normalized, _, _, err := normalizeHostEnvironment(values)
	if err != nil {
		return nil, err
	}
	sort.Slice(normalized, func(i, j int) bool {
		return strings.ToLower(normalized[i]) <
			strings.ToLower(normalized[j])
	})
	block := make([]uint16, 0, 2048)
	for _, value := range normalized {
		block = append(block, utf16.Encode([]rune(value))...)
		block = append(block, 0)
	}
	return append(block, 0), nil
}
