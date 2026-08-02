//go:build windows

package browserruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	browserJobExitCode         = 125
	procThreadAttributeJobList = 0x0002000D
	browserMediumIntegrityRID  = 0x00002000
	browserHighIntegrityRID    = 0x00003000
)

type windowsBrowserProcessStarter struct{}

type windowsBrowserLaunchAuthority struct {
	token  windows.Token
	asUser bool
}

type windowsBrowserProcess struct {
	mu        sync.Mutex
	job       windows.Handle
	process   windows.Handle
	pid       int
	spec      BrowserStartSpec
	startedAt time.Time
	done      chan struct{}
	exit      BrowserProcessExit
	hasExit   bool
	closed    bool
	timedOut  bool
	cancelled bool
}

func newPlatformBrowserProcessStarter() browserProcessStarter {
	return windowsBrowserProcessStarter{}
}

func (windowsBrowserProcessStarter) Name() string    { return WindowsBrowserProcessAdapterName }
func (windowsBrowserProcessStarter) Available() bool { return true }

func (windowsBrowserProcessStarter) Start(ctx context.Context,
	spec BrowserStartSpec,
) (browserPlatformProcess, error) {
	if ctx == nil || ctx.Err() != nil || spec.ProtocolVersion != BrowserStartSpecProtocolVersion ||
		spec.Fingerprint != browserRuntimeFingerprint(spec) {
		return nil, ErrBrowserRuntimeBoundary
	}
	executable, err := pinBrowserExecutable(spec.ExecutablePath, spec.ExecutableSHA256)
	if err != nil {
		return nil, err
	}
	defer executable.Close()
	if err := validateBrowserEnvironmentDirectories(spec.ProfilePath); err != nil {
		return nil, err
	}
	job, err := newBrowserJob(spec)
	if err != nil {
		return nil, fmt.Errorf("create browser Job Object: %w", err)
	}
	attributes, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	defer attributes.Delete()
	jobHandles := []windows.Handle{job}
	if err := attributes.Update(procThreadAttributeJobList,
		unsafe.Pointer(&jobHandles[0]), unsafe.Sizeof(jobHandles[0])); err != nil {
		windows.CloseHandle(job)
		return nil, fmt.Errorf("bind browser Job Object at process creation: %w", err)
	}
	applicationName, err := windows.UTF16PtrFromString(spec.ExecutablePath)
	if err != nil {
		windows.CloseHandle(job)
		return nil, ErrBrowserRuntimeBoundary
	}
	commandLine := windows.ComposeCommandLine(append([]string{spec.ExecutablePath},
		spec.Arguments...))
	commandLinePointer, err := windows.UTF16PtrFromString(commandLine)
	if err != nil {
		windows.CloseHandle(job)
		return nil, ErrBrowserRuntimeBoundary
	}
	directoryPointer, err := windows.UTF16PtrFromString(filepath.Dir(spec.ExecutablePath))
	if err != nil {
		windows.CloseHandle(job)
		return nil, ErrBrowserRuntimeBoundary
	}
	environment, err := browserEnvironmentBlock(spec.ProfilePath)
	if err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	launchAuthority, err := acquireWindowsBrowserLaunchAuthority()
	if err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	defer launchAuthority.Close()
	startup := windows.StartupInfoEx{
		StartupInfo:             windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfoEx{}))},
		ProcThreadAttributeList: attributes.List(),
	}
	processInfo := windows.ProcessInformation{}
	flags := uint32(windows.CREATE_SUSPENDED | windows.CREATE_NO_WINDOW |
		windows.CREATE_UNICODE_ENVIRONMENT | windows.EXTENDED_STARTUPINFO_PRESENT)
	startedAt := time.Now().UTC()
	if launchAuthority.asUser {
		err = windows.CreateProcessAsUser(launchAuthority.token, applicationName,
			commandLinePointer, nil, nil, false, flags, &environment[0],
			directoryPointer, &startup.StartupInfo, &processInfo)
	} else {
		err = windows.CreateProcess(applicationName, commandLinePointer, nil, nil, false,
			flags, &environment[0], directoryPointer, &startup.StartupInfo, &processInfo)
	}
	if err != nil {
		windows.CloseHandle(job)
		if launchAuthority.asUser {
			return nil, errors.Join(ErrBrowserStandardUserTokenUnavailable,
				fmt.Errorf("start accepted browser process as standard user: %w", err))
		}
		return nil, fmt.Errorf("start accepted browser process: %w", err)
	}
	if err := verifyWindowsBrowserChildAuthority(processInfo.Process); err != nil {
		windows.CloseHandle(processInfo.Thread)
		_ = windows.TerminateJobObject(job, browserJobExitCode)
		_, _ = windows.WaitForSingleObject(processInfo.Process, 5_000)
		windows.CloseHandle(processInfo.Process)
		windows.CloseHandle(job)
		return nil, err
	}
	process := &windowsBrowserProcess{
		job: job, process: processInfo.Process, pid: int(processInfo.ProcessId),
		spec: spec, startedAt: startedAt, done: make(chan struct{}),
	}
	if _, err := windows.ResumeThread(processInfo.Thread); err != nil {
		windows.CloseHandle(processInfo.Thread)
		_ = windows.TerminateJobObject(job, browserJobExitCode)
		_, _ = windows.WaitForSingleObject(processInfo.Process, 5_000)
		windows.CloseHandle(processInfo.Process)
		windows.CloseHandle(job)
		return nil, fmt.Errorf("resume browser process: %w", err)
	}
	windows.CloseHandle(processInfo.Thread)
	go process.wait()
	return process, nil
}

