//go:build !windows

package runner

import "context"

type unavailableHostStarter struct{}

func newPlatformHostStarter() HostProcessStarter {
	return unavailableHostStarter{}
}

func (unavailableHostStarter) Name() string {
	return "host-unavailable"
}

func (unavailableHostStarter) Available() bool {
	return false
}

func (unavailableHostStarter) Start(
	context.Context,
	HostStartSpec,
) (HostStartResult, error) {
	return HostStartResult{}, ErrHostCommandPlatform
}
