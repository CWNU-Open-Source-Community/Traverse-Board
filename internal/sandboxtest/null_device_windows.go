//go:build windows

// Package sandboxtest contains Windows host fixtures used only by real Local
// Sandbox conformance tests.
package sandboxtest

import (
	"errors"
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

const nullDeviceMutexName = `Local\TraverseBoard.LocalSandbox.Test.NullDevice.v1`

// PrepareNullDevice supplies the downlevel Windows null-device DACL required
// by LPAC subprocesses and returns an exact restoration function. A named
// mutex serializes test packages only when the machine-wide DACL needs a
// temporary change.
func PrepareNullDevice() (func() error, error) {
	releaseMutex, err := acquireNullDeviceMutex()
	if err != nil {
		return nil, err
	}
	fail := func(cause error) (func() error, error) {
		return nil, errors.Join(cause, releaseMutex())
	}

	pointer, err := windows.UTF16PtrFromString(`\\.\NUL`)
	if err != nil {
		return fail(err)
	}
	restrictedPackagesSID, err := windows.StringToSid("S-1-15-2-2")
	if err != nil {
		return fail(err)
	}
	open := func(access uint32) (windows.Handle, error) {
		return windows.CreateFile(pointer, access,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
			nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	}

	handle, err := open(windows.GENERIC_READ | windows.READ_CONTROL)
	if err != nil {
		return fail(err)
	}
	descriptor, err := windows.GetSecurityInfo(handle, windows.SE_KERNEL_OBJECT,
		windows.DACL_SECURITY_INFORMATION)
	if err != nil || descriptor == nil {
		_ = windows.CloseHandle(handle)
		return fail(errors.Join(err, errors.New("read null-device DACL")))
	}
	existingDACL, _, err := descriptor.DACL()
	if err != nil || existingDACL == nil {
		_ = windows.CloseHandle(handle)
		return fail(errors.Join(err, errors.New("resolve null-device DACL")))
	}
	if daclGrantsRestrictedPackages(existingDACL, restrictedPackagesSID) {
		closeErr := windows.CloseHandle(handle)
		return func() error { return nil }, errors.Join(closeErr, releaseMutex())
	}
	_ = windows.CloseHandle(handle)

	handle, err = open(windows.GENERIC_READ | windows.READ_CONTROL | windows.WRITE_DAC)
	if err != nil {
		return fail(err)
	}
	closeWithError := func(cause error) (func() error, error) {
		_ = windows.CloseHandle(handle)
		return fail(cause)
	}
	descriptor, err = windows.GetSecurityInfo(handle, windows.SE_KERNEL_OBJECT,
		windows.DACL_SECURITY_INFORMATION)
	if err != nil || descriptor == nil {
		return closeWithError(errors.Join(err, errors.New("capture null-device DACL")))
	}
	originalDACL, _, err := descriptor.DACL()
	if err != nil || originalDACL == nil {
		return closeWithError(errors.Join(err, errors.New("capture null-device ACL")))
	}
	entry := windows.EXPLICIT_ACCESS{
		AccessPermissions: windows.GENERIC_READ | windows.GENERIC_WRITE | windows.GENERIC_EXECUTE,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
			TrusteeValue: windows.TrusteeValueFromSID(restrictedPackagesSID)},
	}
	preparedDACL, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{entry}, originalDACL)
	if err != nil {
		return closeWithError(err)
	}
	if err := windows.SetSecurityInfo(handle, windows.SE_KERNEL_OBJECT,
		windows.DACL_SECURITY_INFORMATION, nil, nil, preparedDACL, nil); err != nil {
		return closeWithError(err)
	}
	restore := func() error {
		restoreErr := windows.SetSecurityInfo(handle, windows.SE_KERNEL_OBJECT,
			windows.DACL_SECURITY_INFORMATION, nil, nil, originalDACL, nil)
		closeErr := windows.CloseHandle(handle)
		return errors.Join(restoreErr, closeErr, releaseMutex())
	}
	runtime.KeepAlive(restrictedPackagesSID)
	return restore, nil
}

func acquireNullDeviceMutex() (func() error, error) {
	pointer, err := windows.UTF16PtrFromString(nullDeviceMutexName)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateMutex(nil, false, pointer)
	if err != nil {
		return nil, err
	}
	status, err := windows.WaitForSingleObject(handle, 10*60*1000)
	if err != nil || (status != windows.WAIT_OBJECT_0 && status != windows.WAIT_ABANDONED) {
		_ = windows.CloseHandle(handle)
		return nil, errors.Join(err, fmt.Errorf("wait for null-device test mutex: %d", status))
	}
	return func() error {
		return errors.Join(windows.ReleaseMutex(handle), windows.CloseHandle(handle))
	}, nil
}

func daclGrantsRestrictedPackages(dacl *windows.ACL, sid *windows.SID) bool {
	if dacl == nil || sid == nil || !sid.IsValid() {
		return false
	}
	for index := uint32(0); index < uint32(dacl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &ace); err != nil || ace == nil ||
			ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			continue
		}
		aceSID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if aceSID.IsValid() && aceSID.Equals(sid) && ace.Mask != 0 {
			return true
		}
	}
	return false
}
