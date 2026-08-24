//go:build windows

package sandbox

import (
	"context"
	"crypto/sha256"
	"debug/pe"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
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
	localCreateRestrictedDisableMaxPrivilege             = 0x00000001
	localProcThreadAttributeSecurityCapabilities         = 0x00020009
	localProcThreadAttributeJobList                      = 0x0002000D
	localProcThreadAttributeAllApplicationPackagesPolicy = 0x0002000F
	localProcessCreationAllApplicationPackagesOptOut     = 0x00000001
	localTokenIsAppContainer                             = 29
	localTokenCapabilities                               = 30
	localTokenAppContainerSID                            = 31
	localSecurityMandatoryLowRID                         = 0x00001000
	localJobExitCode                                     = 125
	localOutputCaptureMaximum                            = 1024 * 1024
	localJobCPUEnable                                    = 0x1
	localJobCPUHardCap                                   = 0x4
)

var localCreateRestrictedTokenProc = windows.NewLazySystemDLL("advapi32.dll").NewProc(
	"CreateRestrictedToken")

type localSecurityCapabilities struct {
	AppContainerSID *windows.SID
	Capabilities    *windows.SIDAndAttributes
	CapabilityCount uint32
	Reserved        uint32
}

type localProcessSpec struct {
	profile      localAppContainerProfile
	executable   string
	arguments    []string
	workingDir   string
	environment  []uint16
	resources    ResourceLimits
	timeout      time.Duration
	captureOut   bool
	captureErr   bool
	stdin        io.ReadCloser
	writeMaximum int64
}

type localTokenProof struct {
	appContainer            bool
	lessPrivileged          bool
	restricted              bool
	lowIntegrity            bool
	zeroNetworkCapabilities bool
	matchingProfileSID      bool
	matchingCapabilitySIDs  bool
}

type localProcessResult struct {
	exitCode            int
	stdout              LocalCapturedOutput
	stderr              LocalCapturedOutput
	startedAt           time.Time
	completedAt         time.Time
	timedOut            bool
	cancelled           bool
	outputLimitExceeded bool
	writeLimitExceeded  bool
	treeReaped          bool
	proof               localTokenProof
}

type localPipe struct {
	read  windows.Handle
	write windows.Handle
}

type localOutputBudget struct {
	mu       sync.Mutex
	maximum  int64
	observed int64
	exceeded bool
	signal   chan struct{}
}

type localOutputStream struct {
	data     []byte
	observed int64
	done     chan struct{}
	err      error
}

type localJobBasicAccounting struct {
	TotalUserTime             int64
	TotalKernelTime           int64
	ThisPeriodTotalUserTime   int64
	ThisPeriodTotalKernelTime int64
	TotalPageFaultCount       uint32
	TotalProcesses            uint32
	ActiveProcesses           uint32
	TotalTerminatedProcesses  uint32
}

type localJobBasicAndIOAccounting struct {
	Basic localJobBasicAccounting
	IO    windows.IO_COUNTERS
}

type localJobCPURateControl struct {
	ControlFlags uint32
	CPURate      uint32
}