func (authority *windowsBrowserLaunchAuthority) Close() {
	if authority == nil || !authority.asUser || authority.token == 0 {
		return
	}
	_ = authority.token.Close()
	authority.token = 0
}

func acquireWindowsBrowserLaunchAuthority() (windowsBrowserLaunchAuthority, error) {
	current := windows.GetCurrentProcessToken()
	elevated, err := windowsBrowserTokenElevated(current)
	if err != nil {
		return windowsBrowserLaunchAuthority{}, errors.Join(
			ErrBrowserStandardUserTokenUnavailable, err)
	}
	if !elevated {
		return windowsBrowserLaunchAuthority{}, nil
	}
	linked, err := current.GetLinkedToken()
	if err != nil {
		return windowsBrowserLaunchAuthority{}, errors.Join(
			ErrBrowserStandardUserTokenUnavailable, err)
	}
	if err := validateWindowsBrowserStandardUserToken(current, linked); err != nil {
		_ = linked.Close()
		return windowsBrowserLaunchAuthority{}, err
	}
	return windowsBrowserLaunchAuthority{token: linked, asUser: true}, nil
}

func verifyWindowsBrowserChildAuthority(process windows.Handle) error {
	var token windows.Token
	if err := windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token); err != nil {
		return errors.Join(ErrBrowserStandardUserTokenUnavailable, err)
	}
	defer token.Close()
	return validateWindowsBrowserStandardUserToken(windows.GetCurrentProcessToken(), token)
}

func validateWindowsBrowserStandardUserToken(parent windows.Token,
	candidate windows.Token,
) error {
	sameUser, err := windowsBrowserTokensHaveSameUser(parent, candidate)
	if err != nil || !sameUser {
		return errors.Join(ErrBrowserStandardUserTokenUnavailable, err)
	}
	elevated, err := windowsBrowserTokenElevated(candidate)
	if err != nil || elevated {
		return errors.Join(ErrBrowserStandardUserTokenUnavailable, err)
	}
	integrityRID, err := windowsBrowserTokenIntegrityRID(candidate)
	if err != nil || integrityRID < browserMediumIntegrityRID ||
		integrityRID >= browserHighIntegrityRID {
		return errors.Join(ErrBrowserStandardUserTokenUnavailable, err)
	}
	return nil
}

