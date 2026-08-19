//go:build !windows && !linux && !darwin

package workspaceidentity

import (
	"fmt"
	"os"
)

func rootIdentity(_ string, _ os.FileInfo) (string, error) {
	return "", fmt.Errorf("workspace root file identity is unsupported on this platform")
}
