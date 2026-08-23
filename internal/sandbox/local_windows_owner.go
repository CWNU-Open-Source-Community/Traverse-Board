//go:build windows

package sandbox

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	localOwnerProtocolVersion = "local_sandbox_owner.v1"
	localOwnerDirectoryName   = "local-sandbox-owners-v1"
	localOwnerLockName        = "owner.lock"
	localOwnerMaximumBytes    = 4 * 1024 * 1024
	localMaximumTreeEntries   = 250_000
	localProfilePrefix        = "TraverseBoard.Local."
	localCapabilityPrefix     = "TraverseBoard.Local.Filesystem."
	localFileDeleteChild      = 0x00000040
)

var (
	createAppContainerProfileProc = windows.NewLazySystemDLL("userenv.dll").NewProc(
		"CreateAppContainerProfile")
	deleteAppContainerProfileProc = windows.NewLazySystemDLL("userenv.dll").NewProc(
		"DeleteAppContainerProfile")
	getAppContainerFolderPathProc = windows.NewLazySystemDLL("userenv.dll").NewProc(
		"GetAppContainerFolderPath")
	deriveAppContainerSIDProc = windows.NewLazySystemDLL("userenv.dll").NewProc(
		"DeriveAppContainerSidFromAppContainerName")
	deriveCapabilitySIDsProc = windows.NewLazySystemDLL("kernelbase.dll").NewProc(
		"DeriveCapabilitySidsFromName")
)

type windowsLocalBackend struct {
	mu         sync.Mutex
	generation string
	ownerRoot  string
	lock       windows.Handle
	initErr    error
	closed     bool
	readiness  LocalReadiness
}

type localAppContainerProfile struct {
	name                      string
	sid                       *windows.SID
	filesystemCapabilitySID   *windows.SID
	registryReadCapabilitySID *windows.SID
}

type localPinnedRoot struct {
	path       string
	handle     windows.Handle
	identity   string
	filesystem string
}

type localSecuritySnapshot struct {
	Path          string `json:"path"`
	PathSHA256    string `json:"path_sha256"`
	RootIdentity  string `json:"root_identity"`
	DACLSDDL      string `json:"dacl_sddl"`
	DACLProtected bool   `json:"dacl_protected"`
	LabelSDDL     string `json:"label_sddl"`
	SACLProtected bool   `json:"sacl_protected"`
}

type localOwnerRecord struct {
	ProtocolVersion    string                  `json:"protocol_version"`
	OwnerID            string                  `json:"owner_id"`
	BindingFingerprint string                  `json:"binding_fingerprint"`
	ProfileName        string                  `json:"profile_name"`
	ProfileSID         string                  `json:"profile_sid"`
	Snapshots          []localSecuritySnapshot `json:"snapshots"`
	CreatedAt          time.Time               `json:"created_at"`
	Fingerprint        string                  `json:"fingerprint"`
}

func newPlatformLocalBackend(config localBackendConfig) (LocalBackend, error) {
	generation, err := newLocalRuntimeGeneration(
		"local-runtime-generation.v1", runtime.GOOS, runtime.GOARCH,
		LocalBackendPolicyVersion, windowsVersionFingerprint())
	if err != nil {
		return nil, err
	}
	backend := &windowsLocalBackend{generation: generation}
	if runtime.GOARCH != "amd64" {
		backend.initErr = errors.New("local sandbox requires Windows x64")
		return backend, nil
	}
	if err := findLocalWindowsAPIs(); err != nil {
		backend.initErr = err
		return backend, nil
	}
	root, err := resolveLocalOwnerRoot(config.ownerRoot)
	if err != nil {
		backend.initErr = err
		return backend, nil
	}
	backend.ownerRoot = root
	if err := os.MkdirAll(root, 0o700); err != nil {
		backend.initErr = fmt.Errorf("create Local Sandbox owner directory: %w", err)
		return backend, nil
	}
	if err := validateLocalTree(root); err != nil {
		backend.initErr = fmt.Errorf("validate Local Sandbox owner directory: %w", err)
		return backend, nil
	}
	lock, err := acquireLocalOwnerLock(filepath.Join(root, localOwnerLockName))
	if err != nil {
		backend.initErr = fmt.Errorf("acquire Local Sandbox owner lock: %w", err)
		return backend, nil
	}
	backend.lock = lock
	if err := backend.recoverOwnersLocked(); err != nil {
		backend.initErr = fmt.Errorf("recover Local Sandbox owners: %w", err)
	}
	return backend, nil
}

