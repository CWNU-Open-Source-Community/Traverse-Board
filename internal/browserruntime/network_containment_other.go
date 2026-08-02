//go:build !windows

package browserruntime

type disabledBrowserNetworkContainmentFactory struct{}

func newPlatformBrowserNetworkContainmentFactory() browserNetworkContainmentFactory {
	return disabledBrowserNetworkContainmentFactory{}
}

func (disabledBrowserNetworkContainmentFactory) Name() string {
	return DisabledBrowserContainmentAdapterName
}
func (disabledBrowserNetworkContainmentFactory) Available() bool { return false }
func (disabledBrowserNetworkContainmentFactory) Prepare(
	BrowserNetworkContainmentPlan,
) (browserNetworkContainmentGuard, error) {
	return nil, ErrBrowserRuntimeUnavailable
}
