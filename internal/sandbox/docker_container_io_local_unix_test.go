//go:build !windows

package sandbox

import "testing"

func TestLocalDockerContainerIOTransportUsesFixedUnixEndpoint(t *testing.T) {
	transport := NewLocalDockerContainerIOTransport()
	if transport == nil || transport.Endpoint().Class != DockerObservationEndpointLocalUnix {
		t.Fatalf("local Docker I/O endpoint = %#v", transport)
	}
}
