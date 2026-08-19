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
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	createRestrictedDisableMaxPrivilege = 0x00000001
	procThreadAttributeJobList          = 0x0002000D
	controlledJobExitCode               = 125
)

var createRestrictedTokenProc = windows.NewLazySystemDLL(
	"advapi32.dll",
).NewProc("CreateRestrictedToken")

type windowsControlledStarter struct{}

type controlledPipe struct {
	read  windows.Handle
	write windows.Handle
}

type controlledWaitResult struct {
	exitCode int
	err      error
}

type controlledOutputResult struct {
	output         ControlledOutput
	observedSHA256 string
	err            error
}

type jobBasicAccountingInformation struct {
	TotalUserTime             int64
	TotalKernelTime           int64
	ThisPeriodTotalUserTime   int64
	ThisPeriodTotalKernelTime int64
	TotalPageFaultCount       uint32
	TotalProcesses            uint32
	ActiveProcesses           uint32
	TotalTerminatedProcesses  uint32
}

func newPlatformControlledStarter() ControlledProcessStarter {
	return windowsControlledStarter{}
}

func (windowsControlledStarter) Name() string {
	return "windows-controlled-v1"
}

func (windowsControlledStarter) Available() bool {
	return runtime.GOOS == "windows" && createRestrictedTokenProc.Find() == nil
}

