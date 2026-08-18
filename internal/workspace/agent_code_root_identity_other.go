//go:build !windows && !linux && !darwin

package workspace

import (
	"fmt"
	"os"
)

func agentCodeRootIdentity(_ string, _ os.FileInfo) (string, error) {
	return "", fmt.Errorf("workspace root file identity is unsupported on this platform")
}
