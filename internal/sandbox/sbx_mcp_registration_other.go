//go:build !windows

package sandbox

import (
	"os"
	"syscall"
)

func sbxMCPRegistrationPath(string) (string, error) {
	// The current stored-registration contract is verified only for Windows
	// sbx v0.47. No guessed Unix/XDG path grants production readiness.
	return "", ErrSBXBoundary
}

func sbxMCPRegistrationHandleValid(file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return ErrSBXBoundary
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || stat.Nlink != 1 {
		return ErrSBXBoundary
	}
	return nil
}
