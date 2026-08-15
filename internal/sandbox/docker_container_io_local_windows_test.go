//go:build windows

package sandbox

import "testing"

func TestLocalDockerContainerIOTransportUsesFixedNPipeEndpoint(t *testing.T) {
	transport := NewLocalDockerContainerIOTransport()
	if transport == nil || transport.Endpoint().Class != DockerObservationEndpointLocalNPipe {
		t.Fatalf("local Docker I/O endpoint = %#v", transport)
	}
}