func (windowsControlledStarter) Start(ctx context.Context,
	spec ControlledStartSpec,
) (ControlledStartResult, error) {
	if err := spec.Validate(); err != nil {
		return ControlledStartResult{}, err
	}
	if ctx == nil {
		return ControlledStartResult{}, ErrControlledExecutionBoundary
	}
	if err := ctx.Err(); err != nil {
		return ControlledStartResult{}, err
	}
	workspaceRoot, err := openControlledWorkspace(spec)
	if err != nil {
		return ControlledStartResult{}, err
	}
	defer workspaceRoot.Close()
	systemRoot, err := controlledWindowsDirectory()
	if err != nil {
		return ControlledStartResult{}, err
	}
	executablePath, executableFile, err := pinControlledExecutable(spec.ExecutableID)
	if err != nil {
		return ControlledStartResult{}, err
	}
	defer executableFile.Close()

	token, err := newLowIntegrityRestrictedToken()
	if err != nil {
		return ControlledStartResult{}, fmt.Errorf(
			"%w: restricted token creation failed", ErrControlledExecutionPlatform)
	}
	defer token.Close()

	job, err := newControlledJob()
	if err != nil {
		return ControlledStartResult{}, fmt.Errorf(
			"%w: Job Object creation failed", ErrControlledExecutionPlatform)
	}
	defer windows.CloseHandle(job)

	stdout, err := newControlledPipe()
	if err != nil {
		return ControlledStartResult{}, err
	}
	defer stdout.close()
	stderr, err := newControlledPipe()
	if err != nil {
		return ControlledStartResult{}, err
	}
	defer stderr.close()
	stdin, err := openControlledNullInput()
	if err != nil {
		return ControlledStartResult{}, err
	}
	defer windows.CloseHandle(stdin)

	attributes, err := windows.NewProcThreadAttributeList(2)
	if err != nil {
		return ControlledStartResult{}, err
	}
	defer attributes.Delete()
	jobHandles := []windows.Handle{job}
	if err := attributes.Update(procThreadAttributeJobList,
		unsafe.Pointer(&jobHandles[0]), unsafe.Sizeof(jobHandles[0])); err != nil {
		return ControlledStartResult{}, fmt.Errorf(
			"%w: creation-time Job Object binding failed",
			ErrControlledExecutionPlatform)
	}
	inheritedHandles := []windows.Handle{stdin, stdout.write, stderr.write}
	if err := attributes.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST,
		unsafe.Pointer(&inheritedHandles[0]),
		uintptr(len(inheritedHandles))*unsafe.Sizeof(inheritedHandles[0])); err != nil {
		return ControlledStartResult{}, fmt.Errorf(
			"%w: inherited handle boundary failed",
			ErrControlledExecutionPlatform)
	}

	applicationName, err := windows.UTF16PtrFromString(executablePath)
	if err != nil {
		return ControlledStartResult{}, ErrControlledExecutionBoundary
	}
	commandLine := windows.ComposeCommandLine(
		append([]string{executablePath}, spec.Argv...))
	commandLinePointer, err := windows.UTF16PtrFromString(commandLine)
	if err != nil {
		return ControlledStartResult{}, ErrControlledExecutionBoundary
	}
	directoryPointer, err := windows.UTF16PtrFromString(spec.WorkspaceRoot)
	if err != nil {
		return ControlledStartResult{}, ErrControlledExecutionBoundary
	}
	environment := controlledEnvironment(spec, systemRoot)
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
	if err := windows.CreateProcessAsUser(token, applicationName,
		commandLinePointer, nil, nil, true, creationFlags, &environment[0],
		directoryPointer, &startup.StartupInfo, &process); err != nil {
		return ControlledStartResult{}, fmt.Errorf(
			"%w: restricted process creation failed",
			ErrControlledExecutionPlatform)
	}
	defer windows.CloseHandle(process.Process)
	defer windows.CloseHandle(process.Thread)
	if _, err := windows.ResumeThread(process.Thread); err != nil {
		_ = windows.TerminateJobObject(job, controlledJobExitCode)
		_, _ = waitControlledProcess(context.WithoutCancel(ctx), process.Process,
			2*time.Second)
		return ControlledStartResult{}, fmt.Errorf(
			"%w: restricted process resume failed",
			ErrControlledExecutionPlatform)
	}
	_ = windows.CloseHandle(stdout.write)
	stdout.write = 0
	_ = windows.CloseHandle(stderr.write)
	stderr.write = 0

	runCtx, cancel := context.WithTimeout(ctx, spec.Timeout)
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
			process.Process, spec.Timeout+5*time.Second)
		waitChannel <- controlledWaitResult{exitCode: code, err: waitErr}
	}()

	result := ControlledStartResult{
		StartedAt: startedAt, RestrictedToken: true, LowIntegrityToken: true,
		JobAssignedAtCreation: true, KillOnJobClose: true, ActiveProcessLimit: 1,
		ProcessMemoryLimit: MaxControlledProcessMemoryBytes, StdinClosed: true,
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
		_ = windows.TerminateJobObject(job, controlledJobExitCode)
		waitResult = <-waitChannel
	case outputErr := <-outputErrorChannel:
		result.OutputLimitExceeded = errors.Is(outputErr, ErrControlledOutputLimit)
		terminalErr = outputErr
		_ = windows.TerminateJobObject(job, controlledJobExitCode)
		waitResult = <-waitChannel
	}
	result.ExitCode = waitResult.exitCode
	if terminalErr == nil && waitResult.err != nil {
		terminalErr = waitResult.err
	}
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
	reaped, reapErr := waitControlledJobReaped(context.WithoutCancel(ctx), job,
		2*time.Second)
	result.TreeReaped = reaped
	result.CompletedAt = time.Now().UTC()
	if reapErr != nil {
		return result, errors.Join(terminalErr,
			fmt.Errorf("%w: Job Object was not reaped",
				ErrControlledExecutionPlatform))
	}
	if result.OutputLimitExceeded {
		return result, ErrControlledOutputLimit
	}
	return result, terminalErr
}

func openControlledWorkspace(spec ControlledStartSpec) (*os.Root, error) {
	info, err := os.Lstat(spec.WorkspaceRoot)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf(
			"%w: Workspace is unavailable", ErrControlledExecutionBoundary)
	}
	pathPointer, err := windows.UTF16PtrFromString(spec.WorkspaceRoot)
	if err != nil {
		return nil, ErrControlledExecutionBoundary
	}
	attributes, err := windows.GetFileAttributes(pathPointer)
	if err != nil || attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return nil, fmt.Errorf(
			"%w: Workspace root cannot be a reparse point",
			ErrControlledExecutionBoundary)
	}
	root, err := os.OpenRoot(spec.WorkspaceRoot)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: Workspace root cannot be pinned",
			ErrControlledExecutionBoundary)
	}
	if spec.ExecutableID == "windows-powershell" {
		relativePath := decodeControlledRelativePath(spec.Argv[7])
		if relativePath == "" {
			_ = root.Close()
			return nil, ErrControlledExecutionBoundary
		}
		target, statErr := root.Stat(relativePath)
		if statErr != nil || !target.IsDir() {
			_ = root.Close()
			return nil, fmt.Errorf(
				"%w: relative directory is unavailable beneath Workspace",
				ErrControlledExecutionBoundary)
		}
	}
	return root, nil
}

