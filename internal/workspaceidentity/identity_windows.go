//go:build windows

package workspaceidentity

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func rootIdentity(root string, _ os.FileInfo) (string, error) {
	name, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return "", err
	}
	handle, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)
	var identity windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &identity); err != nil {
		return "", err
	}
	return fmt.Sprintf("%08x:%08x:%08x", identity.VolumeSerialNumber,
		identity.FileIndexHigh, identity.FileIndexLow), nil
}
