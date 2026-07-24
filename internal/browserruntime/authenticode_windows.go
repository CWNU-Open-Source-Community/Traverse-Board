//go:build windows

package browserruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const cmsgSignerCertInfoParam = 7

// cryptDataBlobAddress mirrors CRYPT_DATA_BLOB while keeping the mapped
// address as uintptr until Windows reads the structure.
type cryptDataBlobAddress struct {
	Size uint32
	Data uintptr
}

var (
	crypt32DLL       = windows.NewLazySystemDLL("crypt32.dll")
	cryptMsgGetParam = crypt32DLL.NewProc("CryptMsgGetParam")
	cryptMsgClose    = crypt32DLL.NewProc("CryptMsgClose")
)

func browserAuthenticodeEvidence(file *os.File, canonicalPath string) (AuthenticodeEvidence, error) {
	evidence := AuthenticodeEvidence{
		Source: AuthenticodeSourceWindows, SameOpenHandleVerified: true,
		CacheOnlyVerification: true, PublisherPolicyVersion: BrowserPublisherPolicyVersion,
	}
	if file == nil {
		return AuthenticodeEvidence{}, errors.New("browser Authenticode handle is required")
	}
	path, err := windows.UTF16PtrFromString(canonicalPath)
	if err != nil {
		return AuthenticodeEvidence{}, errors.New("browser Authenticode path is invalid")
	}
	fileInfo := windows.WinTrustFileInfo{
		Size:     uint32(unsafe.Sizeof(windows.WinTrustFileInfo{})),
		FilePath: path, File: windows.Handle(file.Fd()),
	}
	trustData := windows.WinTrustData{
		Size:                            uint32(unsafe.Sizeof(windows.WinTrustData{})),
		UIChoice:                        windows.WTD_UI_NONE,
		RevocationChecks:                windows.WTD_REVOKE_NONE,
		UnionChoice:                     windows.WTD_CHOICE_FILE,
		FileOrCatalogOrBlobOrSgnrOrCert: unsafe.Pointer(&fileInfo),
		StateAction:                     windows.WTD_STATEACTION_VERIFY,
		ProvFlags: windows.WTD_CACHE_ONLY_URL_RETRIEVAL |
			windows.WTD_REVOCATION_CHECK_NONE | windows.WTD_DISABLE_MD2_MD4,
		UIContext: windows.WTD_UICONTEXT_EXECUTE,
	}
	verifyErr := windows.WinVerifyTrustEx(windows.InvalidHWND,
		&windows.WINTRUST_ACTION_GENERIC_VERIFY_V2, &trustData)
	trustData.StateAction = windows.WTD_STATEACTION_CLOSE
	closeErr := windows.WinVerifyTrustEx(windows.InvalidHWND,
		&windows.WINTRUST_ACTION_GENERIC_VERIFY_V2, &trustData)
	runtime.KeepAlive(file)
	if closeErr != nil && verifyErr == nil {
		return AuthenticodeEvidence{}, fmt.Errorf("close browser Authenticode trust state: %w", closeErr)
	}
	if verifyErr != nil {
		return evidence, nil
	}

	publisher, certificateDigest, err := browserAuthenticodeSigner(file)
	if err != nil {
		return AuthenticodeEvidence{}, err
	}
	evidence.SignatureVerified = true
	evidence.Publisher = publisher
	evidence.CertificateSHA256 = certificateDigest
	return evidence, nil
}