func windowsBrowserTokensHaveSameUser(left windows.Token,
	right windows.Token,
) (bool, error) {
	leftUser, err := left.GetTokenUser()
	if err != nil {
		return false, err
	}
	rightUser, err := right.GetTokenUser()
	if err != nil {
		return false, err
	}
	if leftUser.User.Sid == nil || rightUser.User.Sid == nil {
		return false, ErrBrowserRuntimeBoundary
	}
	return leftUser.User.Sid.Equals(rightUser.User.Sid), nil
}

func windowsBrowserTokenElevated(token windows.Token) (bool, error) {
	var elevated uint32
	var returned uint32
	if err := windows.GetTokenInformation(token, windows.TokenElevation,
		(*byte)(unsafe.Pointer(&elevated)), uint32(unsafe.Sizeof(elevated)),
		&returned); err != nil {
		return false, err
	}
	if returned != uint32(unsafe.Sizeof(elevated)) {
		return false, ErrBrowserRuntimeBoundary
	}
	return elevated != 0, nil
}

func windowsBrowserTokenIntegrityRID(token windows.Token) (uint32, error) {
	var required uint32
	err := windows.GetTokenInformation(token, windows.TokenIntegrityLevel,
		nil, 0, &required)
	if !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) ||
		required < uint32(unsafe.Sizeof(windows.Tokenmandatorylabel{})) {
		return 0, errors.Join(ErrBrowserRuntimeBoundary, err)
	}
	buffer := make([]byte, required)
	if err := windows.GetTokenInformation(token, windows.TokenIntegrityLevel,
		&buffer[0], required, &required); err != nil {
		return 0, err
	}
	label := (*windows.Tokenmandatorylabel)(unsafe.Pointer(&buffer[0]))
	if label.Label.Sid == nil || label.Label.Sid.SubAuthorityCount() == 0 {
		return 0, ErrBrowserRuntimeBoundary
	}
	return label.Label.Sid.SubAuthority(
		uint32(label.Label.Sid.SubAuthorityCount() - 1)), nil
}

func (process *windowsBrowserProcess) PID() int              { return process.pid }
func (process *windowsBrowserProcess) Done() <-chan struct{} { return process.done }

func (process *windowsBrowserProcess) Exit() (BrowserProcessExit, bool) {
	process.mu.Lock()
	defer process.mu.Unlock()
	return process.exit, process.hasExit
}

func (process *windowsBrowserProcess) Stop(ctx context.Context, timedOut bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	process.mu.Lock()
	if process.closed {
		process.mu.Unlock()
		return nil
	}
	job := process.job
	if timedOut {
		process.timedOut = true
	} else {
		process.cancelled = true
	}
	if err := windows.TerminateJobObject(job, browserJobExitCode); err != nil &&
		!errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		process.mu.Unlock()
		return err
	}
	process.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-process.done:
		return nil
	case <-time.After(5 * time.Second):
		return context.DeadlineExceeded
	}
}

func (process *windowsBrowserProcess) wait() {
	_, _ = windows.WaitForSingleObject(process.process, windows.INFINITE)
	var exitCode uint32
	_ = windows.GetExitCodeProcess(process.process, &exitCode)
	_ = windows.TerminateJobObject(process.job, browserJobExitCode)
	treeReaped := waitBrowserJobReaped(process.job, 3*time.Second)
	process.mu.Lock()
	timedOut, cancelled := process.timedOut, process.cancelled
	exit := newBrowserProcessExit(WindowsBrowserProcessAdapterName, process.spec,
		int(exitCode), treeReaped, timedOut, cancelled, process.startedAt, time.Now().UTC())
	windows.CloseHandle(process.process)
	windows.CloseHandle(process.job)
	process.process, process.job, process.closed = 0, 0, true
	process.exit, process.hasExit = exit, true
	process.mu.Unlock()
	close(process.done)
}

