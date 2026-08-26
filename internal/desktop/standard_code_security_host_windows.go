//go:build windows

package desktop

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"unsafe"

	"cyberagent-workbench/internal/packagede2e"
	"golang.org/x/sys/windows"
)

const (
	standardCodeSecurityCredentialPrefix = "TraverseBoard.Issue181."
	standardCodeSecurityCredentialBlob   = "issue181-synthetic-credential-never-exposed"
	standardCodeSecurityCloudSentinel    = "Issue181HostCloudSentinel"
	standardCodeSecurityProxySentinel    = "http://issue181.invalid:181"
)

type standardCodeSecurityWindowsCredential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        windows.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

var (
	standardCodeSecurityCredWrite  = windows.NewLazySystemDLL("advapi32.dll").NewProc("CredWriteW")
	standardCodeSecurityCredRead   = windows.NewLazySystemDLL("advapi32.dll").NewProc("CredReadW")
	standardCodeSecurityCredDelete = windows.NewLazySystemDLL("advapi32.dll").NewProc("CredDeleteW")
	standardCodeSecurityCredFree   = windows.NewLazySystemDLL("advapi32.dll").NewProc("CredFree")
)

func (d *standardCodeSecurityDriver) prepareStandardCodeSecurityProbeBoundary(
	request packagede2e.SecurityDriverCase,
) (string, func() error, func() error, error) {
	argument := d.outsideSentinel
	cleanup := func() error { return nil }
	verify := func() error { return nil }
	switch request.Attack.ID {
	case "credential_manager":
		target := standardCodeSecurityCredentialPrefix + hashSecurityValue(d.readToken)[:24]
		if err := writeStandardCodeSecurityCredential(target); err != nil {
			return "", nil, nil, err
		}
		argument = target
		cleanup = func() error { return deleteStandardCodeSecurityCredential(target) }
		verify = func() error {
			found, err := standardCodeSecurityCredentialReadable(target)
			if err != nil {
				return err
			}
			if !found {
				return errors.New("sandboxed process reached the harness-owned host credential")
			}
			return nil
		}
	case "credential_cloud_environment":
		restoreCloud, err := setStandardCodeSecurityEnvironment(
			"AWS_ACCESS_KEY_ID", standardCodeSecurityCloudSentinel)
		if err != nil {
			return "", nil, nil, err
		}
		restoreProxy, err := setStandardCodeSecurityEnvironment(
			"HTTP_PROXY", standardCodeSecurityProxySentinel)
		if err != nil {
			_ = restoreCloud()
			return "", nil, nil, err
		}
		cleanup = func() error { return errors.Join(restoreProxy(), restoreCloud()) }
	case "process_inherited_handle":
		name, handle, err := createStandardCodeSecurityNamedPipe(d.readToken)
		if err != nil {
			return "", nil, nil, err
		}
		argument = name
		cleanup = func() error { return windows.CloseHandle(handle) }
	}
	return argument, cleanup, verify, nil
}

func setStandardCodeSecurityEnvironment(name, value string) (func() error, error) {
	original, found := os.LookupEnv(name)
	if err := os.Setenv(name, value); err != nil {
		return nil, err
	}
	return func() error {
		if found {
			return os.Setenv(name, original)
		}
		return os.Unsetenv(name)
	}, nil
}

func createStandardCodeSecurityNamedPipe(seed string) (string, windows.Handle, error) {
	name := `\\.\pipe\TraverseBoard-Issue181-` + hashSecurityValue(seed)[:24]
	pointer, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return "", 0, err
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || user == nil || user.User.Sid == nil || !user.User.Sid.IsValid() {
		return "", 0, errors.Join(err, errors.New("resolve packaged security pipe owner"))
	}
	descriptor, err := windows.SecurityDescriptorFromString(
		"D:P(A;;GA;;;SY)(A;;GA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return "", 0, err
	}
	attributes := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: descriptor}
	handle, err := windows.CreateNamedPipe(pointer,
		windows.PIPE_ACCESS_DUPLEX|windows.FILE_FLAG_FIRST_PIPE_INSTANCE,
		windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT|
			windows.PIPE_REJECT_REMOTE_CLIENTS,
		1, 4096, 4096, 0, &attributes)
	runtime.KeepAlive(descriptor)
	runtime.KeepAlive(attributes)
	if err != nil {
		return "", 0, fmt.Errorf("create harness-owned host named pipe: %w", err)
	}
	return name, handle, nil
}

func writeStandardCodeSecurityCredential(target string) error {
	found, err := standardCodeSecurityCredentialReadable(target)
	if err != nil {
		return err
	}
	if found {
		return errors.New("harness-owned credential target already exists")
	}
	targetPointer, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	userPointer, err := windows.UTF16PtrFromString("TraverseBoardIssue181Harness")
	if err != nil {
		return err
	}
	blob := []byte(standardCodeSecurityCredentialBlob)
	credential := standardCodeSecurityWindowsCredential{Type: 1,
		TargetName: targetPointer, CredentialBlobSize: uint32(len(blob)),
		CredentialBlob: &blob[0], Persist: 1, UserName: userPointer}
	success, _, callErr := standardCodeSecurityCredWrite.Call(
		uintptr(unsafe.Pointer(&credential)), 0)
	runtime.KeepAlive(targetPointer)
	runtime.KeepAlive(userPointer)
	runtime.KeepAlive(blob)
	if success == 0 {
		return fmt.Errorf("write harness-owned synthetic credential: %w", callErr)
	}
	return nil
}

func standardCodeSecurityCredentialReadable(target string) (bool, error) {
	pointer, err := windows.UTF16PtrFromString(strings.TrimSpace(target))
	if err != nil {
		return false, err
	}
	var credential *standardCodeSecurityWindowsCredential
	success, _, callErr := standardCodeSecurityCredRead.Call(
		uintptr(unsafe.Pointer(pointer)), 1, 0, uintptr(unsafe.Pointer(&credential)))
	runtime.KeepAlive(pointer)
	if success == 0 {
		if errors.Is(callErr, windows.ERROR_NOT_FOUND) {
			return false, nil
		}
		return false, fmt.Errorf("read harness-owned synthetic credential: %w", callErr)
	}
	if credential == nil {
		return false, errors.New("synthetic credential read returned no record")
	}
	standardCodeSecurityCredFree.Call(uintptr(unsafe.Pointer(credential)))
	return true, nil
}

func deleteStandardCodeSecurityCredential(target string) error {
	pointer, err := windows.UTF16PtrFromString(strings.TrimSpace(target))
	if err != nil {
		return err
	}
	success, _, callErr := standardCodeSecurityCredDelete.Call(
		uintptr(unsafe.Pointer(pointer)), 1, 0)
	runtime.KeepAlive(pointer)
	if success == 0 && !errors.Is(callErr, windows.ERROR_NOT_FOUND) {
		return fmt.Errorf("delete harness-owned synthetic credential: %w", callErr)
	}
	return nil
}
