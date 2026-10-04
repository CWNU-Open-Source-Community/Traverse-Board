//go:build !windows

package agentpackages

import (
	"io/fs"
	"os"
	"path/filepath"
)

func directoryCanonical(_ *os.Root, name string) (string, error) {
	resolved, err := filepath.EvalSymlinks(name)
	if err != nil {
		return "", err
	}
	return filepath.Abs(resolved)
}

func resolveDirectoryPath(_ *os.Root, canonical, local string) (string, error) {
	resolved, err := filepath.EvalSymlinks(filepath.Join(canonical, local))
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(canonical, resolved)
	if err != nil || !filepath.IsLocal(relative) {
		return "", &fs.PathError{Op: "resolve", Path: local, Err: fs.ErrPermission}
	}
	return relative, nil
}