func pinBrowserExecutable(path string, expectedSHA256 string) (*os.File, error) {
	if !validSHA256(expectedSHA256) {
		return nil, ErrBrowserRuntimeBoundary
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		!platformProfilePathDirect(path) {
		return nil, errors.New("accepted browser executable became unavailable or indirect")
	}
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, ErrBrowserRuntimeBoundary
	}
	handle, err := windows.CreateFile(pointer,
		windows.GENERIC_READ|windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), "accepted-browser-executable")
	if file == nil {
		windows.CloseHandle(handle)
		return nil, ErrBrowserRuntimeUnavailable
	}
	digest := sha256.New()
	read, hashErr := io.Copy(digest, io.LimitReader(file, MaxBrowserExecutableBytes+1))
	if hashErr != nil || read != info.Size() || hex.EncodeToString(digest.Sum(nil)) != expectedSHA256 {
		file.Close()
		return nil, errors.New("accepted browser executable bytes changed at start")
	}
	return file, nil
}

func newBrowserJob(spec BrowserStartSpec) (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE |
		windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS | windows.JOB_OBJECT_LIMIT_JOB_MEMORY |
		windows.JOB_OBJECT_LIMIT_DIE_ON_UNHANDLED_EXCEPTION
	limits.BasicLimitInformation.ActiveProcessLimit = uint32(spec.ActiveProcessLimit)
	limits.JobMemoryLimit = uintptr(spec.JobMemoryLimitBytes)
	if _, err := windows.SetInformationJobObject(job,
		windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)),
		uint32(unsafe.Sizeof(limits))); err != nil {
		windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

func validateBrowserEnvironmentDirectories(profilePath string) error {
	for _, name := range profileEnvironmentDirectoryNames {
		path := filepath.Join(profilePath, name)
		if !pathWithinRoot(profilePath, path) {
			return ErrBrowserRuntimeBoundary
		}
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || !profilePathHasNoIndirection(path) {
			return errors.New("browser environment directory is indirect")
		}
	}
	return nil
}

func browserEnvironmentBlock(profilePath string) ([]uint16, error) {
	systemRoot := strings.TrimSpace(os.Getenv("SystemRoot"))
	if systemRoot == "" || !filepath.IsAbs(systemRoot) || !profilePathHasNoIndirection(systemRoot) {
		return nil, errors.New("windows system root is unavailable")
	}
	values := []string{
		"APPDATA=" + filepath.Join(profilePath, "RoamingAppData"),
		"HOME=" + profilePath,
		"LOCALAPPDATA=" + filepath.Join(profilePath, "LocalAppData"),
		"SystemRoot=" + systemRoot,
		"TEMP=" + filepath.Join(profilePath, "Temp"),
		"TMP=" + filepath.Join(profilePath, "Temp"),
		"USERPROFILE=" + profilePath,
		"WINDIR=" + systemRoot,
	}
	sort.Slice(values, func(left int, right int) bool {
		return strings.ToLower(values[left]) < strings.ToLower(values[right])
	})
	block := make([]uint16, 0, 1024)
	for _, value := range values {
		block = append(block, utf16.Encode([]rune(value))...)
		block = append(block, 0)
	}
	return append(block, 0), nil
}

func waitBrowserJobReaped(job windows.Handle, maximum time.Duration) bool {
	deadline := time.Now().Add(maximum)
	for time.Now().Before(deadline) {
		accounting := struct {
			TotalUserTime             int64
			TotalKernelTime           int64
			ThisPeriodTotalUserTime   int64
			ThisPeriodTotalKernelTime int64
			TotalPageFaultCount       uint32
			TotalProcesses            uint32
			ActiveProcesses           uint32
			TotalTerminatedProcesses  uint32
		}{}
		if err := windows.QueryInformationJobObject(job,
			windows.JobObjectBasicAccountingInformation,
			uintptr(unsafe.Pointer(&accounting)), uint32(unsafe.Sizeof(accounting)), nil); err != nil {
			return false
		}
		if accounting.ActiveProcesses == 0 {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}