func browserAuthenticodeSigner(file *os.File) (string, string, error) {
	if file == nil {
		return "", "", errors.New("browser signer handle is required")
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() ||
		info.Size() < MinBrowserExecutableBytes || info.Size() > MaxBrowserExecutableBytes ||
		info.Size() > int64(^uint32(0)) {
		return "", "", errors.New("browser signer handle size is invalid")
	}
	mapping, err := windows.CreateFileMapping(windows.Handle(file.Fd()), nil,
		windows.PAGE_READONLY, 0, 0, nil)
	if err != nil {
		return "", "", fmt.Errorf("map browser signer handle: %w", err)
	}
	defer windows.CloseHandle(mapping)
	address, err := windows.MapViewOfFile(mapping, windows.FILE_MAP_READ, 0, 0,
		uintptr(info.Size()))
	if err != nil {
		return "", "", fmt.Errorf("view browser signer handle: %w", err)
	}
	defer windows.UnmapViewOfFile(address)
	blob := cryptDataBlobAddress{
		Size: uint32(info.Size()),
		Data: address,
	}
	var encoding uint32
	var content uint32
	var format uint32
	var store windows.Handle
	var message windows.Handle
	if err := windows.CryptQueryObject(windows.CERT_QUERY_OBJECT_BLOB,
		unsafe.Pointer(&blob), windows.CERT_QUERY_CONTENT_FLAG_PKCS7_SIGNED_EMBED,
		windows.CERT_QUERY_FORMAT_FLAG_BINARY, 0, &encoding, &content, &format,
		&store, &message, nil); err != nil {
		return "", "", fmt.Errorf("query browser Authenticode signer: %w", err)
	}
	runtime.KeepAlive(file)
	defer windows.CertCloseStore(store, 0)
	defer cryptMsgClose.Call(uintptr(message))

	var size uint32
	if err := callCryptMsgGetParam(message, cmsgSignerCertInfoParam, nil, &size); err != nil ||
		size < uint32(unsafe.Sizeof(windows.CertInfo{})) || size > 1<<20 {
		return "", "", errors.New("browser signer certificate metadata is unavailable")
	}
	buffer := make([]byte, size)
	if err := callCryptMsgGetParam(message, cmsgSignerCertInfoParam,
		unsafe.Pointer(&buffer[0]), &size); err != nil {
		return "", "", err
	}
	signerInfo := (*windows.CertInfo)(unsafe.Pointer(&buffer[0]))
	certificate, err := windows.CertFindCertificateInStore(store, encoding, 0,
		windows.CERT_FIND_SUBJECT_CERT, unsafe.Pointer(signerInfo), nil)
	if err != nil {
		return "", "", fmt.Errorf("find browser signer certificate: %w", err)
	}
	defer windows.CertFreeCertificateContext(certificate)
	publisher, err := certificateDisplayName(certificate)
	if err != nil {
		return "", "", err
	}
	if certificate.EncodedCert == nil || certificate.Length == 0 || certificate.Length > 1<<20 {
		return "", "", errors.New("browser signer certificate bytes are invalid")
	}
	encoded := unsafe.Slice(certificate.EncodedCert, certificate.Length)
	digest := sha256.Sum256(encoded)
	return publisher, hex.EncodeToString(digest[:]), nil
}

func callCryptMsgGetParam(message windows.Handle, param uint32, output unsafe.Pointer,
	size *uint32,
) error {
	result, _, callErr := cryptMsgGetParam.Call(uintptr(message), uintptr(param), 0,
		uintptr(output), uintptr(unsafe.Pointer(size)))
	if result == 0 {
		if callErr != nil && callErr != windows.ERROR_SUCCESS {
			return fmt.Errorf("read browser signer metadata: %w", callErr)
		}
		return errors.New("read browser signer metadata failed")
	}
	return nil
}

func certificateDisplayName(certificate *windows.CertContext) (string, error) {
	size := windows.CertGetNameString(certificate, windows.CERT_NAME_SIMPLE_DISPLAY_TYPE,
		0, nil, nil, 0)
	if size < 2 || size > 256 {
		return "", errors.New("browser signer publisher name is invalid")
	}
	buffer := make([]uint16, size)
	if windows.CertGetNameString(certificate, windows.CERT_NAME_SIMPLE_DISPLAY_TYPE,
		0, nil, &buffer[0], size) != size {
		return "", errors.New("browser signer publisher name changed")
	}
	name := strings.TrimSpace(windows.UTF16ToString(buffer))
	if name == "" || len(name) > 128 || strings.ContainsAny(name, "\r\n\t") {
		return "", errors.New("browser signer publisher name is malformed")
	}
	return name, nil
}
