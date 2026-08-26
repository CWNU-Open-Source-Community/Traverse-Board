//go:build !windows

package sandbox

import (
	"context"
	"io"
	"net"
	"net/http"
	"time"
)

type localDockerContainerIOTransport struct {
	inner dockerEngineContainerIOTransport
}

func (transport localDockerContainerIOTransport) Endpoint() DockerObservationEndpoint {
	return transport.inner.Endpoint()
}

func (transport localDockerContainerIOTransport) AttachLogs(ctx context.Context,
	plan DockerLogCapturePlan,
) (io.ReadCloser, error) {
	return transport.inner.AttachLogs(ctx, plan)
}

func (transport localDockerContainerIOTransport) ExportOutputs(ctx context.Context,
	plan DockerOutputExportPlan,
) (io.ReadCloser, error) {
	return transport.inner.ExportOutputs(ctx, plan)
}

func (transport localDockerContainerIOTransport) AttachOwnedLogs(ctx context.Context,
	request DockerContainerLifecycleRequest, plan DockerLogCapturePlan,
) (io.ReadCloser, error) {
	return transport.inner.AttachOwnedLogs(ctx, request, plan)
}

func (transport localDockerContainerIOTransport) ExportOwnedOutputs(ctx context.Context,
	request DockerContainerLifecycleRequest, plan DockerOutputExportPlan,
) (io.ReadCloser, error) {
	return transport.inner.ExportOwnedOutputs(ctx, request, plan)
}

func (transport localDockerContainerIOTransport) AttachOwnedStdin(ctx context.Context,
	request DockerContainerLifecycleRequest, stdin io.ReadCloser,
	fence DockerContainerLifecycleFence,
) error {
	return transport.inner.AttachOwnedStdin(ctx, request, stdin, fence)
}

func (transport localDockerContainerIOTransport) SupportsOwnedStdin() bool {
	return transport.inner.SupportsOwnedStdin()
}

// NewLocalDockerContainerIOTransport returns the fixed local Docker I/O
// transport. The endpoint is not caller-configurable, proxy discovery is
// disabled, and redirects are rejected.
func NewLocalDockerContainerIOTransport() DockerContainerOwnedIOTransport {
	endpoint, _ := NewDockerObservationEndpoint(DockerObservationEndpointLocalUnix)
	dialer := &net.Dialer{Timeout: 2 * time.Second, KeepAlive: -1}
	httpTransport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", localDockerUnixSocket)
		},
		DisableKeepAlives: true,
	}
	client := &http.Client{
		Transport: httpTransport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	transport, err := newDockerEngineContainerIOTransport(client, endpoint)
	if err != nil {
		return unavailableDockerContainerIOTransport{endpoint: endpoint,
			reason: DockerContainerIOFailureUnavailable}
	}
	return localDockerContainerIOTransport{inner: transport}
}
