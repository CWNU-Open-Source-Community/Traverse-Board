//go:build !windows

package sandbox

import (
	"context"
	"net"
	"net/http"
	"time"
)

type localDockerContainerLifecycleTransport struct {
	inner dockerEngineContainerLifecycleTransport
}

func (transport localDockerContainerLifecycleTransport) Endpoint() DockerObservationEndpoint {
	return transport.inner.Endpoint()
}

func (transport localDockerContainerLifecycleTransport) Stage(ctx context.Context,
	request DockerContainerWriteRequest,
) (DockerContainerStageResult, error) {
	return transport.inner.Stage(ctx, request)
}

func (transport localDockerContainerLifecycleTransport) Run(ctx context.Context,
	request DockerContainerLifecycleRequest,
) (DockerContainerLifecycleResult, error) {
	return transport.inner.Run(ctx, request)
}

func newLocalDockerContainerLifecycleTransport() DockerContainerLifecycleTransport {
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
	transport, err := newDockerEngineContainerLifecycleTransport(client, endpoint)
	if err != nil {
		return NewUnavailableDockerContainerLifecycleTransport(
			DockerObservationEndpointLocalUnix, DockerContainerLifecycleFailureUnsupported)
	}
	return localDockerContainerLifecycleTransport{inner: transport}
}
