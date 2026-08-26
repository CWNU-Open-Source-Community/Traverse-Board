//go:build windows

package packagede2e

import (
	"os/exec"
	"testing"

	"golang.org/x/sys/windows"
)

func TestConfigurePackagedE2EProcessHidesWindowsConsole(t *testing.T) {
	command := exec.Command("cmd.exe", "/c", "exit", "0")
	configurePackagedE2EProcess(command)
	if command.SysProcAttr == nil || !command.SysProcAttr.HideWindow ||
		command.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatalf("packaged subprocess can create a visible console: %+v", command.SysProcAttr)
	}
}
