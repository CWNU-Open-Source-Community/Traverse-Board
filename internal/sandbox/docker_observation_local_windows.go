//go:build windows

package sandbox

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/Microsoft/go-winio"
)

func NewLocalDockerReadOnlyTransport() DockerReadOnlyTransport {
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
		Timeout:   5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	transport, err := newDockerEngineReadOnlyTransport(client, endpoint)
	if err != nil {
		return NewUnavailableDockerReadOnlyTransport(DockerObservationEndpointLocalNPipe,
			DockerObservationFailureTransportUnsupported)
	}
	return transport
}
