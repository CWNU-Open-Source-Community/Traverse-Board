//go:build windows

package repository

import (
	"os"
	"syscall"
)

func isReparsePoint(info os.FileInfo) bool {
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	return ok && data != nil && data.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0
}
