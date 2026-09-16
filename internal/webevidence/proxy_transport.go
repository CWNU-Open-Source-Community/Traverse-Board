package webevidence

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/http/httpproxy"
)

type webSystemProxy struct {
	enabled bool
	server  string
	bypass  string
	pac     bool
}

func configuredWebProxy(host string) (*url.URL, error) {
	allProxy := os.Getenv("ALL_PROXY")
	if allProxy == "" {
		allProxy = os.Getenv("all_proxy")
	}
	return selectWebProxy(host, *httpproxy.FromEnvironment(), allProxy, readWebSystemProxy)
}

// Environment settings take precedence as a set, including an explicit
// NO_PROXY bypass. Windows static settings are consulted only without an
// environment proxy. PAC and unsupported proxy protocols fail explicitly.
func selectWebProxy(host string, environment httpproxy.Config, allProxy string,
	system func() (webSystemProxy, error),
) (*url.URL, error) {
	target := &url.URL{Scheme: "https", Host: net.JoinHostPort(host, "443")}
	if environment.HTTPProxy != "" || environment.HTTPSProxy != "" {
		if environment.HTTPSProxy == "" {
			return nil, nil // HTTP_PROXY alone does not proxy HTTPS in Go.
		}
		proxyURL, err := parseWebHTTPProxy(environment.HTTPSProxy)
		if err != nil {
			return nil, err
		}
		selected, err := environment.ProxyFunc()(target)
		if err != nil {
			return nil, errors.New("web environment proxy configuration is invalid")
		}
		if selected == nil {
			return nil, nil
		}
		return proxyURL, nil
	}
	if strings.TrimSpace(allProxy) != "" {
		return nil, errors.New("web transport requires HTTP_PROXY or HTTPS_PROXY; ALL_PROXY is unsupported")
	}
	settings, err := system()
	if err != nil {
		return nil, errors.New("read system web proxy configuration failed")
	}
	if settings.pac {
		return nil, errors.New("web transport does not support automatic proxy scripts; configure an explicit HTTP proxy")
	}
	if !settings.enabled {
		return nil, nil
	}
	proxyAddress, err := webSystemHTTPSProxy(settings.server)
	if err != nil || proxyAddress == "" {
		return nil, errors.New("enabled system web proxy has no supported HTTPS proxy endpoint")
	}
	proxyURL, err := parseWebHTTPProxy(proxyAddress)
	if err != nil {
		return nil, err
	}
	for _, rawPattern := range strings.Split(settings.bypass, ";") {
		pattern := strings.ToLower(strings.TrimSpace(rawPattern))
		if pattern == "" || pattern == "<-loopback>" {
			continue
		}
		if pattern == "<local>" {
			if !strings.Contains(host, ".") && !strings.Contains(host, ":") {
				return nil, nil
			}
			continue
		}
		matched, patternErr := path.Match(pattern, strings.ToLower(host))
		if patternErr != nil {
			return nil, errors.New("system web proxy bypass pattern is invalid")
		}
		if matched {
			return nil, nil
		}
	}
	// NO_PROXY also applies when the endpoint came from system settings.
	environment.HTTPSProxy = proxyURL.String()
	selected, err := environment.ProxyFunc()(target)
	if err != nil {
		return nil, errors.New("web proxy bypass configuration is invalid")
	}
	if selected == nil {
		return nil, nil
	}
	return proxyURL, nil
}

func webSystemHTTPSProxy(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "=") {
		return raw, nil
	}
	var selected string
	for _, entry := range strings.Split(raw, ";") {
		key, value, found := strings.Cut(strings.TrimSpace(entry), "=")
		if !found {
			return "", errors.New("system web proxy mapping is invalid")
		}
		if strings.EqualFold(strings.TrimSpace(key), "https") {
			if selected != "" {
				return "", errors.New("system web proxy mapping is duplicated")
			}
			selected = strings.TrimSpace(value)
		}
	}
	return selected, nil
}

func parseWebHTTPProxy(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 4096 || containsControl(raw) {
		return nil, errors.New("web proxy endpoint is invalid")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	endpoint, err := url.Parse(raw)
	if err != nil || endpoint.Scheme != "http" || endpoint.Hostname() == "" ||
		endpoint.Opaque != "" || endpoint.Path != "" || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, errors.New("web transport supports only explicit HTTP proxy endpoints")
	}
	port := endpoint.Port()
	if port == "" {
		port = "80"
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return nil, errors.New("web proxy endpoint port is invalid")
	}
	endpoint.Host = net.JoinHostPort(endpoint.Hostname(), port)
	return endpoint, nil
}

// CONNECT names the already validated public IP, never the origin hostname.
// The surrounding Transport performs TLS using the original hostname. Proxy
// credentials are sent only in CONNECT and are never copied to origin headers.
func dialPinnedWebTarget(ctx context.Context, addresses []netip.Addr, proxyURL *url.URL) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if len(addresses) == 0 || len(addresses) > DefaultAddressLimit {
		return nil, errors.New("web transport has no bounded pinned public address set")
	}
	for _, address := range addresses {
		if !IsPublicAddress(address.Unmap()) {
			return nil, errors.New("web transport target is not a pinned public address")
		}
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: -1}
	var last error
	for _, candidate := range addresses {
		target := net.JoinHostPort(candidate.Unmap().String(), "443")
		var conn net.Conn
		if proxyURL == nil {
			conn, last = dialer.DialContext(ctx, "tcp", target)
		} else {
			conn, last = dialWebProxyTunnel(ctx, dialer, proxyURL, target)
		}
		if last == nil {
			return conn, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, fmt.Errorf("dial pinned public web address: %w", last)
}

func dialWebProxyTunnel(ctx context.Context, dialer *net.Dialer, proxyURL *url.URL, target string) (net.Conn, error) {
	conn, err := dialer.DialContext(ctx, "tcp", proxyURL.Host)
	if err != nil {
		return nil, errors.New("connect configured web proxy failed")
	}
	succeeded := false
	defer func() {
		if !succeeded {
			_ = conn.Close()
		}
	}()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	stopped := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = conn.Close(); close(stopped) })
	defer func() {
		if !stop() {
			<-stopped
		}
	}()
	request := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: target},
		Host: target, Header: make(http.Header)}
	if proxyURL.User != nil {
		password, _ := proxyURL.User.Password()
		request.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString(
			[]byte(proxyURL.User.Username()+":"+password)))
	}
	if err := request.Write(conn); err != nil {
		return nil, errors.New("write web proxy CONNECT failed")
	}
	bounded := &webProxyHeaderReader{reader: conn, remaining: 64 * 1024}
	buffer := bufio.NewReader(bounded)
	response, err := http.ReadResponse(buffer, request)
	if err != nil || bounded.remaining <= 0 {
		return nil, errors.New("web proxy CONNECT response is invalid or oversized")
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("web proxy CONNECT rejected with HTTP %d", response.StatusCode)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bounded.unlimited = true
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return nil, errors.New("clear web proxy CONNECT deadline failed")
	}
	succeeded = true
	return &webProxyBufferedConn{Conn: conn, reader: buffer}, nil
}

type webProxyHeaderReader struct {
	reader    io.Reader
	remaining int
	unlimited bool
}

func (r *webProxyHeaderReader) Read(p []byte) (int, error) {
	if r.unlimited {
		return r.reader.Read(p)
	}
	if r.remaining <= 0 {
		return 0, errors.New("web proxy response header limit exceeded")
	}
	if len(p) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.reader.Read(p)
	r.remaining -= n
	return n, err
}

type webProxyBufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *webProxyBufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }
