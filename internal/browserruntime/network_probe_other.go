//go:build !windows

package browserruntime

import (
	"context"
	"time"
)

func runPlatformBrowserNetworkContainmentProbe(_ context.Context,
	_ BrowserExecutableIdentity, request BrowserNetworkProbeRequest,
) BrowserNetworkProbeReport {
	startedAt := time.Now().UTC()
	return finishBrowserNetworkProbeReport(BrowserNetworkProbeReport{
		ID: request.ID, CollectorIdentity: request.CollectorIdentity,
		Adapter: DisabledBrowserContainmentAdapterName, StartedAt: startedAt,
	}, "windows_wfp_unavailable")
}
