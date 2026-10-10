//go:build windows

package sandbox

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func sbxMCPRegistrationPath(name string) (string, error) {
	if !sbxMCPHelperName(name) {
		return "", ErrSBXBoundary
	}
	// Use the actual process token's profile, not USERPROFILE/LOCALAPPDATA or
	// renderer-supplied settings. Redirected or unverified stores fail closed.
	profile, err := windows.GetCurrentProcessToken().GetUserProfileDirectory()
	if err != nil {
		return "", ErrSBXBoundary
	}
	if _, err := sbxCanonical(profile, true); err != nil {
		return "", ErrSBXBoundary
	}
	return filepath.Join(profile, "AppData", "Local", "DockerSandboxes", "sandboxes-"+SBXAppName,
		"state", "sandboxd", "mcp", "servers", name+".json"), nil
}

func sbxMCPRegistrationHandleValid(file *os.File) error {
	var info windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info) != nil ||
		info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.NumberOfLinks != 1 {
		return ErrSBXBoundary
	}
	return nil
}