func newControlledPipe() (controlledPipe, error) {
	security := windows.SecurityAttributes{
		Length:        uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		InheritHandle: 1,
	}
	var readHandle windows.Handle
	var writeHandle windows.Handle
	if err := windows.CreatePipe(&readHandle, &writeHandle, &security, 0); err != nil {
		return controlledPipe{}, err
	}
	if err := windows.SetHandleInformation(readHandle,
		windows.HANDLE_FLAG_INHERIT, 0); err != nil {
		_ = windows.CloseHandle(readHandle)
		_ = windows.CloseHandle(writeHandle)
		return controlledPipe{}, err
	}
	return controlledPipe{read: readHandle, write: writeHandle}, nil
}

func (p *controlledPipe) close() {
	if p == nil {
		return
	}
	if p.read != 0 {
		_ = windows.CloseHandle(p.read)
		p.read = 0
	}
	if p.write != 0 {
		_ = windows.CloseHandle(p.write)
		p.write = 0
	}
}

func openControlledNullInput() (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString("NUL")
	if err != nil {
		return 0, err
	}
	security := windows.SecurityAttributes{
		Length:        uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		InheritHandle: 1,
	}
	return windows.CreateFile(name, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, &security,
		windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
}

func pinControlledExecutable(executableID string) (string, *os.File, error) {
	candidates := controlledExecutableCandidates(executableID)
	if len(candidates) == 0 {
		return "", nil, ErrControlledExecutionBoundary
	}
	for _, candidate := range candidates {
		candidate = filepath.Clean(candidate)
		info, err := os.Lstat(candidate)
		if err != nil || !info.Mode().IsRegular() ||
			info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		pathPointer, err := windows.UTF16PtrFromString(candidate)
		if err != nil {
			continue
		}
		attributes, err := windows.GetFileAttributes(pathPointer)
		if err != nil || attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			continue
		}
		handle, err := windows.CreateFile(pathPointer,
			windows.GENERIC_READ|windows.FILE_READ_ATTRIBUTES,
			windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING,
			windows.FILE_ATTRIBUTE_NORMAL, 0)
		if err != nil {
			continue
		}
		file := os.NewFile(uintptr(handle), "controlled-executable")
		if file == nil {
			_ = windows.CloseHandle(handle)
			continue
		}
		if validatePinnedPE(file) {
			return candidate, file, nil
		}
		_ = file.Close()
	}
	return "", nil, fmt.Errorf("%w: approved executable is unavailable",
		ErrControlledExecutionPlatform)
}

func validatePinnedPE(file *os.File) bool {
	if file == nil {
		return false
	}
	info, err := file.Stat()
	if err != nil || info.Size() < 512 || info.Size() > 1024*1024*1024 {
		return false
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return false
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil ||
		len(hex.EncodeToString(digest.Sum(nil))) != 64 {
		return false
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return false
	}
	parsed, err := pe.NewFile(file)
	if err != nil {
		return false
	}
	return parsed.Close() == nil
}

func controlledExecutableCandidates(executableID string) []string {
	programFiles := controlledKnownFolders(
		windows.FOLDERID_ProgramFiles,
		windows.FOLDERID_ProgramFilesX64,
		windows.FOLDERID_ProgramFilesX86,
	)
	switch executableID {
	case "windows-powershell":
		systemRoot, err := controlledWindowsDirectory()
		if err != nil {
			return nil
		}
		return []string{filepath.Join(systemRoot, "System32",
			"WindowsPowerShell", "v1.0", "powershell.exe")}
	case "go":
		candidates := make([]string, 0, len(programFiles))
		for _, root := range programFiles {
			candidates = append(candidates,
				filepath.Join(root, "Go", "bin", "go.exe"))
		}
		return candidates
	case "git":
		roots := append([]string(nil), programFiles...)
		roots = append(roots, controlledKnownFolders(
			windows.FOLDERID_LocalAppData)...)
		candidates := make([]string, 0, len(roots)*2)
		for _, root := range roots {
			candidates = append(candidates,
				filepath.Join(root, "Git", "cmd", "git.exe"),
				filepath.Join(root, "Git", "bin", "git.exe"),
				filepath.Join(root, "Programs", "Git", "cmd", "git.exe"),
			)
		}
		return candidates
	default:
		return nil
	}
}

func controlledWindowsDirectory() (string, error) {
	path, err := windows.GetWindowsDirectory()
	if err != nil {
		return "", fmt.Errorf("%w: Windows directory is unavailable",
			ErrControlledExecutionPlatform)
	}
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) || path == "." ||
		strings.ContainsRune(path, 0) {
		return "", fmt.Errorf("%w: Windows directory is invalid",
			ErrControlledExecutionPlatform)
	}
	return path, nil
}