func (b *windowsLocalBackend) Generation() string {
	if b == nil {
		return ""
	}
	return b.generation
}

func (b *windowsLocalBackend) Close() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	err := b.recoverOwnersLocked()
	if b.lock != 0 {
		err = errors.Join(err, windows.CloseHandle(b.lock))
		b.lock = 0
	}
	return err
}

func findLocalWindowsAPIs() error {
	for name, proc := range map[string]*windows.LazyProc{
		"CreateAppContainerProfile":                 createAppContainerProfileProc,
		"DeleteAppContainerProfile":                 deleteAppContainerProfileProc,
		"GetAppContainerFolderPath":                 getAppContainerFolderPathProc,
		"DeriveAppContainerSidFromAppContainerName": deriveAppContainerSIDProc,
		"DeriveCapabilitySidsFromName":              deriveCapabilitySIDsProc,
	} {
		if err := proc.Find(); err != nil {
			return fmt.Errorf("%s is unavailable: %w", name, err)
		}
	}
	return nil
}

func resolveLocalOwnerRoot(override string) (string, error) {
	if override != "" {
		if !validLocalHostRoot(override) {
			return "", ErrLocalSandboxBoundary
		}
		return override, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	root := filepath.Clean(filepath.Join(base, "TraverseBoard", localOwnerDirectoryName))
	if !validLocalHostRoot(root) {
		return "", ErrLocalSandboxBoundary
	}
	return root, nil
}

func acquireLocalOwnerLock(path string) (windows.Handle, error) {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	handle, err := windows.CreateFile(pointer,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		0, nil, windows.OPEN_ALWAYS,
		windows.FILE_ATTRIBUTE_HIDDEN|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return 0, err
	}
	attributes, err := windows.GetFileAttributes(pointer)
	if err != nil || attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		_ = windows.CloseHandle(handle)
		return 0, ErrLocalSandboxBoundary
	}
	return handle, nil
}

func windowsVersionFingerprint() string {
	version := windows.RtlGetVersion()
	if version == nil {
		return "unknown"
	}
	return fmt.Sprintf("%d.%d.%d", version.MajorVersion, version.MinorVersion,
		version.BuildNumber)
}

func prepareLocalProfile(seed string) (localAppContainerProfile, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return localAppContainerProfile{}, err
	}
	digest := localFingerprint("local-profile.v1", seed, hex.EncodeToString(random))
	name := localProfilePrefix + digest[:32]
	sid, err := deriveLocalProfileSID(name)
	if err != nil {
		return localAppContainerProfile{}, err
	}
	filesystemCapabilitySID, err := deriveLocalCapabilitySID(localCapabilityName(name))
	if err != nil {
		return localAppContainerProfile{}, err
	}
	registryReadCapabilitySID, err := deriveLocalCapabilitySID("registryRead")
	if err != nil {
		return localAppContainerProfile{}, err
	}
	return localAppContainerProfile{name: name, sid: sid,
		filesystemCapabilitySID:   filesystemCapabilitySID,
		registryReadCapabilitySID: registryReadCapabilitySID}, nil
}

