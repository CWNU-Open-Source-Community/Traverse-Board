//go:build !windows

package packagede2e

import (
	"os/exec"
	"runtime"
)

func configurePackagedE2EProcess(*exec.Cmd) {}

func securityOSVersion() string { return runtime.GOOS + "-unknown" }
