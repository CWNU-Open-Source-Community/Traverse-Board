//go:build unix

package plugins

import (
	"os"
	"syscall"
)

func openSnapshotFile(root *os.Root, name string) (*os.File, error) {
	// Do not block if a regular source is replaced with a FIFO before opening.
	return root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