func runLocalProcess(ctx context.Context, spec localProcessSpec) (localProcessResult, error) {
	if ctx == nil || spec.profile.sid == nil || !spec.profile.sid.IsValid() ||
		spec.profile.filesystemCapabilitySID == nil ||
		!spec.profile.filesystemCapabilitySID.IsValid() ||
		spec.profile.registryReadCapabilitySID == nil ||
		!spec.profile.registryReadCapabilitySID.IsValid() ||
		!validLocalProfileName(spec.profile.name) || spec.timeout <= 0 ||
		spec.writeMaximum < 1 || spec.resources.MaxOutputBytes < 1 {
		return localProcessResult{}, ErrLocalSandboxBoundary
	}
	if err := ctx.Err(); err != nil {
		return localProcessResult{}, err
	}
	executable, executableHandle, err := pinLocalExecutable(spec.executable)
	if err != nil {
		return localProcessResult{}, err
	}
	defer windows.CloseHandle(executableHandle)
	restrictedToken, err := newLocalRestrictedToken()
	if err != nil {
		return localProcessResult{}, fmt.Errorf("create restricted Local Sandbox token: %w", err)
	}
	defer restrictedToken.Close()

	job, err := newLocalJob(spec.resources)
	if err != nil {
		return localProcessResult{}, fmt.Errorf("create Local Sandbox Job: %w", err)
	}
	defer windows.CloseHandle(job)
	stdout, err := newLocalPipe()
	if err != nil {
		return localProcessResult{}, err
	}
	defer stdout.close()
	stderr, err := newLocalPipe()
	if err != nil {
		return localProcessResult{}, err
	}
	defer stderr.close()
	var stdin windows.Handle
	var stdinWriter *os.File
	if spec.stdin == nil {
		stdin, err = openLocalNullInput()
	} else {
		stdin, stdinWriter, err = newLocalInputPipe()
	}
	if err != nil {
		return localProcessResult{}, err
	}
	defer func() {
		if stdin != 0 {
			_ = windows.CloseHandle(stdin)
		}
		if stdinWriter != nil {
			_ = stdinWriter.Close()
		}
	}()

	attributes, err := windows.NewProcThreadAttributeList(4)
	if err != nil {
		return localProcessResult{}, err
	}
	defer attributes.Delete()
	capabilities := localProfileCapabilities(spec.profile)
	securityCapabilities := localSecurityCapabilities{AppContainerSID: spec.profile.sid,
		Capabilities: &capabilities[0], CapabilityCount: uint32(len(capabilities))}
	if err := attributes.Update(localProcThreadAttributeSecurityCapabilities,
		unsafe.Pointer(&securityCapabilities), unsafe.Sizeof(securityCapabilities)); err != nil {
		return localProcessResult{}, fmt.Errorf("bind AppContainer at creation: %w", err)
	}
	allApplicationPackagesPolicy := uint32(localProcessCreationAllApplicationPackagesOptOut)
	if err := attributes.Update(localProcThreadAttributeAllApplicationPackagesPolicy,
		unsafe.Pointer(&allApplicationPackagesPolicy), unsafe.Sizeof(allApplicationPackagesPolicy)); err != nil {
		return localProcessResult{}, fmt.Errorf("enforce LPAC policy at creation: %w", err)
	}
	jobHandles := []windows.Handle{job}
	if err := attributes.Update(localProcThreadAttributeJobList,
		unsafe.Pointer(&jobHandles[0]), unsafe.Sizeof(jobHandles[0])); err != nil {
		return localProcessResult{}, fmt.Errorf("bind Job at creation: %w", err)
	}
	inherited := []windows.Handle{stdin, stdout.write, stderr.write}
	if err := attributes.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST,
		unsafe.Pointer(&inherited[0]), uintptr(len(inherited))*unsafe.Sizeof(inherited[0])); err != nil {
		return localProcessResult{}, fmt.Errorf("bind inherited handle list: %w", err)
	}

	applicationName, err := windows.UTF16PtrFromString(executable)
	if err != nil {
		return localProcessResult{}, ErrLocalSandboxBoundary
	}
	commandLine, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(
		append([]string{executable}, spec.arguments...)))
	if err != nil {
		return localProcessResult{}, ErrLocalSandboxBoundary
	}
	workingDirectory, err := windows.UTF16PtrFromString(spec.workingDir)
	if err != nil || len(spec.environment) < 2 {
		return localProcessResult{}, ErrLocalSandboxBoundary
	}
	startup := windows.StartupInfoEx{StartupInfo: windows.StartupInfo{
		Cb:    uint32(unsafe.Sizeof(windows.StartupInfoEx{})),
		Flags: windows.STARTF_USESTDHANDLES, StdInput: stdin,
		StdOutput: stdout.write, StdErr: stderr.write,
	}, ProcThreadAttributeList: attributes.List()}
	process := windows.ProcessInformation{}
	creationFlags := uint32(windows.CREATE_SUSPENDED | windows.CREATE_NO_WINDOW |
		windows.CREATE_UNICODE_ENVIRONMENT | windows.EXTENDED_STARTUPINFO_PRESENT)
	startedAt := time.Now().UTC()
	if err := windows.CreateProcessAsUser(restrictedToken, applicationName, commandLine, nil, nil, true,
		creationFlags, &spec.environment[0], workingDirectory,
		&startup.StartupInfo, &process); err != nil {
		return localProcessResult{}, fmt.Errorf("create AppContainer process: %w", err)
	}
	defer windows.CloseHandle(process.Process)
	defer windows.CloseHandle(process.Thread)
	runtime.KeepAlive(securityCapabilities)
	runtime.KeepAlive(capabilities)
	runtime.KeepAlive(allApplicationPackagesPolicy)

	proof, err := verifyLocalProcessToken(process.Process, spec.profile)
	if err != nil || !proof.appContainer || !proof.lessPrivileged || !proof.lowIntegrity ||
		!proof.zeroNetworkCapabilities || !proof.matchingProfileSID ||
		!proof.matchingCapabilitySIDs {
		_ = windows.TerminateJobObject(job, localJobExitCode)
		_, _ = waitLocalProcess(process.Process, 2*time.Second)
		return localProcessResult{}, errors.Join(err, fmt.Errorf(
			"local sandbox process token proof failed (appcontainer=%t lpac=%t restricted=%t low_integrity=%t zero_network_capabilities=%t profile_sid=%t filesystem_capability_sid=%t)",
			proof.appContainer, proof.lessPrivileged, proof.restricted, proof.lowIntegrity,
			proof.zeroNetworkCapabilities, proof.matchingProfileSID,
			proof.matchingCapabilitySIDs))
	}
	if _, err := windows.ResumeThread(process.Thread); err != nil {
		_ = windows.TerminateJobObject(job, localJobExitCode)
		_, _ = waitLocalProcess(process.Process, 2*time.Second)
		return localProcessResult{}, fmt.Errorf("resume AppContainer process: %w", err)
	}
	_ = windows.CloseHandle(stdin)
	stdin = 0
	_ = windows.CloseHandle(stdout.write)
	stdout.write = 0
	_ = windows.CloseHandle(stderr.write)
	stderr.write = 0

	budget := &localOutputBudget{maximum: spec.resources.MaxOutputBytes,
		signal: make(chan struct{}, 1)}
	outStream := startLocalOutput(stdout.read, budget, spec.captureOut)
	stdout.read = 0
	errStream := startLocalOutput(stderr.read, budget, spec.captureErr)
	stderr.read = 0
	var stdinDone chan error
	if stdinWriter != nil {
		stdinDone = make(chan error, 1)
		writer := stdinWriter
		done := stdinDone
		go func() {
			_, copyErr := io.Copy(writer, spec.stdin)
			done <- errors.Join(copyErr, writer.Close())
		}()
	}

	result := localProcessResult{startedAt: startedAt, proof: proof}
	deadline := time.Now().Add(spec.timeout)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	processDone := false
	for !processDone {
		status, waitErr := windows.WaitForSingleObject(process.Process, 0)
		if waitErr != nil {
			err = waitErr
			break
		}
		if status == windows.WAIT_OBJECT_0 {
			processDone = true
			break
		}
		if status != uint32(windows.WAIT_TIMEOUT) {
			err = errors.New("unexpected Local Sandbox wait status")
			break
		}
		select {
		case <-ctx.Done():
			result.cancelled = true
			err = context.Canceled
		case inputErr := <-stdinDone:
			stdinDone = nil
			stdinWriter = nil
			if inputErr != nil {
				err = inputErr
			}
		case <-budget.signal:
			result.outputLimitExceeded = true
			err = ErrLocalSandboxOutputLimit
		case <-ticker.C:
			if time.Now().After(deadline) {
				result.timedOut = true
				err = context.DeadlineExceeded
			} else if writes, queryErr := localJobWrites(job); queryErr != nil {
				err = queryErr
			} else if writes > uint64(spec.writeMaximum) {
				result.writeLimitExceeded = true
				err = ErrLocalSandboxWriteLimit
			}
		default:
			time.Sleep(time.Millisecond)
		}
		if err != nil {
			break
		}
	}
	if spec.stdin != nil {
		_ = spec.stdin.Close()
	}
	if stdinWriter != nil {
		_ = stdinWriter.Close()
		stdinWriter = nil
	}
	if stdinDone != nil {
		<-stdinDone
		stdinDone = nil
	}
	if processDone {
		var exitCode uint32
		if exitErr := windows.GetExitCodeProcess(process.Process, &exitCode); exitErr != nil {
			err = errors.Join(err, exitErr)
		} else {
			result.exitCode = int(exitCode)
		}
	}
	// workspace_access never grants detached/background authority. Terminating
	// the owned Job after the root command exits also closes descendant pipe handles.
	if terminateErr := windows.TerminateJobObject(job, localJobExitCode); terminateErr != nil &&
		!errors.Is(terminateErr, windows.ERROR_ACCESS_DENIED) {
		err = errors.Join(err, terminateErr)
	}
	if !processDone {
		if code, waitErr := waitLocalProcess(process.Process, 3*time.Second); waitErr != nil {
			err = errors.Join(err, waitErr)
		} else {
			result.exitCode = code
		}
	}
	var waitErr error
	result.treeReaped, waitErr = waitLocalJobReaped(job, 3*time.Second)
	err = errors.Join(err, waitErr)
	result.stdout, waitErr = finishLocalOutput(outStream,
		spec.resources.MaxOutputBytes)
	err = errors.Join(err, waitErr)
	result.stderr, waitErr = finishLocalOutput(errStream,
		spec.resources.MaxOutputBytes)
	err = errors.Join(err, waitErr)
	budget.mu.Lock()
	result.outputLimitExceeded = result.outputLimitExceeded || budget.exceeded
	budget.mu.Unlock()
	if writes, queryErr := localJobWrites(job); queryErr != nil {
		err = errors.Join(err, queryErr)
	} else if writes > uint64(spec.writeMaximum) {
		result.writeLimitExceeded = true
		err = errors.Join(err, ErrLocalSandboxWriteLimit)
	}
	result.completedAt = time.Now().UTC()
	return result, err
}

