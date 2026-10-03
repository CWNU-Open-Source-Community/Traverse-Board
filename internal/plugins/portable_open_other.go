//go:build !unix

package plugins

import "os"

func openSnapshotFile(root *os.Root, name string) (*os.File, error) { return root.Open(name) }
