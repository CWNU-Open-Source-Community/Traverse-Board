//go:build darwin && cgo

package credential

/*
#cgo LDFLAGS: -framework CoreFoundation -framework Security
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>
#include <stdlib.h>

// Use a single, explicit file-based keychain throughout each operation. The
// private path is only set by package tests; production uses the user's default.
static OSStatus traverse_keychain_target(const char *path, SecKeychainRef *target) {
	*target = NULL;
	if (path != NULL) return SecKeychainOpen(path, target);
	return SecKeychainCopyDefault(target);
}

static CFMutableDictionaryRef traverse_keychain_query(SecKeychainRef target,
	const char *service, const char *account) {
	CFMutableDictionaryRef query = NULL;
	CFStringRef serviceRef = CFStringCreateWithCString(NULL, service, kCFStringEncodingUTF8);
	CFStringRef accountRef = CFStringCreateWithCString(NULL, account, kCFStringEncodingUTF8);
	const void *targets[] = { target };
	CFArrayRef search = CFArrayCreate(NULL, targets, 1, &kCFTypeArrayCallBacks);
	if (serviceRef && accountRef && search) {
		query = CFDictionaryCreateMutable(NULL, 0, &kCFTypeDictionaryKeyCallBacks,
			&kCFTypeDictionaryValueCallBacks);
		if (query) {
			CFDictionarySetValue(query, kSecClass, kSecClassGenericPassword);
			CFDictionarySetValue(query, kSecAttrService, serviceRef);
			CFDictionarySetValue(query, kSecAttrAccount, accountRef);
			CFDictionarySetValue(query, kSecMatchSearchList, search);
		}
	}
	if (search) CFRelease(search);
	if (accountRef) CFRelease(accountRef);
	if (serviceRef) CFRelease(serviceRef);
	return query;
}

static void traverse_clear_secret(CFMutableDataRef data) {
	if (!data) return;
	volatile UInt8 *bytes = CFDataGetMutableBytePtr(data);
	CFIndex size = CFDataGetLength(data);
	for (CFIndex i = 0; i < size; i++) bytes[i] = 0;
	CFRelease(data);
}

static OSStatus traverse_keychain_put(CFDictionaryRef query, SecKeychainRef target,
	const void *bytes, CFIndex size, int add) {
	CFMutableDataRef data = CFDataCreateMutable(NULL, size);
	if (!data) return errSecAllocate;
	CFDataAppendBytes(data, bytes, size);
	CFMutableDictionaryRef attributes = CFDictionaryCreateMutable(NULL, 0,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	if (!attributes) {
		traverse_clear_secret(data);
		return errSecAllocate;
	}
	OSStatus status;
	if (add) {
		CFDictionarySetValue(attributes, kSecClass, kSecClassGenericPassword);
		CFDictionarySetValue(attributes, kSecAttrService, CFDictionaryGetValue(query, kSecAttrService));
		CFDictionarySetValue(attributes, kSecAttrAccount, CFDictionaryGetValue(query, kSecAttrAccount));
		CFDictionarySetValue(attributes, kSecAttrLabel, CFSTR("Traverse Board"));
		CFDictionarySetValue(attributes, kSecUseKeychain, target);
		CFDictionarySetValue(attributes, kSecValueData, data);
		status = SecItemAdd(attributes, NULL);
	} else {
		// Preserve the existing item's access controls. Never delete to replace.
		CFDictionarySetValue(attributes, kSecValueData, data);
		status = SecItemUpdate(query, attributes);
	}
	CFRelease(attributes);
	traverse_clear_secret(data);
	return status;
}

static OSStatus traverse_keychain_read(CFMutableDictionaryRef query, CFDataRef *data) {
	*data = NULL;
	CFDictionarySetValue(query, kSecMatchLimit, kSecMatchLimitOne);
	CFDictionarySetValue(query, kSecReturnData, kCFBooleanTrue);
	CFTypeRef result = NULL;
	OSStatus status = SecItemCopyMatching(query, &result);
	if (status != errSecSuccess) {
		if (result) CFRelease(result);
		return status;
	}
	if (!result || CFGetTypeID(result) != CFDataGetTypeID()) {
		if (result) CFRelease(result);
		return errSecDecode;
	}
	*data = (CFDataRef)result;
	return errSecSuccess;
}
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"unsafe"
)

const darwinCredentialService = "workbench.prayu.desktop.credentials.v1"

const (
	darwinSuccess         = int32(C.errSecSuccess)
	darwinItemNotFound    = int32(C.errSecItemNotFound)
	darwinDuplicateItem   = int32(C.errSecDuplicateItem)
	darwinUserCanceled    = int32(C.errSecUserCanceled)
	darwinAuthFailed      = int32(C.errSecAuthFailed)
	darwinInteraction     = int32(C.errSecInteractionNotAllowed)
	darwinInteractionNeed = int32(C.errSecInteractionRequired)
)

type darwinStore struct {
	// Unexported overrides allow isolated package tests. There is deliberately
	// no environment variable, renderer argument or public path configuration.
	keychainPath string
	service      string
}

func newSystemStore() Store         { return darwinStore{} }
func (darwinStore) Kind() string    { return "macos_keychain" }
func (darwinStore) Available() bool { return true }

// Available reports compiled platform support, not whether the keychain is
// unlocked or the user will grant access. Those failures are operation errors.
func (s darwinStore) query(name string) (C.SecKeychainRef, C.CFMutableDictionaryRef, error) {
	var path *C.char
	if s.keychainPath != "" {
		path = C.CString(s.keychainPath)
		defer C.free(unsafe.Pointer(path))
	}
	var target C.SecKeychainRef
	status := C.traverse_keychain_target(path, &target)
	if status != C.errSecSuccess {
		if target != 0 {
			C.CFRelease(C.CFTypeRef(target))
		}
		return 0, 0, darwinStatusError(int32(status))
	}
	if target == 0 {
		return 0, 0, errors.New("macOS Keychain returned an empty target")
	}
	service := s.service
	if service == "" {
		service = darwinCredentialService
	}
	serviceRef := C.CString(service)
	accountRef := C.CString(name)
	defer C.free(unsafe.Pointer(serviceRef))
	defer C.free(unsafe.Pointer(accountRef))
	query := C.traverse_keychain_query(target, serviceRef, accountRef)
	if query == 0 {
		C.CFRelease(C.CFTypeRef(target))
		return 0, 0, darwinStatusError(int32(C.errSecAllocate))
	}
	return target, query, nil
}

func (s darwinStore) Put(ctx context.Context, name, secret string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !ValidName(name) || !ValidSecret(secret) {
		return errors.New("credential name or secret is invalid")
	}
	target, query, err := s.query(name)
	if err != nil {
		return err
	}
	defer C.CFRelease(C.CFTypeRef(target))
	defer C.CFRelease(C.CFTypeRef(query))
	blob := []byte(secret)
	defer clear(blob)
	write := func(add bool) int32 {
		mode := C.int(0)
		if add {
			mode = 1
		}
		status := C.traverse_keychain_put(C.CFDictionaryRef(query), target, unsafe.Pointer(&blob[0]), C.CFIndex(len(blob)), mode)
		runtime.KeepAlive(blob)
		return int32(status)
	}
	return darwinUpsert(ctx, func() int32 { return write(false) }, func() int32 { return write(true) })
}

// The synchronous OS call can show its own authorization dialog. Do not return
// early on context cancellation while an unobserved background write continues.
// A completed success remains success even if the context expired during it.
func darwinUpsert(ctx context.Context, update, add func() int32) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	status := update()
	if status != darwinItemNotFound {
		return darwinStatusError(status)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	status = add()
	if status != darwinDuplicateItem {
		return darwinStatusError(status)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return darwinStatusError(update())
}

func (s darwinStore) Delete(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !ValidName(name) {
		return errors.New("credential name is invalid")
	}
	target, query, err := s.query(name)
	if err != nil {
		return err
	}
	defer C.CFRelease(C.CFTypeRef(target))
	defer C.CFRelease(C.CFTypeRef(query))
	if err := ctx.Err(); err != nil {
		return err
	}
	status := int32(C.SecItemDelete(C.CFDictionaryRef(query)))
	if status == darwinItemNotFound {
		return nil
	}
	return darwinStatusError(status)
}

func (s darwinStore) Get(ctx context.Context, name string) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	if !ValidName(name) {
		return "", false, errors.New("credential name is invalid")
	}
	target, query, err := s.query(name)
	if err != nil {
		return "", false, err
	}
	defer C.CFRelease(C.CFTypeRef(target))
	defer C.CFRelease(C.CFTypeRef(query))
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	var data C.CFDataRef
	status := int32(C.traverse_keychain_read(query, &data))
	if status == darwinItemNotFound {
		return "", false, nil
	}
	if err := darwinStatusError(status); err != nil {
		return "", false, err
	}
	defer C.CFRelease(C.CFTypeRef(data))
	size := C.CFDataGetLength(data)
	if size < 1 || size > MaxSecretBytes {
		return "", false, errors.New("stored credential has an invalid size")
	}
	bytes := C.CFDataGetBytePtr(data)
	if bytes == nil {
		return "", false, errors.New("stored credential has no data")
	}
	blob := C.GoBytes(unsafe.Pointer(bytes), C.int(size))
	defer clear(blob)
	value := string(blob)
	if !ValidSecret(value) {
		return "", false, errors.New("stored credential is invalid")
	}
	return value, true, nil
}

func (s darwinStore) Configured(ctx context.Context, name string) (bool, error) {
	// Metadata existence is not proof that a valid secret is readable. Keep
	// access denial and corruption distinct from an absent credential.
	_, found, err := s.Get(ctx, name)
	return found, err
}

type darwinKeychainError struct{ status int32 }

func (e darwinKeychainError) Error() string {
	message := "operation failed"
	switch e.status {
	case darwinUserCanceled:
		message = "operation was canceled"
	case darwinAuthFailed:
		message = "access was denied"
	case darwinInteraction, darwinInteractionNeed:
		message = "requires user authorization or an unlocked keychain"
	case int32(C.errSecNotAvailable), int32(C.errSecNoDefaultKeychain), int32(C.errSecNoSuchKeychain):
		message = "storage is unavailable"
	case darwinItemNotFound:
		message = "item was not found"
	case darwinDuplicateItem:
		message = "item already exists"
	case int32(C.errSecAllocate):
		message = "allocation failed"
	case int32(C.errSecDecode):
		message = "stored item is invalid"
	}
	// Only fixed text and the numerical status can cross the public error path.
	return fmt.Sprintf("macOS Keychain %s (OSStatus %d)", message, e.status)
}

func (e darwinKeychainError) Unwrap() error {
	switch e.status {
	case int32(C.errSecNotAvailable), int32(C.errSecNoDefaultKeychain), int32(C.errSecNoSuchKeychain):
		return ErrUnavailable
	default:
		return nil
	}
}

func darwinStatusError(status int32) error {
	if status == darwinSuccess {
		return nil
	}
	return darwinKeychainError{status: status}
}