func newLocalRestrictedToken() (windows.Token, error) {
	if err := localCreateRestrictedTokenProc.Find(); err != nil {
		return 0, err
	}
	var current windows.Token
	access := uint32(windows.TOKEN_DUPLICATE | windows.TOKEN_QUERY |
		windows.TOKEN_ASSIGN_PRIMARY | windows.TOKEN_ADJUST_DEFAULT)
	if err := windows.OpenProcessToken(windows.CurrentProcess(), access, &current); err != nil {
		return 0, err
	}
	defer current.Close()
	var restricted windows.Token
	success, _, callErr := localCreateRestrictedTokenProc.Call(uintptr(current),
		localCreateRestrictedDisableMaxPrivilege, 0, 0, 0, 0, 0, 0,
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
	label := windows.Tokenmandatorylabel{Label: windows.SIDAndAttributes{
		Sid: lowSID, Attributes: windows.SE_GROUP_INTEGRITY |
			windows.SE_GROUP_INTEGRITY_ENABLED}}
	if err := windows.SetTokenInformation(restricted, windows.TokenIntegrityLevel,
		(*byte)(unsafe.Pointer(&label)), label.Size()); err != nil {
		_ = restricted.Close()
		return 0, err
	}
	return restricted, nil
}

func newLocalJob(resources ResourceLimits) (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE |
		windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS | windows.JOB_OBJECT_LIMIT_JOB_MEMORY |
		windows.JOB_OBJECT_LIMIT_DIE_ON_UNHANDLED_EXCEPTION
	limits.BasicLimitInformation.ActiveProcessLimit = uint32(resources.PIDs)
	limits.JobMemoryLimit = uintptr(resources.MemoryBytes)
	if _, err := windows.SetInformationJobObject(job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		_ = windows.CloseHandle(job)
		return 0, err
	}
	ui := windows.JOBOBJECT_BASIC_UI_RESTRICTIONS{UIRestrictionsClass: windows.JOB_OBJECT_UILIMIT_DESKTOP | windows.JOB_OBJECT_UILIMIT_DISPLAYSETTINGS |
		windows.JOB_OBJECT_UILIMIT_EXITWINDOWS | windows.JOB_OBJECT_UILIMIT_GLOBALATOMS |
		windows.JOB_OBJECT_UILIMIT_HANDLES | windows.JOB_OBJECT_UILIMIT_READCLIPBOARD |
		windows.JOB_OBJECT_UILIMIT_SYSTEMPARAMETERS |
		windows.JOB_OBJECT_UILIMIT_WRITECLIPBOARD}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectBasicUIRestrictions,
		uintptr(unsafe.Pointer(&ui)), uint32(unsafe.Sizeof(ui))); err != nil {
		_ = windows.CloseHandle(job)
		return 0, err
	}
	denominator := uint64(1000 * runtime.NumCPU())
	rate := uint32(uint64(resources.CPUQuotaMillis) * 10_000 / denominator)
	if rate < 100 {
		rate = 100
	}
	if rate < 10_000 {
		cpu := localJobCPURateControl{ControlFlags: localJobCPUEnable | localJobCPUHardCap,
			CPURate: rate}
		if _, err := windows.SetInformationJobObject(job,
			windows.JobObjectCpuRateControlInformation,
			uintptr(unsafe.Pointer(&cpu)), uint32(unsafe.Sizeof(cpu))); err != nil {
			_ = windows.CloseHandle(job)
			return 0, err
		}
	}
	return job, nil
}

