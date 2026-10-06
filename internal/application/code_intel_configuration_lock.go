package application

import (
	"errors"
	"os"

	"cyberagent-workbench/internal/apperror"
)

// This persistent, empty sidecar carries only an OS advisory lock. Closing the
// owned handle (including process exit) releases it; no PID or stale lockfile
// needs to be adopted, guessed, or removed during restart recovery.
func acquireCodeIntelConfigurationLock(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	var file *os.File
	if errors.Is(err, os.ErrNotExist) {
		file, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	} else if err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 && info.Size() == 0 {
		file, err = os.OpenFile(path, os.O_RDWR, 0)
	} else {
		return nil, apperror.New(apperror.CodeFailedPrecondition, "managed LSP publication lock is unavailable or redirected")
	}
	if err != nil {
		return nil, apperror.New(apperror.CodeConflict, "managed LSP configuration publication is busy")
	}
	opened, statErr := file.Stat()
	current, pathErr := os.Lstat(path)
	if statErr != nil || pathErr != nil || !opened.Mode().IsRegular() || opened.Size() != 0 || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(opened, current) || (info != nil && !os.SameFile(info, opened)) {
		_ = file.Close()
		return nil, apperror.New(apperror.CodeFailedPrecondition, "managed LSP publication lock changed while it was opened")
	}
	if err := lockCodeIntelConfigurationFile(file); err != nil {
		_ = file.Close()
		return nil, apperror.New(apperror.CodeConflict, "managed LSP configuration publication is busy")
	}
	return file, nil
}
