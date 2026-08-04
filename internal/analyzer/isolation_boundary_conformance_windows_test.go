//go:build windows

package analyzer

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	analyzerDisableMaxPrivilege = 0x1
	analyzerLowIntegrityRID     = 0x1000
)

var analyzerCreateRestrictedToken = windows.NewLazySystemDLL("advapi32.dll").
	NewProc("CreateRestrictedToken")

type windowsIsolationIdentityResult struct {
	Restricted            bool   `json:"restricted"`
	Elevated              bool   `json:"elevated"`
	AdministratorMember   bool   `json:"administrator_member"`
	IntegrityRID          uint32 `json:"integrity_rid"`
	EnabledPrivilegeCount uint32 `json:"enabled_privilege_count"`
	UserSID               string `json:"user_sid"`
}

type windowsIsolationFilesystemResult struct {
	InputRead           bool `json:"input_read"`
	InputWriteDenied    bool `json:"input_write_denied"`
	OutsideWriteDenied  bool `json:"outside_write_denied"`
	StagingWriteAllowed bool `json:"staging_write_allowed"`
}

func observeAnalyzerImmutableHandleHandoff(t *testing.T,
	approval AnalyzerScopeLimitsApproval,
) analyzerImmutableHandleObservation {
	t.Helper()
	original := []byte("caller-owned immutable analyzer image\n")
	replacement := []byte("path replacement must never become executable\n")
	directory := t.TempDir()
	path := filepath.Join(directory, "analyzer-image.bin")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := openWindowsAnalyzerReadHandle(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	handle := windows.Handle(file.Fd())
	if err := windows.SetHandleInformation(handle, windows.HANDLE_FLAG_INHERIT,
		windows.HANDLE_FLAG_INHERIT); err != nil {
		t.Fatal(err)
	}
	defer windows.SetHandleInformation(handle, windows.HANDLE_FLAG_INHERIT, 0)

	displaced := path + ".displaced"
	if err := os.Rename(path, displaced); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, replacement, 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestAnalyzerIsolationBoundaryHelper$")
	command.Env = windowsIsolationEnvironment(
		analyzerIsolationHelperModeEnv+"=windows-handle",
		analyzerIsolationHandleEnv+"="+strconv.FormatUint(uint64(handle), 10),
	)
	command.SysProcAttr = &syscall.SysProcAttr{
		HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW,
		AdditionalInheritedHandles: []syscall.Handle{syscall.Handle(handle)},
	}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("read inherited analyzer handle: %v output=%q", err, output)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	callerBytes, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	pathBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	originalDigest := analyzerIsolationBytesDigest(original)
	childDigest := stringTrimSpace(string(output))
	return analyzerImmutableHandleObservation{
		Mechanism:            "windows_inherited_read_handle.v1",
		ScopeApprovalSHA256:  analyzerIsolationScopeDigest(t, approval),
		CallerHandleRetained: analyzerIsolationBytesDigest(callerBytes) == originalDigest,
		ChildHandleInherited: childDigest == originalDigest,
		PathReplacedBeforeChildRead: analyzerIsolationBytesDigest(pathBytes) ==
			analyzerIsolationBytesDigest(replacement),
		OriginalBytesObserved:    childDigest == originalDigest,
		ReplacementBytesRejected: childDigest != analyzerIsolationBytesDigest(replacement),
		TestConformanceOnly:      true,
	}
}

func observeAnalyzerLowPrivilegeIdentity(t *testing.T,
	approval AnalyzerScopeLimitsApproval,
) analyzerLowPrivilegeIdentityObservation {
	t.Helper()
	token := newWindowsAnalyzerLowToken(t)
	defer token.Close()
	command := exec.Command(os.Args[0], "-test.run=^TestAnalyzerIsolationBoundaryHelper$")
	command.Env = windowsIsolationEnvironment(
		analyzerIsolationHelperModeEnv + "=windows-identity")
	command.Dir = filepath.Join(os.Getenv("SystemRoot"), "System32")
	command.SysProcAttr = &syscall.SysProcAttr{
		Token: syscall.Token(token), HideWindow: true,
		CreationFlags: windows.CREATE_NO_WINDOW,
	}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("start restricted identity helper: %v output=%q", err, output)
	}
	var result windowsIsolationIdentityResult
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("decode restricted identity observation: %v output=%q", err, output)
	}
	parentUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	parentIntegrity, err := windowsTokenIntegrityRID(windows.GetCurrentProcessToken())
	if err != nil {
		t.Fatal(err)
	}
	return analyzerLowPrivilegeIdentityObservation{
		Mechanism:           "windows_restricted_low_integrity_primary_token.v2",
		ScopeApprovalSHA256: analyzerIsolationScopeDigest(t, approval),
		SeparateIdentityContext: result.UserSID == parentUser.User.Sid.String() &&
			parentIntegrity > result.IntegrityRID,
		// TokenElevation describes UAC provenance and can remain true after a token
		// loses its effective administrator group. Membership is the authority check.
		NonAdministratorObserved: !result.AdministratorMember &&
			result.IntegrityRID <= analyzerLowIntegrityRID,
		AmbientPrivilegesDenied: result.EnabledPrivilegeCount <= 1,
		NoNewPrivilegesObserved: result.EnabledPrivilegeCount <= 1 &&
			result.IntegrityRID <= analyzerLowIntegrityRID,
		DedicatedAccountObserved: false,
		TestConformanceOnly:      true,
	}
}

