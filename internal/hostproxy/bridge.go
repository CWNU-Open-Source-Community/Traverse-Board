// Package hostproxy provides a loopback HTTP forward proxy for a static
// Windows proxy and ProxyOverride configuration. It handles HTTP requests and
// CONNECT tunnels only; it cannot route arbitrary TCP/UDP programs or PAC.
package hostproxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxConnectResponseHeaderBytes = 64 << 10

// Config is a static HTTP upstream proxy and the unmodified Windows
// ProxyOverride string. PAC, auto-detection, protocol maps, and credentials
// must be rejected by the caller rather than represented as Config.
type Config struct {
	UpstreamURL string
	Bypass      string
}

type rule struct {
	local bool
	host  string
	port  string
	glob  *regexp.Regexp
}

// Bridge owns one 127.0.0.1 listener. URL can be used as HTTP_PROXY and
// HTTPS_PROXY by HTTP-proxy-aware clients. The port changes on each Start;
// Fingerprint does not.
type Bridge struct {
	listener    net.Listener
	server      *http.Server
	upstream    *url.URL
	rules       []rule
	client      *http.Transport
	address     string
	fingerprint string
	ctx         context.Context
	cancel      context.CancelFunc
	mu          sync.Mutex
	closed      bool
	closeDone   chan struct{}
	closeErr    error
	conns       map[net.Conn]struct{}
	handlers    sync.WaitGroup
	done        chan struct{}
}

// Fingerprint validates Config and returns a stable SHA-256 identity for the
// effective upstream and ordered bypass rules, excluding the listening port.
func Fingerprint(c Config) (string, error) {
	_, _, identity, err := parseConfig(c)
	return identity, err
}

// Start validates all rules before listening. No target allowlist is applied.
// A failed upstream request never falls back to a direct connection.
func Start(c Config) (*Bridge, error) {
	upstream, rules, identity, err := parseConfig(c)
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	b := &Bridge{listener: listener, upstream: upstream, rules: rules,
		address: "http://" + listener.Addr().String(), fingerprint: identity,
		ctx: ctx, cancel: cancel, conns: make(map[net.Conn]struct{}), done: make(chan struct{}), closeDone: make(chan struct{})}
	b.client = &http.Transport{
		Proxy:             b.proxyForRequest,
		DialContext:       (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: -1}).DialContext,
		DisableKeepAlives: true, DisableCompression: true, ForceAttemptHTTP2: false,
	}
	b.server = &http.Server{Handler: b, ConnState: b.connectionState,
		ReadHeaderTimeout: 15 * time.Second}
	go func() { defer close(b.done); _ = b.server.Serve(listener) }()
	return b, nil
}

func (b *Bridge) URL() string         { return b.address }
func (b *Bridge) Fingerprint() string { return b.fingerprint }

// Close cancels active HTTP forwarding, closes active CONNECT tunnels, and
// waits for handlers to stop. Calling Close repeatedly is safe.
func (b *Bridge) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		<-b.closeDone
		return b.closeErr
	}
	b.closed = true
	b.cancel()
	conns := make([]net.Conn, 0, len(b.conns))
	for c := range b.conns {
		conns = append(conns, c)
	}
	b.mu.Unlock()
	err := b.server.Close()
	for _, c := range conns {
		_ = c.Close()
	}
	b.client.CloseIdleConnections()
	<-b.done
	b.handlers.Wait()
	b.closeErr = err
	close(b.closeDone)
	return err
}

func (b *Bridge) connectionState(c net.Conn, state http.ConnState) {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch state {
	case http.StateNew:
		if b.closed {
			_ = c.Close()
		} else {
			b.conns[c] = struct{}{}
		}
	case http.StateClosed:
		delete(b.conns, c)
	}
	// Hijacked CONNECT sockets remain tracked until Close.
}

func (b *Bridge) track(c net.Conn) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		_ = c.Close()
		return false
	}
	b.conns[c] = struct{}{}
	return true
}

func (b *Bridge) untrack(c net.Conn) {
	b.mu.Lock()
	delete(b.conns, c)
	b.mu.Unlock()
}

func (b *Bridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		http.Error(w, "proxy closed", http.StatusServiceUnavailable)
		return
	}
	b.handlers.Add(1)
	b.mu.Unlock()
	defer b.handlers.Done()
	if r.Method == http.MethodConnect {
		b.connect(w, r)
		return
	}
	b.forward(w, r)
}

