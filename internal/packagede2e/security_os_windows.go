//go:build windows

package packagede2e

import (
	"fmt"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

func configurePackagedE2EProcess(command *exec.Cmd) {
	if command != nil {
		command.SysProcAttr = &syscall.SysProcAttr{
			CreationFlags: windows.CREATE_NO_WINDOW,
			HideWindow:    true,
		}
	}
}

func securityOSVersion() string {
	version := windows.RtlGetVersion()
	if version == nil {
		return "windows-unknown"
	}
	return fmt.Sprintf("%d.%d.%d", version.MajorVersion, version.MinorVersion,
		version.BuildNumber)
}
