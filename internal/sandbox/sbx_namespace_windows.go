//go:build windows

package sandbox

import (
	"errors"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

func sbxAcquireNamespaceNamedLock(appName string) (*sbxNamespaceLock, error) {
	if !sbxAppName.MatchString(appName) {
		return nil, ErrSBXOwnership
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || user == nil || user.User.Sid == nil || !user.User.Sid.IsValid() {
		return nil, errors.Join(ErrSBXOwnership, err)
	}
	sid := user.User.Sid.String()
	// Global avoids one controller per terminal session. The actual token SID
	// avoids profile/environment/MSIX path redirection splitting the lock.
	name := `Global\TraverseBoard-SBX-` + sbxDigest(sid, appName)
	pointer, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, errors.Join(ErrSBXOwnership, err)
	}
	// A protected explicit DACL admits this OS account in every login session,
	// including elevated instances, and SYSTEM. Other accounts cannot acquire
	// or release the lock. A pre-created incompatible object fails closed.
	descriptor, err := windows.SecurityDescriptorFromString(
		"D:P(A;;GA;;;SY)(A;;GA;;;" + sid + ")")
	if err != nil {
		return nil, errors.Join(ErrSBXOwnership, err)
	}
	attributes := windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: descriptor,
	}
	ready := make(chan error, 1)
	release := make(chan struct{})
	done := make(chan error, 1)
	// Mutex ownership belongs to an OS thread. A dedicated pinned goroutine
	// performs both Wait and Release; callers may Close from any goroutine.
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		handle, createErr := windows.CreateMutexEx(&attributes, pointer, 0,
			windows.SYNCHRONIZE|windows.MUTEX_MODIFY_STATE)
		runtime.KeepAlive(descriptor)
		runtime.KeepAlive(attributes)
		if createErr != nil && !errors.Is(createErr, windows.ERROR_ALREADY_EXISTS) {
			ready <- errors.Join(ErrSBXOwnership, createErr)
			return
		}
		if handle == 0 {
			ready <- ErrSBXOwnership
			return
		}
		status, waitErr := windows.WaitForSingleObject(handle, 0)
		if waitErr != nil || (status != windows.WAIT_OBJECT_0 && status != windows.WAIT_ABANDONED) {
			_ = windows.CloseHandle(handle)
			ready <- errors.Join(ErrSBXOwnership, waitErr)
			return
		}
		// An abandoned owner's journal is still authoritative. The new holder
		// must recover before admitting work; abandonment never adopts a VM.
		ready <- nil
		<-release
		done <- errors.Join(windows.ReleaseMutex(handle), windows.CloseHandle(handle))
	}()
	if err := <-ready; err != nil {
		return nil, err
	}
	return &sbxNamespaceLock{close: func() error {
		close(release)
		return <-done
	}}, nil
}