func observeAnalyzerFilesystemIsolation(t *testing.T,
	approval AnalyzerScopeLimitsApproval,
) analyzerFilesystemIsolationObservation {
	t.Helper()
	root := t.TempDir()
	inputPath := filepath.Join(root, "input.bin")
	stagingPath := filepath.Join(root, "staging")
	outsidePath := filepath.Join(root, "outside")
	input := []byte("read-only analyzer input\n")
	resultBytes := []byte("validated analyzer result\n")
	if err := os.WriteFile(inputPath, input, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(stagingPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outsidePath, 0o700); err != nil {
		t.Fatal(err)
	}
	privateStaging := configureWindowsLowIntegrityStaging(stagingPath) == nil
	if !privateStaging {
		t.Fatal("could not configure protected low-integrity staging")
	}
	token := newWindowsAnalyzerLowToken(t)
	defer token.Close()
	command := exec.Command(os.Args[0], "-test.run=^TestAnalyzerIsolationBoundaryHelper$")
	command.Env = windowsIsolationEnvironment(
		analyzerIsolationHelperModeEnv+"=windows-filesystem",
		analyzerIsolationInputEnv+"="+inputPath,
		analyzerIsolationStagingEnv+"="+stagingPath,
		analyzerIsolationOutsideEnv+"="+outsidePath,
	)
	command.Dir = filepath.Join(os.Getenv("SystemRoot"), "System32")
	command.SysProcAttr = &syscall.SysProcAttr{
		Token: syscall.Token(token), HideWindow: true,
		CreationFlags: windows.CREATE_NO_WINDOW,
	}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("start filesystem isolation helper: %v output=%q", err, output)
	}
	var result windowsIsolationFilesystemResult
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("decode filesystem observation: %v output=%q", err, output)
	}
	stagingFile := filepath.Join(stagingPath, "result.tmp")
	observed, err := os.ReadFile(stagingFile)
	if err != nil || string(observed) != string(resultBytes) {
		t.Fatalf("staged result mismatch: err=%v value=%q", err, observed)
	}
	destination := filepath.Join(root, "result.final")
	noReplace := analyzerNoReplaceHandoff(t, stagingFile, destination, resultBytes)
	for _, path := range []string{stagingFile, destination, destination + ".conflict"} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
	}
	residueRemoved := true
	for _, path := range []string{stagingFile, destination, destination + ".conflict"} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			residueRemoved = false
		}
	}
	return analyzerFilesystemIsolationObservation{
		Mechanism:                "windows_low_integrity_acl_staging.v1",
		ScopeApprovalSHA256:      analyzerIsolationScopeDigest(t, approval),
		ReadOnlyInputObserved:    result.InputRead && result.InputWriteDenied,
		OutsideWriteDenied:       result.OutsideWriteDenied,
		PrivateStagingObserved:   privateStaging,
		StagingWriteObserved:     result.StagingWriteAllowed,
		NoReplaceHandoffObserved: noReplace, ResidueRemoved: residueRemoved,
		CompleteFilesystemSandbox: false, TestConformanceOnly: true,
	}
}