func verifyLocalProcessToken(process windows.Handle,
	profile localAppContainerProfile,
) (localTokenProof, error) {
	var token windows.Token
	if err := windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token); err != nil {
		return localTokenProof{}, err
	}
	defer token.Close()
	var proof localTokenProof
	var isAppContainer uint32
	var returned uint32
	if err := windows.GetTokenInformation(token, localTokenIsAppContainer,
		(*byte)(unsafe.Pointer(&isAppContainer)), uint32(unsafe.Sizeof(isAppContainer)),
		&returned); err != nil {
		return proof, err
	}
	proof.appContainer = isAppContainer == 1
	tokenGroups, err := token.GetTokenGroups()
	if err != nil {
		return proof, err
	}
	allApplicationPackages, err := windows.CreateWellKnownSid(windows.WinBuiltinAnyPackageSid)
	if err != nil {
		return proof, err
	}
	proof.lessPrivileged = true
	for _, group := range tokenGroups.AllGroups() {
		if group.Sid != nil && group.Sid.IsValid() && group.Sid.Equals(allApplicationPackages) &&
			group.Attributes&windows.SE_GROUP_ENABLED != 0 {
			proof.lessPrivileged = false
			break
		}
	}
	restricted, err := token.IsRestricted()
	if err != nil {
		return proof, err
	}
	proof.restricted = restricted
	capabilities, err := localTokenInformation(token, localTokenCapabilities)
	if err != nil {
		return proof, err
	}
	capabilityGroups := (*windows.Tokengroups)(unsafe.Pointer(&capabilities[0]))
	allCapabilities := capabilityGroups.AllGroups()
	proof.matchingCapabilitySIDs = localTokenHasExactCapabilities(allCapabilities,
		profile.filesystemCapabilitySID, profile.registryReadCapabilitySID)
	proof.zeroNetworkCapabilities = proof.matchingCapabilitySIDs
	containerInfo, err := localTokenInformation(token, localTokenAppContainerSID)
	if err != nil {
		return proof, err
	}
	actualSID := *(**windows.SID)(unsafe.Pointer(&containerInfo[0]))
	proof.matchingProfileSID = actualSID != nil && actualSID.IsValid() &&
		profile.sid != nil && actualSID.Equals(profile.sid)
	integrity, err := localTokenInformation(token, windows.TokenIntegrityLevel)
	if err != nil {
		return proof, err
	}
	label := (*windows.Tokenmandatorylabel)(unsafe.Pointer(&integrity[0]))
	if label.Label.Sid != nil && label.Label.Sid.IsValid() {
		integrityRID, ok := localSIDLastSubAuthority(integrity, label.Label.Sid)
		proof.lowIntegrity = ok && integrityRID <= localSecurityMandatoryLowRID
	}
	runtime.KeepAlive(capabilities)
	runtime.KeepAlive(containerInfo)
	runtime.KeepAlive(integrity)
	return proof, nil
}

