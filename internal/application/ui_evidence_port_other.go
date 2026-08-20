//go:build !windows

package application

import (
	"errors"
	"syscall"
)

func uiEvidenceConnectionRefused(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED)
}