func runAnalyzerIsolationBoundaryHelper(mode string) error {
	switch mode {
	case "windows-handle":
		handleValue, err := strconv.ParseUint(os.Getenv(analyzerIsolationHandleEnv), 10, 64)
		if err != nil || handleValue == 0 {
			return errors.New("invalid inherited analyzer handle")
		}
		file := os.NewFile(uintptr(handleValue), "inherited-analyzer-image")
		if file == nil {
			return errors.New("inherited analyzer handle is unavailable")
		}
		defer file.Close()
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return err
		}
		content, err := io.ReadAll(file)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(os.Stdout, analyzerIsolationBytesDigest(content))
		return err
	case "windows-identity":
		result, err := inspectWindowsIsolationIdentity()
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(result)
	case "windows-filesystem":
		inputPath := os.Getenv(analyzerIsolationInputEnv)
		stagingPath := os.Getenv(analyzerIsolationStagingEnv)
		outsidePath := os.Getenv(analyzerIsolationOutsideEnv)
		input, readErr := os.ReadFile(inputPath)
		inputWriteErr := os.WriteFile(inputPath, []byte("forbidden"), 0o600)
		outsideErr := os.WriteFile(filepath.Join(outsidePath, "escape"), []byte("forbidden"), 0o600)
		stagingErr := os.WriteFile(filepath.Join(stagingPath, "result.tmp"),
			[]byte("validated analyzer result\n"), 0o600)
		return json.NewEncoder(os.Stdout).Encode(windowsIsolationFilesystemResult{
			InputRead:           readErr == nil && string(input) == "read-only analyzer input\n",
			InputWriteDenied:    inputWriteErr != nil,
			OutsideWriteDenied:  outsideErr != nil,
			StagingWriteAllowed: stagingErr == nil,
		})
	default:
		return fmt.Errorf("unknown Windows isolation helper mode %q", mode)
	}
}

func openWindowsAnalyzerReadHandle(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(handle), path), nil
}

func newWindowsAnalyzerLowToken(t *testing.T) windows.Token {
	t.Helper()
	var source windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY|
		windows.TOKEN_DUPLICATE|windows.TOKEN_ASSIGN_PRIMARY|windows.TOKEN_ADJUST_DEFAULT,
		&source); err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	administratorSID, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		t.Fatal(err)
	}
	disabledSIDs := []windows.SIDAndAttributes{{Sid: administratorSID}}
	var restricted windows.Token
	result, _, callErr := analyzerCreateRestrictedToken.Call(uintptr(source),
		analyzerDisableMaxPrivilege,
		uintptr(len(disabledSIDs)), uintptr(unsafe.Pointer(&disabledSIDs[0])),
		0, 0, 0, 0,
		uintptr(unsafe.Pointer(&restricted)))
	runtime.KeepAlive(disabledSIDs)
	runtime.KeepAlive(administratorSID)
	if result == 0 {
		t.Fatalf("CreateRestrictedToken: %v", callErr)
	}
	lowSID, err := windows.CreateWellKnownSid(windows.WinLowLabelSid)
	if err != nil {
		restricted.Close()
		t.Fatal(err)
	}
	label := windows.Tokenmandatorylabel{Label: windows.SIDAndAttributes{
		Sid: lowSID, Attributes: windows.SE_GROUP_INTEGRITY,
	}}
	if err := windows.SetTokenInformation(restricted, windows.TokenIntegrityLevel,
		(*byte)(unsafe.Pointer(&label)), label.Size()); err != nil {
		restricted.Close()
		t.Fatal(err)
	}
	return restricted
}

func inspectWindowsIsolationIdentity() (windowsIsolationIdentityResult, error) {
	token := windows.GetCurrentProcessToken()
	restricted, err := token.IsRestricted()
	if err != nil {
		return windowsIsolationIdentityResult{}, err
	}
	elevated := token.IsElevated()
	administratorSID, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return windowsIsolationIdentityResult{}, err
	}
	administratorMember, err := token.IsMember(administratorSID)
	if err != nil {
		return windowsIsolationIdentityResult{}, err
	}
	integrity, err := windowsTokenIntegrityRID(token)
	if err != nil {
		return windowsIsolationIdentityResult{}, err
	}
	privileges, err := windowsTokenEnabledPrivilegeCount(token)
	if err != nil {
		return windowsIsolationIdentityResult{}, err
	}
	user, err := token.GetTokenUser()
	if err != nil {
		return windowsIsolationIdentityResult{}, err
	}
	return windowsIsolationIdentityResult{
		Restricted: restricted, Elevated: elevated,
		AdministratorMember: administratorMember,
		IntegrityRID:        integrity, EnabledPrivilegeCount: privileges,
		UserSID: user.User.Sid.String(),
	}, nil
}

