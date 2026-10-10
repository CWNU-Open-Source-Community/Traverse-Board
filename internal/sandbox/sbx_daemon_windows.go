//go:build windows

package sandbox

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func newSBXDaemonTransport() SBXDaemonTransport {
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			// Identification lets sandboxd verify the connecting OS user. It
			// cannot impersonate the client to act with its host permissions.
			return winio.DialPipeAccessImpLevel(ctx,
				`\\.\pipe\docker_kaname_sandboxes-`+SBXAppName+`_sandboxd`,
				windows.GENERIC_READ|windows.GENERIC_WRITE, winio.PipeImpLevelIdentification)
		},
		ResponseHeaderTimeout:  8 * time.Second,
		MaxResponseHeaderBytes: 32 * 1024,
		DisableCompression:     true,
		DisableKeepAlives:      true,
	}
	return &sbxDaemonHTTPTransport{client: &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
}
