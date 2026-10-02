//go:build !unix

package agentpackages

import "os"

func openRootReadOnly(root *os.Root, name string) (*os.File, error) { return root.Open(name) }
