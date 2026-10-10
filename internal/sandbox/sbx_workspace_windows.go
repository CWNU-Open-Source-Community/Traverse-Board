//go:build windows

package sandbox

import (
	"os"

	"golang.org/x/sys/windows"
)

func sbxValidateWorkspaceEntry(path string, info os.FileInfo) error {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	attributes, err := windows.GetFileAttributes(pointer)
	if err != nil {
		return err
	}
	if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return ErrSBXBoundary
	}
	if info.Mode().IsRegular() {
		links, err := localFileLinkCount(path)
		if err != nil {
			return err
		}
		if links != 1 {
			return ErrSBXBoundary
		}
	}
	return nil
}