func materializeLocalProfile(profile localAppContainerProfile) error {
	if !validLocalProfileName(profile.name) || profile.sid == nil ||
		!profile.sid.IsValid() || profile.filesystemCapabilitySID == nil ||
		!profile.filesystemCapabilitySID.IsValid() ||
		profile.registryReadCapabilitySID == nil ||
		!profile.registryReadCapabilitySID.IsValid() {
		return ErrLocalSandboxBoundary
	}
	name := profile.name
	namePointer, _ := windows.UTF16PtrFromString(name)
	displayPointer, _ := windows.UTF16PtrFromString("Traverse Board Local Sandbox")
	descriptionPointer, _ := windows.UTF16PtrFromString(
		"Ephemeral LPAC profile with a run-scoped filesystem capability")
	capabilities := localProfileCapabilities(profile)
	var allocatedSID *windows.SID
	hresult, _, _ := createAppContainerProfileProc.Call(
		uintptr(unsafe.Pointer(namePointer)), uintptr(unsafe.Pointer(displayPointer)),
		uintptr(unsafe.Pointer(descriptionPointer)),
		uintptr(unsafe.Pointer(&capabilities[0])), uintptr(len(capabilities)),
		uintptr(unsafe.Pointer(&allocatedSID)))
	if int32(hresult) < 0 || allocatedSID == nil || !allocatedSID.IsValid() {
		return localHRESULT("create AppContainer profile", hresult)
	}
	sid, err := allocatedSID.Copy()
	_ = windows.FreeSid(allocatedSID)
	if err != nil {
		return err
	}
	if !sid.Equals(profile.sid) {
		return errors.New("AppContainer profile SID derivation mismatch")
	}
	if err := removeLocalProfileFilesystem(profile); err != nil {
		return err
	}
	runtime.KeepAlive(capabilities)
	runtime.KeepAlive(profile.filesystemCapabilitySID)
	runtime.KeepAlive(profile.registryReadCapabilitySID)
	return nil
}

func localAppContainerFolderPath(sid *windows.SID) (string, error) {
	if sid == nil || !sid.IsValid() {
		return "", ErrLocalSandboxBoundary
	}
	sidPointer, err := windows.UTF16PtrFromString(sid.String())
	if err != nil {
		return "", ErrLocalSandboxBoundary
	}
	var pathPointer *uint16
	hresult, _, _ := getAppContainerFolderPathProc.Call(
		uintptr(unsafe.Pointer(sidPointer)), uintptr(unsafe.Pointer(&pathPointer)))
	if int32(hresult) < 0 || pathPointer == nil {
		return "", localHRESULT("resolve AppContainer profile folder", hresult)
	}
	defer windows.CoTaskMemFree(unsafe.Pointer(pathPointer))
	value := filepath.Clean(windows.UTF16PtrToString(pathPointer))
	if !validLocalHostRoot(value) {
		return "", ErrLocalSandboxBoundary
	}
	return value, nil
}

func removeLocalProfileFilesystem(profile localAppContainerProfile) error {
	pathValue, err := localAppContainerFolderPath(profile.sid)
	if err != nil {
		return err
	}
	localAppData, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData,
		windows.KF_FLAG_DEFAULT)
	if err != nil {
		return ErrLocalSandboxBoundary
	}
	packagesRoot := filepath.Clean(filepath.Join(localAppData, "Packages"))
	validPackagesRoot := validLocalHostRoot(packagesRoot)
	withinPackages := pathValue != packagesRoot &&
		localHostPathWithin(pathValue, packagesRoot)
	relative, relativeErr := filepath.Rel(packagesRoot, pathValue)
	components := strings.Split(relative, string(os.PathSeparator))
	matchingName := relativeErr == nil && len(components) > 0 &&
		strings.EqualFold(components[0], profile.name)
	if !validPackagesRoot || !withinPackages || !matchingName {
		return fmt.Errorf("validate AppContainer profile folder (packages_root=%t descendant=%t profile_name=%t): %w",
			validPackagesRoot, withinPackages, matchingName, ErrLocalSandboxBoundary)
	}
	profileRoot := filepath.Clean(filepath.Join(packagesRoot, components[0]))
	if !validLocalHostRoot(profileRoot) || !localHostPathWithin(pathValue, profileRoot) {
		return ErrLocalSandboxBoundary
	}
	pinned, err := pinLocalRoot(profileRoot)
	if err != nil {
		return err
	}
	if err := validateLocalTree(pinned.path); err != nil {
		pinned.close()
		return err
	}
	pinned.close()
	if err := os.RemoveAll(profileRoot); err != nil {
		return err
	}
	if _, err := os.Lstat(profileRoot); !errors.Is(err, os.ErrNotExist) {
		return errors.Join(err, ErrLocalSandboxBoundary)
	}
	return nil
}

