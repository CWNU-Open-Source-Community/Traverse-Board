//go:build !windows

package mcp

import "path/filepath"

func canonicalLaunchPath(value string) (string, error) {
	return filepath.EvalSymlinks(value)
}
