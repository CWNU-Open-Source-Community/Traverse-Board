//go:build windows

package sandbox

import (
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// Scratch is private runtime state, never a workspace artifact. The path is
// derived from the private journal and owner ID, not from a caller-supplied path.
// Its identity authorizes removal only; it cannot authorize another execution.
type localScratchIdentity struct {
	PathSHA256   string `json:"path_sha256"`
	RootIdentity string `json:"root_identity"`
}

const localScratchPrefix = "scratch-"

func (b *windowsLocalBackend) scratchPath(ownerID string) (string, error) {
	if b == nil || b.lock == 0 || !validLocalHostRoot(b.ownerRoot) || !validDigest(ownerID) {
		return "", ErrLocalSandboxBoundary
	}
	// Windows child creation is sensitive to the effective profile path length.
	// Keep 128 bits in the directory name; the full owner digest remains in the
	// journal and every existing directory is rejected by Mkdir, never reused.
	return filepath.Join(b.ownerRoot, localScratchPrefix+ownerID[:32]), nil
}

func (b *windowsLocalBackend) prepareScratch(ownerID string, prepared localPreparedRun) (localPinnedRoot, error) {
	// A portable owner directory must not become reachable through a writable
	// workspace or a read-only toolchain mount (including their ancestors).
	if localRootsOverlap(b.ownerRoot, prepared.drydock.path) {
		return localPinnedRoot{}, ErrLocalSandboxBoundary
	}
	for _, root := range prepared.toolchains {
		if localRootsOverlap(b.ownerRoot, root.path) {
			return localPinnedRoot{}, ErrLocalSandboxBoundary
		}
	}
	path, err := b.scratchPath(ownerID)
	if err != nil {
		return localPinnedRoot{}, err
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		return localPinnedRoot{}, err
	}
	root, err := pinLocalRoot(path)
	if err != nil {
		return root, errors.Join(err, os.Remove(path))
	}
	return root, nil
}

func prepareLocalScratchDirectories(root, profileName string) error {
	if !validLocalHostRoot(root) || !validLocalProfileName(profileName) {
		return ErrLocalSandboxBoundary
	}
	// Both environment-based and Windows known-folder AppData layouts live in
	// this one disposable boundary. No .traverse-board directory is created.
	for _, relative := range []string{"home", "tmp"} {
		if err := os.MkdirAll(filepath.Join(root, relative), 0o700); err != nil {
			return err
		}
	}
	for _, packages := range []string{filepath.Join("home", "Packages"), filepath.Join("home", "AppData", "Local", "Packages")} {
		for _, relative := range []string{"AC\\Temp", "AC\\INetCache", "LocalCache", "LocalState", "RoamingState", "Settings", "TempState"} {
			if err := os.MkdirAll(filepath.Join(root, packages, strings.ToLower(profileName), relative), 0o700); err != nil {
				return err
			}
		}
	}
	return nil
}

func (b *windowsLocalBackend) removeScratch(record localOwnerRecord) error {
	if record.Scratch == nil {
		return nil
	}
	path, err := b.scratchPath(record.OwnerID)
	if err != nil || localHostPathDigest(path) != record.Scratch.PathSHA256 {
		return ErrLocalSandboxBoundary
	}
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil // The previous cleanup may have removed it before journal commit.
	} else if err != nil {
		return err
	}
	root, err := pinLocalRoot(path)
	if err != nil {
		return err
	}
	if root.identity != record.Scratch.RootIdentity {
		root.close()
		return ErrLocalSandboxBoundary
	}
	err = validateLocalTree(root.path)
	root.close()
	if err != nil {
		return err
	}
	// The application lock, unique owner identity, and reaped Job bind removal.
	// Reparse points and replacement directories are rejected above.
	return os.RemoveAll(path)
}

func (b *windowsLocalBackend) recoverEmptyScratch(name string) error {
	ownerPrefix := strings.TrimPrefix(name, localScratchPrefix)
	_, decodeErr := hex.DecodeString(ownerPrefix)
	if !strings.HasPrefix(name, localScratchPrefix) || len(ownerPrefix) != 32 || decodeErr != nil {
		return ErrLocalSandboxBoundary
	}
	path := filepath.Join(b.ownerRoot, name)
	entries, err := os.ReadDir(b.ownerRoot)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		fullID := strings.TrimSuffix(entry.Name(), ".json")
		if strings.HasSuffix(entry.Name(), ".json") && validDigest(fullID) && strings.HasPrefix(fullID, ownerPrefix) {
			return nil // Only that validated, full-ID owner may remove its directory.
		}
	}
	// Allocation precedes journal sealing, but no files/ACLs/profile are created
	// until it commits. An unjournaled allocation is removable only when empty.
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.Join(err, ErrLocalSandboxBoundary)
	}
	root, err := pinLocalRoot(path)
	if err != nil {
		return err
	}
	root.close()
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	// RemoveDirectory cannot delete a same-name ordinary file even if it is
	// substituted after inspection, and it never recursively removes contents.
	err = windows.RemoveDirectory(pointer)
	if errors.Is(err, windows.ERROR_PATH_NOT_FOUND) || errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		return nil
	}
	return err
}
