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
	"syscall"
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

var createProcessWithTokenW = windows.NewLazySystemDLL("advapi32.dll").NewProc(
	"CreateProcessWithTokenW")

type windowsBrowserLaunchAuthority struct {
	token  windows.Token
	asUser bool
}

type browserProcessStartStageError struct {
	stage string
	err   error
}

func (err *browserProcessStartStageError) Error() string {
	return fmt.Sprintf("browser process %s: %v", err.stage, err.err)
}

func (err *browserProcessStartStageError) Unwrap() error { return err.err }

func browserProcessStartStageFailure(stage string, err error) error {
	return &browserProcessStartStageError{stage: stage, err: err}
}

func browserProcessStartFailureStage(err error) (string, bool) {
	var stageErr *browserProcessStartStageError
	if !errors.As(err, &stageErr) || stageErr == nil || stageErr.stage == "" {
		return "", false
	}
	return stageErr.stage, true
}

func browserProcessStartFailureReason(err error) string {
	switch {
	case errors.Is(err, windows.ERROR_ACCESS_DENIED):
		return "access_denied"
	case errors.Is(err, windows.ERROR_PRIVILEGE_NOT_HELD):
		return "privilege_not_held"
	case errors.Is(err, windows.ERROR_INVALID_PARAMETER):
		return "invalid_parameter"
	case errors.Is(err, windows.ERROR_INVALID_HANDLE):
		return "invalid_handle"
	case errors.Is(err, windows.ERROR_NOT_SUPPORTED):
		return "not_supported"
	case errors.Is(err, windows.ERROR_FILE_NOT_FOUND):
		return "file_not_found"
	case errors.Is(err, windows.ERROR_PATH_NOT_FOUND):
		return "path_not_found"
	default:
		return ""
	}
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
		return nil, browserProcessStartStageFailure("executable_pin", err)
	}
	defer executable.Close()
	if err := validateBrowserEnvironmentDirectories(spec.ProfilePath); err != nil {
		return nil, browserProcessStartStageFailure("profile_validate", err)
	}
	job, err := newBrowserJob(spec)
	if err != nil {
		return nil, browserProcessStartStageFailure("job_create", err)
	}
	attributes, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		windows.CloseHandle(job)
		return nil, browserProcessStartStageFailure("job_bind", err)
	}
	defer attributes.Delete()
	jobHandles := []windows.Handle{job}
	if err := attributes.Update(procThreadAttributeJobList,
		unsafe.Pointer(&jobHandles[0]), unsafe.Sizeof(jobHandles[0])); err != nil {
		windows.CloseHandle(job)
		return nil, browserProcessStartStageFailure("job_bind", err)
	}
	applicationName, err := windows.UTF16PtrFromString(spec.ExecutablePath)
	if err != nil {
		windows.CloseHandle(job)
		return nil, browserProcessStartStageFailure("command_prepare", ErrBrowserRuntimeBoundary)
	}
	commandLine := windows.ComposeCommandLine(append([]string{spec.ExecutablePath},
		spec.Arguments...))
	commandLineBuffer, err := windows.UTF16FromString(commandLine)
	if err != nil {
		windows.CloseHandle(job)
		return nil, browserProcessStartStageFailure("command_prepare", ErrBrowserRuntimeBoundary)
	}
	directoryPointer, err := windows.UTF16PtrFromString(filepath.Dir(spec.ExecutablePath))
	if err != nil {
		windows.CloseHandle(job)
		return nil, browserProcessStartStageFailure("command_prepare", ErrBrowserRuntimeBoundary)
	}
	launchAuthority, err := acquireWindowsBrowserLaunchAuthority()
	if err != nil {
		windows.CloseHandle(job)
		return nil, browserProcessStartStageFailure("authority_acquire", err)
	}
	defer launchAuthority.Close()
	environmentToken := windows.GetCurrentProcessToken()
	if launchAuthority.asUser {
		environmentToken = launchAuthority.token
	}
	environment, err := browserEnvironmentBlock(spec.ProfilePath, environmentToken)
	if err != nil {
		windows.CloseHandle(job)
		return nil, browserProcessStartStageFailure("environment_prepare", err)
	}
	startup := windows.StartupInfoEx{
		StartupInfo:             windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfoEx{}))},
		ProcThreadAttributeList: attributes.List(),
	}
	processInfo := windows.ProcessInformation{}
	flags := uint32(windows.CREATE_SUSPENDED | windows.CREATE_NO_WINDOW |
		windows.CREATE_UNICODE_ENVIRONMENT | windows.EXTENDED_STARTUPINFO_PRESENT)
	startedAt := time.Now().UTC()
	processCreateStage := "process_create"
	jobBoundAtCreation := true
	if launchAuthority.asUser {
		err = windows.CreateProcessAsUser(launchAuthority.token, applicationName,
			&commandLineBuffer[0], nil, nil, false, flags, &environment[0],
			directoryPointer, &startup.StartupInfo, &processInfo)
		if errors.Is(err, windows.ERROR_PRIVILEGE_NOT_HELD) ||
			errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			processCreateStage = "process_create_with_token"
			fallbackCommandLine, commandErr := windows.UTF16FromString(commandLine)
			if commandErr != nil {
				windows.CloseHandle(job)
				return nil, browserProcessStartStageFailure(
					"command_prepare", ErrBrowserRuntimeBoundary)
			}
			fallbackStartup := windows.StartupInfo{
				Cb: uint32(unsafe.Sizeof(windows.StartupInfo{})),
			}
			fallbackFlags := flags &^ uint32(windows.EXTENDED_STARTUPINFO_PRESENT)
			processInfo = windows.ProcessInformation{}
			jobBoundAtCreation = false
			err = createWindowsBrowserProcessWithToken(launchAuthority.token,
				applicationName, &fallbackCommandLine[0], fallbackFlags, &environment[0],
				directoryPointer, &fallbackStartup, &processInfo)
		}
	} else {
		err = windows.CreateProcess(applicationName, &commandLineBuffer[0], nil, nil, false,
			flags, &environment[0], directoryPointer, &startup.StartupInfo, &processInfo)
	}
	if err != nil {
		windows.CloseHandle(job)
		stageErr := browserProcessStartStageFailure(processCreateStage, err)
		if launchAuthority.asUser {
			return nil, errors.Join(ErrBrowserStandardUserTokenUnavailable,
				stageErr)
		}
		return nil, stageErr
	}
	if !jobBoundAtCreation {
		if err := windows.AssignProcessToJobObject(job, processInfo.Process); err != nil {
			windows.CloseHandle(processInfo.Thread)
			_ = windows.TerminateProcess(processInfo.Process, browserJobExitCode)
			_, _ = windows.WaitForSingleObject(processInfo.Process, 5_000)
			windows.CloseHandle(processInfo.Process)
			windows.CloseHandle(job)
			return nil, browserProcessStartStageFailure("job_bind_after_token", err)
		}
	}
	if err := verifyWindowsBrowserChildAuthority(processInfo.Process); err != nil {
		windows.CloseHandle(processInfo.Thread)
		_ = windows.TerminateJobObject(job, browserJobExitCode)
		_, _ = windows.WaitForSingleObject(processInfo.Process, 5_000)
		windows.CloseHandle(processInfo.Process)
		windows.CloseHandle(job)
		return nil, browserProcessStartStageFailure("child_authority", err)
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
		return nil, browserProcessStartStageFailure("process_resume", err)
	}
	windows.CloseHandle(processInfo.Thread)
	go process.wait()
	return process, nil
}

