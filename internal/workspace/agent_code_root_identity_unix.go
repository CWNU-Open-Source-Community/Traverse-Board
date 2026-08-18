//go:build linux || darwin

package workspace

import (
	"fmt"
	"os"
	"syscall"
)

func agentCodeRootIdentity(_ string, info os.FileInfo) (string, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil {
		return "", fmt.Errorf("workspace root file identity is unavailable")
	}
	return fmt.Sprintf("%d:%d", uint64(stat.Dev), uint64(stat.Ino)), nil
}
