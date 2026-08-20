//go:build windows

package application

import (
	"errors"

	"golang.org/x/sys/windows"
)

func uiEvidenceConnectionRefused(err error) bool {
	return errors.Is(err, windows.WSAECONNREFUSED)
}