// localSIDLastSubAuthority parses a SID that aliases a GetTokenInformation
// buffer without asking x/sys to perform unverifiable pointer arithmetic.
func localSIDLastSubAuthority(buffer []byte, sid *windows.SID) (uint32, bool) {
	if len(buffer) == 0 || sid == nil {
		return 0, false
	}
	base := uintptr(unsafe.Pointer(&buffer[0]))
	sidAddress := uintptr(unsafe.Pointer(sid))
	if sidAddress < base || sidAddress-base >= uintptr(len(buffer)) {
		return 0, false
	}
	sidBytes := buffer[int(sidAddress-base):]
	if len(sidBytes) < 8 || sidBytes[0] != 1 {
		return 0, false
	}
	count := int(sidBytes[1])
	if count == 0 || count > (len(sidBytes)-8)/4 {
		return 0, false
	}
	last := 8 + (count-1)*4
	return binary.LittleEndian.Uint32(sidBytes[last : last+4]), true
}

func localTokenHasExactCapabilities(actual []windows.SIDAndAttributes,
	expected ...*windows.SID,
) bool {
	if len(actual) != len(expected) || len(expected) == 0 {
		return false
	}
	matched := make([]bool, len(expected))
	for _, group := range actual {
		if group.Sid == nil || !group.Sid.IsValid() ||
			group.Attributes&windows.SE_GROUP_ENABLED == 0 {
			return false
		}
		found := false
		for index, sid := range expected {
			if !matched[index] && sid != nil && sid.IsValid() && group.Sid.Equals(sid) {
				matched[index], found = true, true
				break
			}
		}
		if !found {
			return false
		}
	}
	for _, value := range matched {
		if !value {
			return false
		}
	}
	return true
}

