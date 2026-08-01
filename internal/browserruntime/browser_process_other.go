//go:build !windows

package browserruntime

import "context"

type unavailableBrowserProcessStarter struct{}

func newPlatformBrowserProcessStarter() browserProcessStarter {
	return unavailableBrowserProcessStarter{}
}

func (unavailableBrowserProcessStarter) Name() string    { return "unavailable" }
func (unavailableBrowserProcessStarter) Available() bool { return false }
func (unavailableBrowserProcessStarter) Start(context.Context,
	BrowserStartSpec,
) (browserPlatformProcess, error) {
	return nil, ErrBrowserRuntimeUnavailable
}