func controlledKnownFolders(ids ...*windows.KNOWNFOLDERID) []string {
	paths := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		path, err := windows.KnownFolderPath(id, windows.KF_FLAG_DEFAULT)
		if err != nil {
			continue
		}
		path = filepath.Clean(path)
		key := strings.ToLower(path)
		if !filepath.IsAbs(path) || path == "." ||
			strings.ContainsRune(path, 0) {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		paths = append(paths, path)
	}
	return paths
}

func newLowIntegrityRestrictedToken() (windows.Token, error) {
	var current windows.Token
	access := uint32(windows.TOKEN_DUPLICATE | windows.TOKEN_QUERY |
		windows.TOKEN_ASSIGN_PRIMARY | windows.TOKEN_ADJUST_DEFAULT)
	if err := windows.OpenProcessToken(windows.CurrentProcess(), access,
		&current); err != nil {
		return 0, err
	}
	defer current.Close()
	var restricted windows.Token
	success, _, callErr := createRestrictedTokenProc.Call(
		uintptr(current), createRestrictedDisableMaxPrivilege,
		0, 0, 0, 0, 0, 0,
		uintptr(unsafe.Pointer(&restricted)))
	if success == 0 {
		if callErr == nil {
			callErr = syscall.EINVAL
		}
		return 0, callErr
	}
	lowSID, err := windows.CreateWellKnownSid(windows.WinLowLabelSid)
	if err != nil {
		_ = restricted.Close()
		return 0, err
	}
	label := windows.Tokenmandatorylabel{
		Label: windows.SIDAndAttributes{
			Sid: lowSID,
			Attributes: windows.SE_GROUP_INTEGRITY |
				windows.SE_GROUP_INTEGRITY_ENABLED,
		},
	}
	if err := windows.SetTokenInformation(restricted,
		windows.TokenIntegrityLevel, (*byte)(unsafe.Pointer(&label)),
		label.Size()); err != nil {
		_ = restricted.Close()
		return 0, err
	}
	return restricted, nil
}

func newControlledJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags =
		windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE |
			windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS |
			windows.JOB_OBJECT_LIMIT_PROCESS_MEMORY |
			windows.JOB_OBJECT_LIMIT_DIE_ON_UNHANDLED_EXCEPTION
	limits.BasicLimitInformation.ActiveProcessLimit = 1
	limits.ProcessMemoryLimit = MaxControlledProcessMemoryBytes
	if _, err := windows.SetInformationJobObject(job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)),
		uint32(unsafe.Sizeof(limits))); err != nil {
		_ = windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

func controlledEnvironment(spec ControlledStartSpec, systemRoot string) []uint16 {
	values := []string{
		"ComSpec=" + filepath.Join(systemRoot, "System32", "cmd.exe"),
		"GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=core.hooksPath",
		"GIT_CONFIG_KEY_1=core.fsmonitor",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=NUL",
		"GIT_CONFIG_VALUE_0=NUL",
		"GIT_CONFIG_VALUE_1=false",
		"GIT_LFS_SKIP_SMUDGE=1",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_PAGER=cat",
		"GIT_TERMINAL_PROMPT=0",
		"HOME=" + spec.WorkspaceRoot,
		"LANG=C",
		"NO_COLOR=1",
		"PAGER=cat",
		"SystemRoot=" + systemRoot,
		"WINDIR=" + systemRoot,
	}
	sort.Slice(values, func(i, j int) bool {
		return strings.ToLower(values[i]) < strings.ToLower(values[j])
	})
	block := make([]uint16, 0, 1024)
	for _, value := range values {
		block = append(block, utf16.Encode([]rune(value))...)
		block = append(block, 0)
	}
	return append(block, 0)
}

func readControlledOutput(handle windows.Handle,
	channel chan<- controlledOutputResult, errorChannel chan<- error,
) {
	file := os.NewFile(uintptr(handle), "controlled-output")
	if file == nil {
		if handle != 0 {
			_ = windows.CloseHandle(handle)
		}
		channel <- controlledOutputResult{err: ErrControlledExecutionPlatform}
		errorChannel <- ErrControlledExecutionPlatform
		return
	}
	buffer := make([]byte, 32*1024)
	captured := make([]byte, 0, MaxControlledOutputCaptureBytes)
	observedDigest := sha256.New()
	var observed int64
	var readErr error
	for {
		count, err := file.Read(buffer)
		if count > 0 {
			_, _ = observedDigest.Write(buffer[:count])
			remaining := MaxControlledOutputCaptureBytes - len(captured)
			if remaining > count {
				remaining = count
			}
			if remaining > 0 {
				captured = append(captured, buffer[:remaining]...)
			}
			if observed > MaxControlledOutputObservedBytes-int64(count) {
				observed = MaxControlledOutputObservedBytes
				readErr = ErrControlledOutputLimit
				break
			}
			observed += int64(count)
		}
		if err != nil {
			if !errors.Is(err, io.EOF) &&
				!errors.Is(err, windows.ERROR_BROKEN_PIPE) &&
				!errors.Is(err, windows.ERROR_INVALID_HANDLE) {
				readErr = err
			}
			break
		}
	}
	if closeErr := file.Close(); closeErr != nil && readErr == nil {
		readErr = closeErr
	}
	digest := sha256.Sum256(captured)
	channel <- controlledOutputResult{output: ControlledOutput{
		Data: captured, ObservedBytes: observed, CapturedBytes: len(captured),
		CapturedPrefixSHA256: hex.EncodeToString(digest[:]),
		Truncated:            observed > int64(len(captured)),
	}, observedSHA256: hex.EncodeToString(observedDigest.Sum(nil)), err: readErr}
	if readErr != nil {
		select {
		case errorChannel <- readErr:
		default:
		}
	}
}

func receiveControlledOutput(channel <-chan controlledOutputResult) (
	controlledOutputResult, error,
) {
	select {
	case value := <-channel:
		return value, nil
	case <-time.After(2 * time.Second):
		return controlledOutputResult{}, fmt.Errorf(
			"%w: output collector did not finish",
			ErrControlledExecutionPlatform)
	}
}

func waitControlledProcess(ctx context.Context, process windows.Handle,
	maximum time.Duration,
) (int, error) {
	deadline := time.Now().Add(maximum)
	for {
		status, err := windows.WaitForSingleObject(process, 25)
		if err != nil {
			return 0, err
		}
		if status == windows.WAIT_OBJECT_0 {
			var exitCode uint32
			if err := windows.GetExitCodeProcess(process, &exitCode); err != nil {
				return 0, err
			}
			return int(exitCode), nil
		}
		if status != uint32(windows.WAIT_TIMEOUT) {
			return 0, ErrControlledExecutionPlatform
		}
		if ctx != nil {
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			default:
			}
		}
		if time.Now().After(deadline) {
			return 0, context.DeadlineExceeded
		}
	}
}

func waitControlledJobReaped(ctx context.Context, job windows.Handle,
	maximum time.Duration,
) (bool, error) {
	deadline := time.Now().Add(maximum)
	for {
		accounting := jobBasicAccountingInformation{}
		if err := windows.QueryInformationJobObject(job,
			windows.JobObjectBasicAccountingInformation,
			uintptr(unsafe.Pointer(&accounting)),
			uint32(unsafe.Sizeof(accounting)), nil); err != nil {
			return false, err
		}
		if accounting.ActiveProcesses == 0 {
			return true, nil
		}
		if ctx != nil {
			select {
			case <-ctx.Done():
				return false, ctx.Err()
			default:
			}
		}
		if time.Now().After(deadline) {
			return false, context.DeadlineExceeded
		}
		time.Sleep(10 * time.Millisecond)
	}
}
