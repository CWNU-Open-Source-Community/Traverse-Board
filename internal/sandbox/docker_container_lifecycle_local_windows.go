//go:build windows

package sandbox

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/Microsoft/go-winio"
)

const dockerDesktopLinuxEnginePipe = `\\.\pipe\dockerDesktopLinuxEngine`

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

func (transport localDockerContainerLifecycleTransport) StageOwned(ctx context.Context,
	request DockerContainerWriteRequest, ownership DockerContainerLifecycleOwnership,
	fence DockerContainerLifecycleFence,
) (DockerContainerStageResult, error) {
	return transport.inner.StageOwned(ctx, request, ownership, fence)
}

func (transport localDockerContainerLifecycleTransport) Observe(ctx context.Context,
	request DockerContainerLifecycleRequest,
) (DockerContainerLifecycleObservation, error) {
	return transport.inner.Observe(ctx, request)
}

func (transport localDockerContainerLifecycleTransport) Start(ctx context.Context,
	request DockerContainerLifecycleRequest, fence DockerContainerLifecycleFence,
) (DockerContainerLifecycleObservation, bool, error) {
	return transport.inner.Start(ctx, request, fence)
}

func (transport localDockerContainerLifecycleTransport) Wait(ctx context.Context,
	request DockerContainerLifecycleRequest, fence DockerContainerLifecycleFence,
) (DockerContainerLifecycleObservation, error) {
	return transport.inner.Wait(ctx, request, fence)
}

func (transport localDockerContainerLifecycleTransport) Terminate(ctx context.Context,
	request DockerContainerLifecycleRequest, fence DockerContainerLifecycleFence,
) (DockerContainerLifecycleTerminationResult, error) {
	return transport.inner.Terminate(ctx, request, fence)
}

func (transport localDockerContainerLifecycleTransport) Cleanup(ctx context.Context,
	request DockerContainerLifecycleRequest, fence DockerContainerLifecycleFence,
) (DockerContainerLifecycleCleanupResult, error) {
	return transport.inner.Cleanup(ctx, request, fence)
}

func (transport localDockerContainerLifecycleTransport) Run(ctx context.Context,
	request DockerContainerLifecycleRequest,
) (DockerContainerLifecycleResult, error) {
	return transport.inner.Run(ctx, request)
}

func newLocalDockerContainerLifecycleTransport() DockerContainerLifecycleTransport {
	endpoint, _ := NewDockerObservationEndpoint(DockerObservationEndpointLocalNPipe)
	httpTransport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			dialCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			return winio.DialPipeContext(dialCtx, dockerDesktopLinuxEnginePipe)
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
			DockerObservationEndpointLocalNPipe, DockerContainerLifecycleFailureUnsupported)
	}
	return localDockerContainerLifecycleTransport{inner: transport}
}
