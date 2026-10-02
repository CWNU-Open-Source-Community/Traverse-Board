//go:build unix

package agentpackages

import (
	"os"
	"syscall"
)

func openRootReadOnly(root *os.Root, name string) (*os.File, error) {
	// A concurrent replacement by a FIFO must not block before the caller can
	// inspect the opened handle and reject non-regular resource content.
	return root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
