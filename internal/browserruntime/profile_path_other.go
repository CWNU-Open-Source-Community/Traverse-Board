//go:build !windows

package browserruntime

import "os"

func platformProfilePathDirect(string) bool { return true }

func platformReplaceProfileMarker(source string, destination string) error {
	return os.Rename(source, destination)
}
