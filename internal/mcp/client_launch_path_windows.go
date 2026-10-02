//go:build windows

package mcp

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// EvalSymlinks does not resolve Windows junctions on current Go versions.
// Resolve the opened object, including directory junctions and reparse points;
// this is path normalization, not an execution authorization or pinned launch.
func canonicalLaunchPath(value string) (string, error) {
	file, err := os.Open(value)
	if err != nil {
		return "", err
	}
	defer file.Close()
	for size := 512; size <= 32768; {
		buffer := make([]uint16, size)
		length, err := windows.GetFinalPathNameByHandle(windows.Handle(file.Fd()), &buffer[0], uint32(len(buffer)), 0)
		if err != nil {
			return "", err
		}
		if length < uint32(len(buffer)) {
			name := windows.UTF16ToString(buffer[:length])
			if strings.HasPrefix(name, `\\?\UNC\`) {
				name = `\\` + strings.TrimPrefix(name, `\\?\UNC\`)
			} else {
				name = strings.TrimPrefix(name, `\\?\`)
			}
			return filepath.Clean(name), nil
		}
		size = int(length) + 1
	}
	return "", fs.ErrInvalid
}
