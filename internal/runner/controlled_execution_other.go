//go:build !windows

package runner

import (
	"context"
)

type unavailableControlledStarter struct{}

func newPlatformControlledStarter() ControlledProcessStarter {
	return unavailableControlledStarter{}
}

func (unavailableControlledStarter) Name() string {
	return "controlled-unavailable"
}

func (unavailableControlledStarter) Available() bool {
	return false
}

func (unavailableControlledStarter) Start(context.Context,
	ControlledStartSpec,
) (ControlledStartResult, error) {
	return ControlledStartResult{}, ErrControlledExecutionPlatform
}