func createWindowsBrowserProcessWithToken(token windows.Token, applicationName,
	commandLine *uint16, flags uint32, environment *uint16, directory *uint16,
	startup *windows.StartupInfo, processInfo *windows.ProcessInformation,
) error {
	result, _, callErr := createProcessWithTokenW.Call(
		uintptr(token),
		0,
		uintptr(unsafe.Pointer(applicationName)),
		uintptr(unsafe.Pointer(commandLine)),
		uintptr(flags),
		uintptr(unsafe.Pointer(environment)),
		uintptr(unsafe.Pointer(directory)),
		uintptr(unsafe.Pointer(startup)),
		uintptr(unsafe.Pointer(processInfo)),
	)
	if result != 0 {
		return nil
	}
	if callErr != nil && callErr != syscall.Errno(0) {
		return callErr
	}
	return windows.ERROR_INVALID_FUNCTION
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
	primary, err := acquireWindowsInteractiveShellPrimaryToken(current)
	if err != nil {
		return windowsBrowserLaunchAuthority{}, errors.Join(
			ErrBrowserStandardUserTokenUnavailable, err)
	}
	return windowsBrowserLaunchAuthority{token: primary, asUser: true}, nil
}

func acquireWindowsInteractiveShellPrimaryToken(parent windows.Token) (windows.Token, error) {
	shellWindow := windows.GetShellWindow()
	if shellWindow == 0 || !windows.IsWindow(shellWindow) {
		return 0, errors.New("windows interactive shell window is unavailable")
	}
	var shellPID uint32
	if _, err := windows.GetWindowThreadProcessId(shellWindow, &shellPID); err != nil || shellPID == 0 {
		return 0, errors.Join(errors.New("windows interactive shell process is unavailable"), err)
	}
	var currentSession uint32
	var shellSession uint32
	if err := windows.ProcessIdToSessionId(uint32(os.Getpid()), &currentSession); err != nil {
		return 0, err
	}
	if err := windows.ProcessIdToSessionId(shellPID, &shellSession); err != nil {
		return 0, err
	}
	if shellSession != currentSession {
		return 0, errors.New("windows interactive shell belongs to another session")
	}
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION,
		false, shellPID)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(process)
	imagePath, err := windowsProcessImagePath(process)
	if err != nil {
		return 0, err
	}
	windowsDirectory, err := windows.GetWindowsDirectory()
	if err != nil {
		return 0, err
	}
	expectedPath := filepath.Clean(filepath.Join(windowsDirectory, "explorer.exe"))
	if !strings.EqualFold(imagePath, expectedPath) ||
		!profilePathHasNoIndirection(expectedPath) {
		return 0, errors.New("windows interactive shell executable is not trusted")
	}
	var shellToken windows.Token
	if err := windows.OpenProcessToken(process,
		windows.TOKEN_QUERY|windows.TOKEN_DUPLICATE, &shellToken); err != nil {
		return 0, err
	}
	defer shellToken.Close()
	if err := validateWindowsBrowserStandardUserPrimaryToken(parent, shellToken); err != nil {
		return 0, err
	}
	var primary windows.Token
	// The duplicated primary token needs full access, not just the documented
	// TOKEN_QUERY|TOKEN_DUPLICATE|TOKEN_ASSIGN_PRIMARY minimum: CreateProcessWithTokenW
	// (the SeAssignPrimaryToken-free fallback used to de-elevate the browser)
	// rejects a restricted-access token with ERROR_ACCESS_DENIED. The token is
	// still validated to be the trusted interactive shell's same-user, same-session,
	// non-elevated, medium-integrity primary token before it is used anywhere.
	desiredAccess := uint32(windows.TOKEN_ALL_ACCESS)
	if err := windows.DuplicateTokenEx(shellToken, desiredAccess, nil,
		windows.SecurityImpersonation, windows.TokenPrimary, &primary); err != nil {
		return 0, err
	}
	if err := validateWindowsBrowserStandardUserPrimaryToken(parent, primary); err != nil {
		_ = primary.Close()
		return 0, err
	}
	return primary, nil
}