func windowsTokenIntegrityRID(token windows.Token) (uint32, error) {
	var size uint32
	err := windows.GetTokenInformation(token, windows.TokenIntegrityLevel, nil, 0, &size)
	if !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) || size == 0 {
		return 0, err
	}
	buffer := make([]byte, size)
	if err := windows.GetTokenInformation(token, windows.TokenIntegrityLevel, &buffer[0],
		size, &size); err != nil {
		return 0, err
	}
	label := (*windows.Tokenmandatorylabel)(unsafe.Pointer(&buffer[0]))
	sidLength := windows.GetLengthSid(label.Label.Sid)
	if sidLength < 12 {
		return 0, errors.New("token integrity SID is truncated")
	}
	sidBytes := make([]byte, sidLength)
	if err := windows.CopySid(sidLength,
		(*windows.SID)(unsafe.Pointer(&sidBytes[0])), label.Label.Sid); err != nil {
		return 0, err
	}
	count := int(sidBytes[1])
	if count == 0 {
		return 0, errors.New("token integrity SID has no sub-authority")
	}
	lastOffset := 8 + (count-1)*4
	if lastOffset+4 > len(sidBytes) {
		return 0, errors.New("token integrity SID sub-authorities are truncated")
	}
	return binary.LittleEndian.Uint32(sidBytes[lastOffset : lastOffset+4]), nil
}

func windowsTokenEnabledPrivilegeCount(token windows.Token) (uint32, error) {
	var size uint32
	err := windows.GetTokenInformation(token, windows.TokenPrivileges, nil, 0, &size)
	if !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) || size < 4 {
		return 0, err
	}
	buffer := make([]byte, size)
	if err := windows.GetTokenInformation(token, windows.TokenPrivileges, &buffer[0],
		size, &size); err != nil {
		return 0, err
	}
	privileges := (*windows.Tokenprivileges)(unsafe.Pointer(&buffer[0]))
	var enabled uint32
	for _, privilege := range privileges.AllPrivileges() {
		if privilege.Attributes&windows.SE_PRIVILEGE_ENABLED != 0 {
			enabled++
		}
	}
	return enabled, nil
}

func configureWindowsLowIntegrityStaging(path string) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	systemSID, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return err
	}
	access := []windows.EXPLICIT_ACCESS{
		{AccessPermissions: windows.ACCESS_MASK(windows.GENERIC_ALL),
			AccessMode:  windows.GRANT_ACCESS,
			Inheritance: windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
			Trustee: windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_USER,
				TrusteeValue: windows.TrusteeValueFromSID(user.User.Sid)}},
		{AccessPermissions: windows.ACCESS_MASK(windows.GENERIC_ALL),
			AccessMode:  windows.GRANT_ACCESS,
			Inheritance: windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
			Trustee: windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_USER,
				TrusteeValue: windows.TrusteeValueFromSID(systemSID)}},
	}
	acl, err := windows.ACLFromEntries(access, nil)
	if err != nil {
		return err
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, acl, nil); err != nil {
		return err
	}
	labelDescriptor, err := windows.SecurityDescriptorFromString("S:(ML;OICI;NW;;;LW)")
	if err != nil {
		return err
	}
	sacl, _, err := labelDescriptor.SACL()
	if err != nil {
		return err
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.LABEL_SECURITY_INFORMATION, nil, nil, nil, sacl); err != nil {
		return err
	}
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	control, _, err := descriptor.Control()
	if err != nil {
		return err
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		return errors.New("staging DACL is not protected")
	}
	return nil
}

func windowsIsolationEnvironment(values ...string) []string {
	environment := append([]string(nil), values...)
	if systemRoot := os.Getenv("SystemRoot"); systemRoot != "" {
		environment = append(environment, "SystemRoot="+systemRoot)
	}
	if windir := os.Getenv("WINDIR"); windir != "" {
		environment = append(environment, "WINDIR="+windir)
	}
	return environment
}
