//go:build darwin && cgo && keychainintegration

package credential

/*
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>
#include <stdlib.h>

// Deliberately bypass Go validation to verify that reads reject corrupt items.
static OSStatus traverse_test_raw_item(CFDictionaryRef query, SecKeychainRef target,
	const void *bytes, CFIndex size) {
	CFDataRef data = CFDataCreate(NULL, bytes, size);
	if (!data) return errSecAllocate;
	CFMutableDictionaryRef attributes = CFDictionaryCreateMutableCopy(NULL, 0, query);
	if (!attributes) {
		CFRelease(data);
		return errSecAllocate;
	}
	CFDictionaryRemoveValue(attributes, kSecMatchSearchList);
	CFDictionarySetValue(attributes, kSecUseKeychain, target);
	CFDictionarySetValue(attributes, kSecValueData, data);
	OSStatus status = SecItemAdd(attributes, NULL);
	CFRelease(attributes);
	CFRelease(data);
	return status;
}
*/
import "C"

import (
	"errors"
	"runtime"
	"unsafe"
)

// Test-only helpers are excluded unless keychainintegration is explicitly set.
// No helper changes the default keychain, search list, or interaction policy.
type darwinTestKeychainState struct {
	defaultKeychain C.SecKeychainRef
	defaultStatus   C.OSStatus
	searchList      C.CFArrayRef
}

func snapshotDarwinTestKeychains() (darwinTestKeychainState, error) {
	var state darwinTestKeychainState
	state.defaultStatus = C.SecKeychainCopyDefault(&state.defaultKeychain)
	if state.defaultStatus != C.errSecSuccess && state.defaultStatus != C.errSecNoDefaultKeychain {
		state.release()
		return darwinTestKeychainState{}, darwinStatusError(int32(state.defaultStatus))
	}
	if status := C.SecKeychainCopySearchList(&state.searchList); status != C.errSecSuccess {
		state.release()
		return darwinTestKeychainState{}, darwinStatusError(int32(status))
	}
	return state, nil
}

func (s darwinTestKeychainState) release() {
	if s.defaultKeychain != 0 {
		C.CFRelease(C.CFTypeRef(s.defaultKeychain))
	}
	if s.searchList != 0 {
		C.CFRelease(C.CFTypeRef(s.searchList))
	}
}

func (s darwinTestKeychainState) unchanged() (bool, error) {
	next, err := snapshotDarwinTestKeychains()
	if err != nil {
		return false, err
	}
	defer next.release()
	if s.defaultStatus != next.defaultStatus {
		return false, nil
	}
	if s.defaultStatus == C.errSecSuccess && C.CFEqual(C.CFTypeRef(s.defaultKeychain), C.CFTypeRef(next.defaultKeychain)) == 0 {
		return false, nil
	}
	return C.CFEqual(C.CFTypeRef(s.searchList), C.CFTypeRef(next.searchList)) != 0, nil
}

func createDarwinTestKeychain(path string, password []byte) (func() error, error) {
	if len(password) == 0 {
		return nil, errors.New("test keychain password is required")
	}
	pathRef := C.CString(path)
	defer C.free(unsafe.Pointer(pathRef))
	var target C.SecKeychainRef
	status := C.SecKeychainCreate(pathRef, C.UInt32(len(password)), unsafe.Pointer(&password[0]), C.Boolean(0), 0, &target)
	runtime.KeepAlive(password)
	if status != C.errSecSuccess {
		if target != 0 {
			C.CFRelease(C.CFTypeRef(target))
		}
		return nil, darwinStatusError(int32(status))
	}
	cleanup := func() error {
		status := C.SecKeychainDelete(target)
		C.CFRelease(C.CFTypeRef(target))
		return darwinStatusError(int32(status))
	}
	status = C.SecKeychainUnlock(target, C.UInt32(len(password)), unsafe.Pointer(&password[0]), C.Boolean(1))
	runtime.KeepAlive(password)
	if status != C.errSecSuccess {
		_ = cleanup()
		return nil, darwinStatusError(int32(status))
	}
	return cleanup, nil
}

func writeDarwinTestRaw(s darwinStore, name string, bytes []byte) error {
	target, query, err := s.query(name)
	if err != nil {
		return err
	}
	defer C.CFRelease(C.CFTypeRef(target))
	defer C.CFRelease(C.CFTypeRef(query))
	var ptr unsafe.Pointer
	if len(bytes) > 0 {
		ptr = unsafe.Pointer(&bytes[0])
	}
	status := C.traverse_test_raw_item(query, target, ptr, C.CFIndex(len(bytes)))
	runtime.KeepAlive(bytes)
	return darwinStatusError(int32(status))
}