func localTokenInformation(token windows.Token, class uint32) ([]byte, error) {
	var size uint32
	err := windows.GetTokenInformation(token, class, nil, 0, &size)
	if !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) || size == 0 || size > 1024*1024 {
		return nil, errors.Join(err, ErrLocalSandboxBoundary)
	}
	buffer := make([]byte, size)
	if err := windows.GetTokenInformation(token, class, &buffer[0], size, &size); err != nil {
		return nil, err
	}
	return buffer, nil
}

func pinLocalExecutable(pathValue string) (string, windows.Handle, error) {
	if !validLocalHostRoot(pathValue) {
		return "", 0, ErrLocalSandboxBoundary
	}
	info, err := os.Lstat(pathValue)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", 0, ErrLocalSandboxBoundary
	}
	pointer, _ := windows.UTF16PtrFromString(pathValue)
	handle, err := windows.CreateFile(pointer,
		windows.GENERIC_READ|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return "", 0, err
	}
	attributes, err := windows.GetFileAttributes(pointer)
	if err != nil || attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		_ = windows.CloseHandle(handle)
		return "", 0, ErrLocalSandboxBoundary
	}
	finalPath, err := localFinalPath(handle)
	if err != nil || !strings.EqualFold(finalPath, pathValue) {
		_ = windows.CloseHandle(handle)
		return "", 0, ErrLocalSandboxBoundary
	}
	file, err := os.Open(pathValue)
	if err != nil {
		_ = windows.CloseHandle(handle)
		return "", 0, err
	}
	parsed, parseErr := pe.NewFile(file)
	if parseErr == nil {
		parseErr = parsed.Close()
	}
	parseErr = errors.Join(parseErr, file.Close())
	if parseErr != nil {
		_ = windows.CloseHandle(handle)
		return "", 0, ErrLocalSandboxBoundary
	}
	return finalPath, handle, nil
}