func localProfileCapabilities(profile localAppContainerProfile) []windows.SIDAndAttributes {
	return []windows.SIDAndAttributes{
		{Sid: profile.filesystemCapabilitySID, Attributes: windows.SE_GROUP_ENABLED},
		{Sid: profile.registryReadCapabilitySID, Attributes: windows.SE_GROUP_ENABLED},
	}
}

func localCapabilityName(profileName string) string {
	if !validLocalProfileName(profileName) {
		return ""
	}
	return localCapabilityPrefix + strings.TrimPrefix(profileName, localProfilePrefix)
}

func deriveLocalCapabilitySID(name string) (*windows.SID, error) {
	if name != "registryRead" && (!strings.HasPrefix(name, localCapabilityPrefix) ||
		len(name) != len(localCapabilityPrefix)+32) {
		return nil, ErrLocalSandboxBoundary
	}
	pointer, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, ErrLocalSandboxBoundary
	}
	var groupSIDs **windows.SID
	var groupCount uint32
	var capabilitySIDs **windows.SID
	var capabilityCount uint32
	success, _, callErr := deriveCapabilitySIDsProc.Call(
		uintptr(unsafe.Pointer(pointer)), uintptr(unsafe.Pointer(&groupSIDs)),
		uintptr(unsafe.Pointer(&groupCount)), uintptr(unsafe.Pointer(&capabilitySIDs)),
		uintptr(unsafe.Pointer(&capabilityCount)))
	defer freeLocalSIDArray(groupSIDs, groupCount)
	defer freeLocalSIDArray(capabilitySIDs, capabilityCount)
	if success == 0 {
		if callErr == nil {
			callErr = syscall.EINVAL
		}
		return nil, callErr
	}
	if capabilitySIDs == nil || capabilityCount != 1 || capabilityCount > 16 {
		return nil, ErrLocalSandboxBoundary
	}
	sid := unsafe.Slice(capabilitySIDs, capabilityCount)[0]
	if sid == nil || !sid.IsValid() {
		return nil, ErrLocalSandboxBoundary
	}
	return sid.Copy()
}

func freeLocalSIDArray(array **windows.SID, count uint32) {
	if array == nil {
		return
	}
	if count <= 16 {
		for _, sid := range unsafe.Slice(array, count) {
			if sid != nil {
				_, _ = windows.LocalFree(windows.Handle(unsafe.Pointer(sid)))
			}
		}
	}
	_, _ = windows.LocalFree(windows.Handle(unsafe.Pointer(array)))
}

func deriveLocalProfileSID(name string) (*windows.SID, error) {
	if !validLocalProfileName(name) {
		return nil, ErrLocalSandboxBoundary
	}
	pointer, _ := windows.UTF16PtrFromString(name)
	var allocatedSID *windows.SID
	hresult, _, _ := deriveAppContainerSIDProc.Call(
		uintptr(unsafe.Pointer(pointer)), uintptr(unsafe.Pointer(&allocatedSID)))
	if int32(hresult) < 0 || allocatedSID == nil || !allocatedSID.IsValid() {
		return nil, localHRESULT("derive AppContainer SID", hresult)
	}
	sid, err := allocatedSID.Copy()
	_ = windows.FreeSid(allocatedSID)
	return sid, err
}

func deleteLocalProfile(name string) error {
	if !validLocalProfileName(name) {
		return ErrLocalSandboxBoundary
	}
	pointer, _ := windows.UTF16PtrFromString(name)
	hresult, _, _ := deleteAppContainerProfileProc.Call(uintptr(unsafe.Pointer(pointer)))
	if int32(hresult) < 0 {
		// HRESULT_FROM_WIN32(ERROR_NOT_FOUND) is an idempotent recovery success.
		if uint32(hresult) == 0x80070490 {
			return nil
		}
		return localHRESULT("delete AppContainer profile", hresult)
	}
	return nil
}

func localHRESULT(operation string, value uintptr) error {
	if int32(value) >= 0 {
		return nil
	}
	code := syscall.Errno(uint32(value) & 0xffff)
	return fmt.Errorf("%s: HRESULT 0x%08x: %w", operation, uint32(value), code)
}

