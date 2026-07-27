//go:build !windows

package terminal

import "context"

type unavailableHostBoundarySource struct{}

func newPlatformHostBoundarySource() HostBoundarySource {
	return unavailableHostBoundarySource{}
}

func (unavailableHostBoundarySource) Run(context.Context,
	func(HostBoundaryEvent),
	func(error),
) error {
	return ErrHostBoundaryMonitor
}