func newLocalPipe() (localPipe, error) {
	security := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		InheritHandle: 1}
	var readHandle, writeHandle windows.Handle
	if err := windows.CreatePipe(&readHandle, &writeHandle, &security, 0); err != nil {
		return localPipe{}, err
	}
	if err := windows.SetHandleInformation(readHandle, windows.HANDLE_FLAG_INHERIT, 0); err != nil {
		_ = windows.CloseHandle(readHandle)
		_ = windows.CloseHandle(writeHandle)
		return localPipe{}, err
	}
	return localPipe{read: readHandle, write: writeHandle}, nil
}

func newLocalInputPipe() (windows.Handle, *os.File, error) {
	security := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		InheritHandle: 1}
	var readHandle, writeHandle windows.Handle
	if err := windows.CreatePipe(&readHandle, &writeHandle, &security, 0); err != nil {
		return 0, nil, err
	}
	if err := windows.SetHandleInformation(writeHandle, windows.HANDLE_FLAG_INHERIT, 0); err != nil {
		_ = windows.CloseHandle(readHandle)
		_ = windows.CloseHandle(writeHandle)
		return 0, nil, err
	}
	writer := os.NewFile(uintptr(writeHandle), "local-sandbox-stdin")
	if writer == nil {
		_ = windows.CloseHandle(readHandle)
		_ = windows.CloseHandle(writeHandle)
		return 0, nil, ErrLocalSandboxUnavailable
	}
	return readHandle, writer, nil
}

func (p *localPipe) close() {
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

func openLocalNullInput() (windows.Handle, error) {
	pointer, _ := windows.UTF16PtrFromString("NUL")
	security := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		InheritHandle: 1}
	return windows.CreateFile(pointer, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, &security,
		windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
}

func (b *localOutputBudget) add(count int) int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := b.maximum + 1 - b.observed
	accepted := int64(count)
	if accepted > remaining {
		accepted = remaining
	}
	if accepted > 0 {
		b.observed += accepted
	}
	if b.observed > b.maximum && !b.exceeded {
		b.exceeded = true
		select {
		case b.signal <- struct{}{}:
		default:
		}
	}
	return accepted
}

func startLocalOutput(handle windows.Handle, budget *localOutputBudget,
	capture bool,
) *localOutputStream {
	stream := &localOutputStream{done: make(chan struct{})}
	go func() {
		defer close(stream.done)
		file := os.NewFile(uintptr(handle), "local-sandbox-output")
		if file == nil {
			if handle != 0 {
				_ = windows.CloseHandle(handle)
			}
			stream.err = ErrLocalSandboxUnavailable
			return
		}
		defer file.Close()
		buffer := make([]byte, 32*1024)
		for {
			count, err := file.Read(buffer)
			if count > 0 {
				accepted := budget.add(count)
				stream.observed += accepted
				if capture && len(stream.data) < localOutputCaptureMaximum {
					remaining := localOutputCaptureMaximum - len(stream.data)
					copyCount := count
					if copyCount > remaining {
						copyCount = remaining
					}
					if int64(copyCount) > accepted {
						copyCount = int(accepted)
					}
					stream.data = append(stream.data, buffer[:copyCount]...)
				}
			}
			if err != nil {
				if !errors.Is(err, io.EOF) && !errors.Is(err, windows.ERROR_BROKEN_PIPE) &&
					!errors.Is(err, windows.ERROR_INVALID_HANDLE) {
					stream.err = err
				}
				return
			}
		}
	}()
	return stream
}

