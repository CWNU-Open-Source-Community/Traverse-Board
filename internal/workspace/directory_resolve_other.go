//go:build !windows

package workspace

import "path/filepath"

func resolveWorkspaceDirectory(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}
