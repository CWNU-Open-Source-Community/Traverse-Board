package workspaceidentity

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
)

// Fingerprint binds a canonical real directory path to its platform file
// identity. Callers remain responsible for rejecting redirected roots before
// invoking it; this package deliberately has no dependency on Workspace policy.
func Fingerprint(root string) (string, error) {
	root = filepath.Clean(root)
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("workspace root identity is unavailable")
	}
	identity, err := rootIdentity(root, info)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(filepath.ToSlash(root) + "\x00" + identity))
	return hex.EncodeToString(sum[:]), nil
}