func finishLocalOutput(stream *localOutputStream,
	maximum int64,
) (LocalCapturedOutput, error) {
	if stream == nil {
		return LocalCapturedOutput{}, ErrLocalSandboxBoundary
	}
	select {
	case <-stream.done:
	case <-time.After(5 * time.Second):
		return LocalCapturedOutput{}, errors.New("local sandbox output collector did not finish")
	}
	digest := sha256.Sum256(stream.data)
	value := LocalCapturedOutput{Data: append([]byte(nil), stream.data...),
		ObservedBytes: stream.observed, CapturedBytes: len(stream.data),
		SHA256:    hex.EncodeToString(digest[:]),
		Truncated: stream.observed > int64(len(stream.data))}
	return value, errors.Join(stream.err, value.Validate(maximum))
}

func waitLocalProcess(process windows.Handle, maximum time.Duration) (int, error) {
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
			return 0, ErrLocalSandboxUnavailable
		}
		if time.Now().After(deadline) {
			return 0, context.DeadlineExceeded
		}
	}
}

func waitLocalJobReaped(job windows.Handle, maximum time.Duration) (bool, error) {
	deadline := time.Now().Add(maximum)
	for {
		accounting := localJobBasicAccounting{}
		if err := windows.QueryInformationJobObject(job,
			windows.JobObjectBasicAccountingInformation,
			uintptr(unsafe.Pointer(&accounting)), uint32(unsafe.Sizeof(accounting)),
			nil); err != nil {
			return false, err
		}
		if accounting.ActiveProcesses == 0 {
			return true, nil
		}
		if time.Now().After(deadline) {
			return false, context.DeadlineExceeded
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func localJobWrites(job windows.Handle) (uint64, error) {
	accounting := localJobBasicAndIOAccounting{}
	if err := windows.QueryInformationJobObject(job,
		windows.JobObjectBasicAndIoAccountingInformation,
		uintptr(unsafe.Pointer(&accounting)), uint32(unsafe.Sizeof(accounting)),
		nil); err != nil {
		return 0, err
	}
	return accounting.IO.WriteTransferCount, nil
}

func localEnvironment(values map[string]string) ([]uint16, error) {
	if len(values) == 0 || len(values) > 128 {
		return nil, ErrLocalSandboxBoundary
	}
	names := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for name, value := range values {
		if name == "" || strings.ContainsAny(name, "=\x00") || strings.ContainsRune(value, 0) {
			return nil, ErrLocalSandboxBoundary
		}
		key := strings.ToUpper(name)
		if _, exists := seen[key]; exists {
			return nil, ErrLocalSandboxBoundary
		}
		seen[key] = struct{}{}
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		return strings.ToUpper(names[i]) < strings.ToUpper(names[j])
	})
	block := make([]uint16, 0, 2048)
	for _, name := range names {
		block = append(block, utf16.Encode([]rune(name+"="+values[name]))...)
		block = append(block, 0)
	}
	return append(block, 0), nil
}

func localDirectorySize(root string, maximum int64) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(pathValue string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return ErrLocalSandboxBoundary
		}
		if info.Mode().IsRegular() {
			if info.Size() > maximum-total {
				return ErrLocalSandboxWriteLimit
			}
			total += info.Size()
		}
		return nil
	})
	return total, err
}