func validLocalProfileName(value string) bool {
	if !strings.HasPrefix(value, localProfilePrefix) || len(value) != len(localProfilePrefix)+32 {
		return false
	}
	_, err := hex.DecodeString(value[len(localProfilePrefix):])
	return err == nil
}

func pinLocalRoot(pathValue string) (localPinnedRoot, error) {
	if !validLocalHostRoot(pathValue) {
		return localPinnedRoot{}, ErrLocalSandboxBoundary
	}
	info, err := os.Lstat(pathValue)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return localPinnedRoot{}, ErrLocalSandboxBoundary
	}
	pointer, _ := windows.UTF16PtrFromString(pathValue)
	handle, err := windows.CreateFile(pointer,
		windows.FILE_READ_ATTRIBUTES|windows.READ_CONTROL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return localPinnedRoot{}, err
	}
	fail := func(cause error) (localPinnedRoot, error) {
		_ = windows.CloseHandle(handle)
		return localPinnedRoot{}, cause
	}
	attributes, err := windows.GetFileAttributes(pointer)
	if err != nil || attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fail(ErrLocalSandboxBoundary)
	}
	finalPath, err := localFinalPath(handle)
	if err != nil || !strings.EqualFold(finalPath, pathValue) {
		return fail(ErrLocalSandboxBoundary)
	}
	var fileInfo windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &fileInfo); err != nil {
		return fail(err)
	}
	filesystem, err := localFilesystemName(handle)
	if err != nil || (filesystem != "NTFS" && filesystem != "ReFS") {
		return fail(fmt.Errorf("%w: Local Sandbox requires NTFS or ReFS", ErrLocalSandboxBoundary))
	}
	identity := localFingerprint("local-root-identity.v1",
		fmt.Sprint(fileInfo.VolumeSerialNumber), fmt.Sprint(fileInfo.FileIndexHigh),
		fmt.Sprint(fileInfo.FileIndexLow))
	return localPinnedRoot{path: pathValue, handle: handle,
		identity: identity, filesystem: filesystem}, nil
}

func (r *localPinnedRoot) close() {
	if r != nil && r.handle != 0 {
		_ = windows.CloseHandle(r.handle)
		r.handle = 0
	}
}