func (b *Bridge) proxyForRequest(r *http.Request) (*url.URL, error) {
	host, port, err := targetURL(r.URL)
	if err != nil {
		return nil, err
	}
	if b.bypasses(host, port) {
		return nil, nil
	}
	return b.upstream, nil
}

func (b *Bridge) bypasses(host, port string) bool {
	for _, rule := range b.rules {
		if rule.port != "" && rule.port != port {
			continue
		}
		if rule.local {
			if !strings.Contains(host, ".") && net.ParseIP(host) == nil {
				return true
			}
		} else if rule.glob != nil {
			if rule.glob.MatchString(host) {
				return true
			}
		} else if rule.host == host {
			return true
		}
	}
	return false
}

func (b *Bridge) forward(w http.ResponseWriter, r *http.Request) {
	if _, _, err := targetURL(r.URL); err != nil || r.URL.User != nil {
		http.Error(w, "invalid proxy target", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	stop := context.AfterFunc(b.ctx, cancel)
	defer func() { stop(); cancel() }()
	out := r.Clone(ctx)
	out.RequestURI = ""
	out.Host = out.URL.Host
	out.Header = r.Header.Clone()
	stripHopHeaders(out.Header)
	resp, err := b.client.RoundTrip(out)
	if err != nil {
		http.Error(w, "proxy route failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	stripHopHeaders(resp.Header)
	for k, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(k, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (b *Bridge) connect(w http.ResponseWriter, r *http.Request) {
	host, port, err := splitAuthority(r.Host)
	if err != nil {
		http.Error(w, "invalid CONNECT target", http.StatusBadRequest)
		return
	}
	target := net.JoinHostPort(host, port)
	dialer := net.Dialer{Timeout: 10 * time.Second, KeepAlive: -1}
	var remote net.Conn
	var reader *bufio.Reader
	if b.bypasses(host, port) {
		remote, err = dialer.DialContext(b.ctx, "tcp", target)
	} else {
		proxyHost := b.upstream.Host
		if b.upstream.Port() == "" {
			proxyHost = net.JoinHostPort(b.upstream.Hostname(), "80")
		}
		remote, err = dialer.DialContext(b.ctx, "tcp", proxyHost)
		if err == nil {
			if !b.track(remote) {
				return
			}
			_ = remote.SetDeadline(time.Now().Add(15 * time.Second))
			_, err = io.WriteString(remote, "CONNECT "+target+" HTTP/1.1\r\nHost: "+target+"\r\n\r\n")
			if err == nil {
				reader = bufio.NewReader(remote)
				var response *http.Response
				response, err = readConnectResponse(reader)
				if err == nil {
					if response.StatusCode != http.StatusOK {
						err = errors.New("upstream refused CONNECT")
					}
					if response.StatusCode != http.StatusOK {
						_ = response.Body.Close()
					}
				}
			}
			_ = remote.SetDeadline(time.Time{})
		}
	}
	if err != nil {
		if remote != nil {
			b.untrack(remote)
			_ = remote.Close()
		}
		http.Error(w, "proxy route failed", http.StatusBadGateway)
		return
	}
	if !b.track(remote) {
		return
	}
	defer func() { b.untrack(remote); _ = remote.Close() }()
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "CONNECT unavailable", http.StatusInternalServerError)
		return
	}
	client, clientRW, err := hijacker.Hijack()
	if err != nil {
		return
	}
	// net/http does not report StateClosed after Hijack. Release this client
	// explicitly when the tunnel ends so short-lived CONNECTs do not accumulate.
	defer func() { b.untrack(client); _ = client.Close() }()
	if _, err = io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	fromRemote := io.Reader(remote)
	if reader != nil {
		fromRemote = reader
	}
	results := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(remote, clientRW); results <- struct{}{} }()
	go func() { _, _ = io.Copy(client, fromRemote); results <- struct{}{} }()
	<-results
	// A peer may stay idle after the other disconnects. Close both sockets so
	// the remaining copy always exits. TCP half-close continuation is not kept.
	_ = client.Close()
	_ = remote.Close()
	<-results
}

func readConnectResponse(reader *bufio.Reader) (*http.Response, error) {
	header := make([]byte, 0, 4096)
	for len(header) < maxConnectResponseHeaderBytes {
		byteValue, err := reader.ReadByte()
		if err != nil {
			return nil, err
		}
		header = append(header, byteValue)
		if len(header) >= 4 && bytes.Equal(header[len(header)-4:], []byte("\r\n\r\n")) {
			return http.ReadResponse(bufio.NewReader(bytes.NewReader(header)), &http.Request{Method: http.MethodConnect})
		}
	}
	return nil, errors.New("upstream CONNECT response headers exceed 64 KiB")
}

func stripHopHeaders(h http.Header) {
	for _, token := range strings.Split(h.Get("Connection"), ",") {
		h.Del(strings.TrimSpace(token))
	}
	for _, key := range []string{"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "TE", "Trailer", "Transfer-Encoding", "Upgrade"} {
		h.Del(key)
	}
}

func parseConfig(c Config) (*url.URL, []rule, string, error) {
	raw := strings.TrimSpace(c.UpstreamURL)
	u, err := url.Parse(raw)
	if err != nil || u == nil || u.Scheme != "http" || u.User != nil || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return nil, nil, "", errors.New("upstream must be a credential-free HTTP proxy URL")
	}
	if _, _, err = splitProxyHost(u.Host); err != nil {
		return nil, nil, "", err
	}
	var rules []rule
	var parts []string
	for index, rawRule := range strings.Split(c.Bypass, ";") {
		rawRule = strings.TrimSpace(rawRule)
		if rawRule == "" {
			continue
		}
		rule, canonical, err := parseRule(rawRule)
		if err != nil {
			return nil, nil, "", fmt.Errorf("proxy bypass rule %d: %w", index+1, err)
		}
		rules = append(rules, rule)
		parts = append(parts, canonical)
	}
	identity := "hostproxy/v1\n" + strings.ToLower(u.String()) + "\n" + strings.Join(parts, ";")
	sum := sha256.Sum256([]byte(identity))
	return u, rules, hex.EncodeToString(sum[:]), nil
}

func parseRule(value string) (rule, string, error) {
	value = strings.ToLower(value)
	if value == "<local>" {
		return rule{local: true}, value, nil
	}
	if strings.ContainsAny(value, "@/?#=,\\<>") {
		return rule{}, "", errors.New("unsupported syntax")
	}
	host, port := value, ""
	if strings.HasPrefix(value, "[") {
		var err error
		host, port, err = net.SplitHostPort(value)
		if err != nil {
			return rule{}, "", errors.New("unsupported syntax")
		}
	} else if strings.Count(value, ":") == 1 {
		var err error
		host, port, err = net.SplitHostPort(value)
		if err != nil || port == "" {
			return rule{}, "", errors.New("unsupported syntax")
		}
	}
	if port != "" {
		if err := validPort(port); err != nil {
			return rule{}, "", fmt.Errorf("unsupported syntax: %w", err)
		}
	}
	if !validPatternHost(host) {
		return rule{}, "", errors.New("unsupported syntax")
	}
	r := rule{host: strings.ToLower(host), port: port}
	if strings.Contains(host, "*") {
		pattern := "(?i)^" + strings.ReplaceAll(regexp.QuoteMeta(host), `\*`, ".*") + "$"
		r.glob = regexp.MustCompile(pattern)
	}
	return r, value, nil
}

func validPatternHost(host string) bool {
	if host == "" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return true
	}
	for _, c := range host {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == '_' || c == '*' {
			continue
		}
		return false
	}
	return true
}

func validPort(port string) error {
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return errors.New("invalid port")
	}
	return nil
}

func splitProxyHost(authority string) (string, string, error) {
	u := &url.URL{Host: authority}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if !validPatternHost(host) || strings.Contains(host, "*") {
		return "", "", errors.New("invalid upstream proxy host")
	}
	if port != "" {
		if err := validPort(port); err != nil {
			return "", "", err
		}
	}
	return host, port, nil
}

func splitAuthority(authority string) (string, string, error) {
	host, port, err := net.SplitHostPort(authority)
	if err != nil || !validPatternHost(host) || strings.Contains(host, "*") || validPort(port) != nil {
		return "", "", errors.New("invalid target authority")
	}
	return strings.ToLower(host), port, nil
}

func targetURL(u *url.URL) (string, string, error) {
	if u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return "", "", errors.New("invalid target URL")
	}
	host := strings.ToLower(u.Hostname())
	if !validPatternHost(host) || strings.Contains(host, "*") {
		return "", "", errors.New("invalid target host")
	}
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	if err := validPort(port); err != nil {
		return "", "", err
	}
	return host, port, nil
}
