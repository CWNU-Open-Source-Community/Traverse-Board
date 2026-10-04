//go:build windows

package workspace

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// EvalSymlinks does not resolve Windows junctions. Resolve the opened directory
// before registering its path; this does not grant execution authority.
func resolveWorkspaceDirectory(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fs.ErrInvalid
	}
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