func localFinalPath(handle windows.Handle) (string, error) {
	buffer := make([]uint16, 512)
	for {
		length, err := windows.GetFinalPathNameByHandle(handle, &buffer[0],
			uint32(len(buffer)), 0)
		if err != nil {
			return "", err
		}
		if int(length) < len(buffer) {
			value := windows.UTF16ToString(buffer[:length])
			value = strings.TrimPrefix(value, `\\?\`)
			return filepath.Clean(value), nil
		}
		buffer = make([]uint16, int(length)+1)
	}
}

func localFilesystemName(handle windows.Handle) (string, error) {
	filesystem := make([]uint16, 32)
	if err := windows.GetVolumeInformationByHandle(handle, nil, 0, nil, nil, nil,
		&filesystem[0], uint32(len(filesystem))); err != nil {
		return "", err
	}
	return windows.UTF16ToString(filesystem), nil
}

func validateLocalTree(root string) error {
	return validateLocalTreeLinks(root, true)
}

func validateLocalTreeLinks(root string, rejectHardlinks bool) error {
	count := 0
	return filepath.WalkDir(root, func(pathValue string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		count++
		if count > localMaximumTreeEntries {
			return fmt.Errorf("%w: Local Sandbox tree exceeds %d entries",
				ErrLocalSandboxBoundary, localMaximumTreeEntries)
		}
		info, err := entry.Info()
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return ErrLocalSandboxBoundary
		}
		pointer, err := windows.UTF16PtrFromString(pathValue)
		if err != nil {
			return ErrLocalSandboxBoundary
		}
		attributes, err := windows.GetFileAttributes(pointer)
		if err != nil || attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return ErrLocalSandboxBoundary
		}
		if rejectHardlinks && info.Mode().IsRegular() {
			links, err := localFileLinkCount(pathValue)
			if err != nil || links != 1 {
				return ErrLocalSandboxBoundary
			}
		}
		return nil
	})
}

func localFileLinkCount(pathValue string) (uint32, error) {
	pointer, err := windows.UTF16PtrFromString(pathValue)
	if err != nil {
		return 0, err
	}
	handle, err := windows.CreateFile(pointer, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(handle)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return 0, err
	}
	return info.NumberOfLinks, nil
}

func localRootsOverlap(first, second string) bool {
	first = strings.TrimSuffix(strings.ToLower(filepath.Clean(first)), string(os.PathSeparator))
	second = strings.TrimSuffix(strings.ToLower(filepath.Clean(second)), string(os.PathSeparator))
	return first == second || strings.HasPrefix(first, second+string(os.PathSeparator)) ||
		strings.HasPrefix(second, first+string(os.PathSeparator))
}

func captureLocalSecurity(root localPinnedRoot) (localSecuritySnapshot, error) {
	daclSD, err := windows.GetNamedSecurityInfo(root.path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION)
	if err != nil || daclSD == nil {
		return localSecuritySnapshot{}, errors.Join(err, ErrLocalSandboxBoundary)
	}
	control, _, err := daclSD.Control()
	if err != nil {
		return localSecuritySnapshot{}, err
	}
	labelSD, labelErr := windows.GetNamedSecurityInfo(root.path, windows.SE_FILE_OBJECT,
		windows.LABEL_SECURITY_INFORMATION)
	label := ""
	saclProtected := false
	if labelErr == nil && labelSD != nil {
		// LABEL_SECURITY_INFORMATION can return an empty SACL after a prior
		// label removal. Canonicalize that state with ERROR_OBJECT_NOT_FOUND:
		// both mean that no mandatory-integrity ACE exists.
		candidate := labelSD.String()
		labelControl, _, controlErr := labelSD.Control()
		if controlErr != nil {
			return localSecuritySnapshot{}, controlErr
		}
		saclProtected = labelControl&windows.SE_SACL_PROTECTED != 0
		if strings.Contains(candidate, "(ML;") {
			label = candidate
		}
	} else if labelErr != nil && !errors.Is(labelErr, windows.ERROR_OBJECT_NOT_FOUND) {
		return localSecuritySnapshot{}, labelErr
	}
	value := localSecuritySnapshot{Path: root.path,
		PathSHA256: localHostPathDigest(root.path), RootIdentity: root.identity,
		DACLSDDL: daclSD.String(), DACLProtected: control&windows.SE_DACL_PROTECTED != 0,
		LabelSDDL: label, SACLProtected: saclProtected}
	if value.DACLSDDL == "" {
		return localSecuritySnapshot{}, ErrLocalSandboxBoundary
	}
	return value, nil
}

func grantLocalRoot(snapshot localSecuritySnapshot, sid *windows.SID,
	writable, lowIntegrity bool,
) error {
	if sid == nil || !sid.IsValid() || validateLocalSnapshot(snapshot) != nil {
		return ErrLocalSandboxBoundary
	}
	sd, err := windows.SecurityDescriptorFromString(snapshot.DACLSDDL)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	access := windows.ACCESS_MASK(windows.FILE_GENERIC_READ | windows.FILE_GENERIC_EXECUTE)
	if writable {
		access |= windows.ACCESS_MASK(windows.FILE_GENERIC_WRITE | windows.DELETE |
			localFileDeleteChild)
	}
	entry := windows.EXPLICIT_ACCESS{AccessPermissions: access,
		AccessMode:  windows.GRANT_ACCESS,
		Inheritance: windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		Trustee: windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(sid)}}
	newACL, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{entry}, dacl)
	if err != nil {
		return err
	}
	information := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION |
		windows.UNPROTECTED_DACL_SECURITY_INFORMATION,
	)
	if snapshot.DACLProtected {
		information = windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION |
			windows.PROTECTED_DACL_SECURITY_INFORMATION)
	}
	if err := windows.SetNamedSecurityInfo(snapshot.Path, windows.SE_FILE_OBJECT,
		information, nil, nil, newACL, nil); err != nil {
		return err
	}
	if lowIntegrity {
		labelSD, err := windows.SecurityDescriptorFromString("S:(ML;OICI;NW;;;LW)")
		if err != nil {
			return err
		}
		labelACL, _, err := labelSD.SACL()
		if err != nil {
			return err
		}
		if err := windows.SetNamedSecurityInfo(snapshot.Path, windows.SE_FILE_OBJECT,
			windows.LABEL_SECURITY_INFORMATION, nil, nil, nil, labelACL); err != nil {
			return err
		}
	}
	runtime.KeepAlive(sid)
	return nil
}

func restoreLocalSecurity(snapshot localSecuritySnapshot) error {
	if err := validateLocalSnapshot(snapshot); err != nil {
		return err
	}
	pinned, err := pinLocalRoot(snapshot.Path)
	if err != nil {
		return err
	}
	defer pinned.close()
	if pinned.identity != snapshot.RootIdentity {
		return ErrLocalSandboxBoundary
	}
	sd, err := windows.SecurityDescriptorFromString(snapshot.DACLSDDL)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	information := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION |
		windows.UNPROTECTED_DACL_SECURITY_INFORMATION,
	)
	if snapshot.DACLProtected {
		information = windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION |
			windows.PROTECTED_DACL_SECURITY_INFORMATION)
	}
	if err := windows.SetNamedSecurityInfo(snapshot.Path, windows.SE_FILE_OBJECT,
		information, nil, nil, dacl, nil); err != nil {
		return err
	}
	labelSDDL := snapshot.LabelSDDL
	if labelSDDL == "" {
		emptyLabel, err := windows.SecurityDescriptorFromString("S:")
		if err != nil {
			return err
		}
		emptyACL, _, err := emptyLabel.SACL()
		if err != nil {
			return err
		}
		return windows.SetNamedSecurityInfo(snapshot.Path, windows.SE_FILE_OBJECT,
			windows.LABEL_SECURITY_INFORMATION, nil, nil, nil, emptyACL)
	}
	labelSD, err := windows.SecurityDescriptorFromString(labelSDDL)
	if err != nil {
		return err
	}
	labelACL, _, err := labelSD.SACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(snapshot.Path, windows.SE_FILE_OBJECT,
		windows.LABEL_SECURITY_INFORMATION, nil, nil, nil, labelACL)
}

func validateLocalSnapshot(snapshot localSecuritySnapshot) error {
	if !validLocalHostRoot(snapshot.Path) ||
		snapshot.PathSHA256 != localHostPathDigest(snapshot.Path) ||
		!validDigest(snapshot.RootIdentity) || snapshot.DACLSDDL == "" ||
		snapshot.SACLProtected {
		return ErrLocalSandboxBoundary
	}
	if _, err := windows.SecurityDescriptorFromString(snapshot.DACLSDDL); err != nil {
		return ErrLocalSandboxBoundary
	}
	if snapshot.LabelSDDL != "" {
		if _, err := windows.SecurityDescriptorFromString(snapshot.LabelSDDL); err != nil {
			return ErrLocalSandboxBoundary
		}
	}
	return nil
}

func (r *localOwnerRecord) seal() {
	r.Fingerprint = localOwnerFingerprint(*r)
}

func (r localOwnerRecord) validate() error {
	if r.ProtocolVersion != localOwnerProtocolVersion || !validDigest(r.OwnerID) ||
		!validDigest(r.BindingFingerprint) || !validLocalProfileName(r.ProfileName) ||
		r.ProfileSID == "" || r.CreatedAt.IsZero() || len(r.Snapshots) == 0 ||
		len(r.Snapshots) > MaxLocalToolchainInputs+1 || r.Fingerprint != localOwnerFingerprint(r) {
		return ErrLocalSandboxBoundary
	}
	sid, err := windows.StringToSid(r.ProfileSID)
	if err != nil || sid == nil || !sid.IsValid() {
		return ErrLocalSandboxBoundary
	}
	derivedSID, err := deriveLocalProfileSID(r.ProfileName)
	if err != nil || derivedSID == nil || !derivedSID.IsValid() ||
		!sid.Equals(derivedSID) {
		return ErrLocalSandboxBoundary
	}
	seen := map[string]struct{}{}
	for _, snapshot := range r.Snapshots {
		if err := validateLocalSnapshot(snapshot); err != nil {
			return err
		}
		key := strings.ToLower(snapshot.Path)
		if _, exists := seen[key]; exists {
			return ErrLocalSandboxBoundary
		}
		seen[key] = struct{}{}
	}
	return nil
}

func localOwnerFingerprint(record localOwnerRecord) string {
	parts := []string{localOwnerProtocolVersion, record.OwnerID,
		record.BindingFingerprint, record.ProfileName, record.ProfileSID,
		record.CreatedAt.UTC().Format(time.RFC3339Nano)}
	for _, snapshot := range record.Snapshots {
		parts = append(parts, snapshot.PathSHA256, snapshot.RootIdentity,
			snapshot.DACLSDDL, fmt.Sprint(snapshot.DACLProtected),
			snapshot.LabelSDDL, fmt.Sprint(snapshot.SACLProtected))
	}
	return localFingerprint(parts...)
}

func (b *windowsLocalBackend) writeOwnerLocked(record localOwnerRecord) error {
	if b.lock == 0 || b.ownerRoot == "" || record.validate() != nil {
		return ErrLocalSandboxBoundary
	}
	payload, err := json.Marshal(record)
	if err != nil || len(payload) > localOwnerMaximumBytes {
		return ErrLocalSandboxBoundary
	}
	temporary := filepath.Join(b.ownerRoot, record.OwnerID+".json.tmp")
	final := filepath.Join(b.ownerRoot, record.OwnerID+".json")
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(payload)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	return os.Rename(temporary, final)
}

func (b *windowsLocalBackend) removeOwnerLocked(ownerID string) error {
	if !validDigest(ownerID) || b.ownerRoot == "" {
		return ErrLocalSandboxBoundary
	}
	err := os.Remove(filepath.Join(b.ownerRoot, ownerID+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (b *windowsLocalBackend) recoverOwnersLocked() error {
	if b == nil || b.lock == 0 || b.ownerRoot == "" {
		return nil
	}
	entries, err := os.ReadDir(b.ownerRoot)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	var recoveryErr error
	for _, entry := range entries {
		name := entry.Name()
		if name == localOwnerLockName {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() {
			recoveryErr = errors.Join(recoveryErr, ErrLocalSandboxBoundary)
			continue
		}
		if strings.HasSuffix(name, ".json.tmp") {
			if removeErr := os.Remove(filepath.Join(b.ownerRoot, name)); removeErr != nil {
				recoveryErr = errors.Join(recoveryErr, removeErr)
			}
			continue
		}
		if !strings.HasSuffix(name, ".json") || len(name) != 64+len(".json") {
			recoveryErr = errors.Join(recoveryErr, ErrLocalSandboxBoundary)
			continue
		}
		pathValue := filepath.Join(b.ownerRoot, name)
		payload, readErr := os.ReadFile(pathValue)
		if readErr != nil || len(payload) == 0 || len(payload) > localOwnerMaximumBytes {
			recoveryErr = errors.Join(recoveryErr, readErr, ErrLocalSandboxBoundary)
			continue
		}
		decoder := json.NewDecoder(bytes.NewReader(payload))
		decoder.DisallowUnknownFields()
		var record localOwnerRecord
		if decodeErr := decoder.Decode(&record); decodeErr != nil ||
			record.OwnerID+".json" != name || record.validate() != nil {
			recoveryErr = errors.Join(recoveryErr, decodeErr, ErrLocalSandboxBoundary)
			continue
		}
		var ownerErr error
		for index := len(record.Snapshots) - 1; index >= 0; index-- {
			ownerErr = errors.Join(ownerErr, restoreLocalSecurity(record.Snapshots[index]))
		}
		ownerErr = errors.Join(ownerErr, removeLocalAppContainerDirectory(
			record.Snapshots[0].Path, record.ProfileName))
		ownerErr = errors.Join(ownerErr, deleteLocalProfile(record.ProfileName))
		if ownerErr == nil {
			ownerErr = os.Remove(pathValue)
		}
		recoveryErr = errors.Join(recoveryErr, ownerErr)
	}
	return recoveryErr
}
