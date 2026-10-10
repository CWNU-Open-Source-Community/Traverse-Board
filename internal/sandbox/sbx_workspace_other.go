//go:build !windows

package sandbox

import (
	"os"
	"syscall"
)

func sbxValidateWorkspaceEntry(_ string, info os.FileInfo) error {
	if info.Mode().IsRegular() {
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Nlink != 1 {
			return ErrSBXBoundary
		}
	}
	return nil
}
