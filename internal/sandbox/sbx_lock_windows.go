//go:build windows

package sandbox

import (
	"golang.org/x/sys/windows"
	"os"
)

func sbxLock(file *os.File) error {
	return windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
}
func sbxSyncJournalDirectory(string) error { return nil }
