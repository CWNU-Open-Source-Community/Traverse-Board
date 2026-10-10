//go:build !windows

package sandbox

import (
	"golang.org/x/sys/unix"
	"os"
)

func sbxLock(file *os.File) error { return unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB) }
func sbxSyncJournalDirectory(value string) error {
	file, err := os.Open(value)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}
