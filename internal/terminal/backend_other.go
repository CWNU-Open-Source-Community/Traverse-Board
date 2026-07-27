//go:build !windows

package terminal

import (
	"context"
)

type unavailableBackend struct{}

func newPlatformBackend() Backend {
	return unavailableBackend{}
}

func (unavailableBackend) Name() string {
	return "conpty-unavailable"
}

func (unavailableBackend) Available() bool {
	return false
}

func (unavailableBackend) Start(context.Context,
	BackendStartRequest,
) (Process, error) {
	return nil, ErrTerminalUnavailable
}