func windowsProcessImagePath(process windows.Handle) (string, error) {
	buffer := make([]uint16, 32*1024)
	size := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(process, 0, &buffer[0], &size); err != nil {
		return "", err
	}
	if size == 0 || size >= uint32(len(buffer)) {
		return "", ErrBrowserRuntimeBoundary
	}
	path := filepath.Clean(windows.UTF16ToString(buffer[:size]))
	if !filepath.IsAbs(path) || path == "." || strings.ContainsRune(path, 0) {
		return "", ErrBrowserRuntimeBoundary
	}
	return path, nil
}

func verifyWindowsBrowserChildAuthority(process windows.Handle) error {
	var token windows.Token
	if err := windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token); err != nil {
		return errors.Join(ErrBrowserStandardUserTokenUnavailable, err)
	}
	defer token.Close()
	return validateWindowsBrowserStandardUserPrimaryToken(
		windows.GetCurrentProcessToken(), token)
}

func validateWindowsBrowserStandardUserToken(parent windows.Token,
	candidate windows.Token,
) error {
	sameUser, err := windowsBrowserTokensHaveSameUser(parent, candidate)
	if err != nil || !sameUser {
		return errors.Join(ErrBrowserStandardUserTokenUnavailable, err)
	}
	sameSession, err := windowsBrowserTokensHaveSameSession(parent, candidate)
	if err != nil || !sameSession {
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

func validateWindowsBrowserStandardUserPrimaryToken(parent windows.Token,
	candidate windows.Token,
) error {
	if err := validateWindowsBrowserStandardUserToken(parent, candidate); err != nil {
		return err
	}
	primary, err := windowsBrowserTokenIsPrimary(candidate)
	if err != nil || !primary {
		return errors.Join(ErrBrowserStandardUserTokenUnavailable, err)
	}
	return nil
}

func windowsBrowserTokenIsPrimary(token windows.Token) (bool, error) {
	var tokenType uint32
	var outLength uint32
	if err := windows.GetTokenInformation(token, windows.TokenType,
		(*byte)(unsafe.Pointer(&tokenType)), uint32(unsafe.Sizeof(tokenType)),
		&outLength); err != nil {
		return false, err
	}
	if outLength != uint32(unsafe.Sizeof(tokenType)) {
		return false, ErrBrowserRuntimeBoundary
	}
	return tokenType == windows.TokenPrimary, nil
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

func windowsBrowserTokensHaveSameSession(left windows.Token,
	right windows.Token,
) (bool, error) {
	leftSession, err := windowsBrowserTokenSessionID(left)
	if err != nil {
		return false, err
	}
	rightSession, err := windowsBrowserTokenSessionID(right)
	if err != nil {
		return false, err
	}
	return leftSession == rightSession, nil
}

func windowsBrowserTokenSessionID(token windows.Token) (uint32, error) {
	var sessionID uint32
	var returned uint32
	if err := windows.GetTokenInformation(token, windows.TokenSessionId,
		(*byte)(unsafe.Pointer(&sessionID)), uint32(unsafe.Sizeof(sessionID)),
		&returned); err != nil {
		return 0, err
	}
	if returned != uint32(unsafe.Sizeof(sessionID)) {
		return 0, ErrBrowserRuntimeBoundary
	}
	return sessionID, nil
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

func browserEnvironmentBlock(profilePath string, token windows.Token) ([]uint16, error) {
	systemRoot := strings.TrimSpace(os.Getenv("SystemRoot"))
	if systemRoot == "" || !filepath.IsAbs(systemRoot) || !profilePathHasNoIndirection(systemRoot) {
		return nil, errors.New("windows system root is unavailable")
	}
	// Windows known-folder resolution depends on the environment associated
	// with the launch token. Replacing USERPROFILE/APPDATA/LOCALAPPDATA with
	// arbitrary directories makes PathService treat the user-data directory as
	// unknown, which Chromium deliberately handles as the default profile and
	// therefore refuses for remote debugging. Start from CreateEnvironmentBlock
	// without inheriting the caller, then retain only non-secret structural
	// variables. The browser data and temporary directories remain disposable.
	tokenEnvironment, err := token.Environ(false)
	if err != nil {
		return nil, err
	}
	values, err := restrictedBrowserEnvironmentValues(profilePath, systemRoot,
		tokenEnvironment)
	if err != nil {
		return nil, err
	}
	block := make([]uint16, 0, 2048)
	for _, value := range values {
		block = append(block, utf16.Encode([]rune(value))...)
		block = append(block, 0)
	}
	return append(block, 0), nil
}

func restrictedBrowserEnvironmentValues(profilePath string, systemRoot string,
	tokenEnvironment []string,
) ([]string, error) {
	valuesByName := make(map[string]string)
	for _, entry := range tokenEnvironment {
		separator := strings.IndexByte(entry, '=')
		if separator <= 0 {
			continue
		}
		name := strings.ToUpper(entry[:separator])
		if !browserStructuralEnvironmentNameAllowed(name) {
			continue
		}
		value := entry[separator+1:]
		if value == "" || strings.ContainsRune(value, 0) {
			continue
		}
		if _, duplicate := valuesByName[name]; duplicate {
			return nil, errors.New("windows user environment contains a duplicate structural variable")
		}
		valuesByName[name] = value
	}
	for _, name := range []string{"APPDATA", "LOCALAPPDATA", "USERPROFILE"} {
		value := valuesByName[name]
		if value == "" || !filepath.IsAbs(value) {
			return nil, errors.New("windows user environment is missing a known-folder variable")
		}
	}
	if systemRoot == "" || !filepath.IsAbs(systemRoot) || strings.ContainsRune(systemRoot, 0) ||
		profilePath == "" || !filepath.IsAbs(profilePath) || strings.ContainsRune(profilePath, 0) {
		return nil, ErrBrowserRuntimeBoundary
	}
	system32 := filepath.Join(systemRoot, "System32")
	temporaryDirectory := filepath.Join(profilePath, "Temp")
	valuesByName["COMSPEC"] = filepath.Join(system32, "cmd.exe")
	valuesByName["HOME"] = profilePath
	valuesByName["PATH"] = system32 + ";" + systemRoot
	valuesByName["PATHEXT"] = ".COM;.EXE;.BAT;.CMD"
	valuesByName["SYSTEMROOT"] = systemRoot
	valuesByName["TEMP"] = temporaryDirectory
	valuesByName["TMP"] = temporaryDirectory
	valuesByName["WINDIR"] = systemRoot
	values := make([]string, 0, len(valuesByName))
	for name, value := range valuesByName {
		values = append(values, name+"="+value)
	}
	sort.Slice(values, func(left int, right int) bool {
		return strings.ToLower(values[left]) < strings.ToLower(values[right])
	})
	return values, nil
}

func browserStructuralEnvironmentNameAllowed(name string) bool {
	switch name {
	case "ALLUSERSPROFILE", "APPDATA", "COMMONPROGRAMFILES",
		"COMMONPROGRAMFILES(X86)", "COMMONPROGRAMW6432", "COMPUTERNAME",
		"DRIVERDATA", "HOMEDRIVE", "HOMEPATH", "LOCALAPPDATA",
		"NUMBER_OF_PROCESSORS", "OS", "PROCESSOR_ARCHITECTURE",
		"PROCESSOR_IDENTIFIER", "PROCESSOR_LEVEL", "PROCESSOR_REVISION",
		"PROGRAMDATA", "PROGRAMFILES", "PROGRAMFILES(X86)", "PROGRAMW6432",
		"PUBLIC", "SYSTEMDRIVE", "USERDOMAIN", "USERDOMAIN_ROAMINGPROFILE",
		"USERNAME", "USERPROFILE":
		return true
	default:
		return false
	}
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
