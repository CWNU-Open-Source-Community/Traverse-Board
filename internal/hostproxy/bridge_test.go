package hostproxy

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func proxyClient(t *testing.T, bridge *Bridge) *http.Client {
	t.Helper()
	u, err := url.Parse(bridge.URL())
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(u), DisableKeepAlives: true}, Timeout: 3 * time.Second}
}

func readBody(t *testing.T, response *http.Response) string {
	t.Helper()
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestMixedRoutingAndRedirect(t *testing.T) {
	var upstreamCalls, directCalls atomic.Int32
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		directCalls.Add(1)
		_, _ = io.WriteString(w, "direct:"+r.URL.Path)
	}))
	defer direct.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, direct.URL+"/landing", http.StatusFound)
			return
		}
		_, _ = io.WriteString(w, "upstream:"+r.URL.Host)
	}))
	defer upstream.Close()
	bridge, err := Start(Config{UpstreamURL: upstream.URL, Bypass: "<local>;localhost;127.0.0.1;10.*;*.corp"})
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	client := proxyClient(t, bridge)
	for _, tc := range []struct{ target, want string }{
		{"http://public.invalid/one", "upstream:public.invalid"},
		{direct.URL + "/two", "direct:/two"},
		{"http://public.invalid/redirect", "direct:/landing"},
		{"http://public.invalid/three", "upstream:public.invalid"},
	} {
		response, err := client.Get(tc.target)
		if err != nil {
			t.Fatal(err)
		}
		if got := readBody(t, response); got != tc.want {
			t.Fatalf("%s: got %q, want %q", tc.target, got, tc.want)
		}
	}
	if got := upstreamCalls.Load(); got != 3 {
		t.Fatalf("upstream calls = %d", got)
	}
	if got := directCalls.Load(); got != 2 {
		t.Fatalf("direct calls = %d", got)
	}
	if !bridge.bypasses("10.23.4.5", "80") || !bridge.bypasses("host.corp", "443") || !bridge.bypasses("printer", "80") {
		t.Fatal("wildcard or <local> bypass did not match")
	}
	if !bridge.bypasses("corp", "443") || bridge.bypasses("public.invalid", "80") {
		t.Fatal("bypass matched unrelated target")
	}
}

func TestBypassPort(t *testing.T) {
	bridge, err := Start(Config{UpstreamURL: "http://127.0.0.1:9", Bypass: "example.test:8080;[::1]:443"})
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	if !bridge.bypasses("example.test", "8080") || bridge.bypasses("example.test", "80") || !bridge.bypasses("::1", "443") {
		t.Fatal("port-specific bypass mismatch")
	}
}

func TestConnectDirectAndUpstream(t *testing.T) {
	direct, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer direct.Close()
	go func() {
		for {
			conn, err := direct.Accept()
			if err != nil {
				return
			}
			go func() { defer conn.Close(); _, _ = io.Copy(conn, conn) }()
		}
	}()
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		if r.Method != http.MethodConnect {
			t.Errorf("upstream method %s", r.Method)
			return
		}
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_, _ = rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = rw.Flush()
		_, _ = io.Copy(conn, rw)
	}))
	defer upstream.Close()
	bridge, err := Start(Config{UpstreamURL: upstream.URL, Bypass: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	for _, target := range []string{direct.Addr().String(), "public.invalid:443"} {
		conn, err := net.DialTimeout("tcp", strings.TrimPrefix(bridge.URL(), "http://"), 3*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		_, _ = fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
		reader := bufio.NewReader(conn)
		response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("CONNECT %s: %s", target, response.Status)
		}
		_, _ = conn.Write([]byte("ping"))
		buf := make([]byte, 4)
		if _, err := io.ReadFull(reader, buf); err != nil {
			t.Fatal(err)
		}
		if string(buf) != "ping" {
			t.Fatalf("tunnel echo = %q", buf)
		}
		_ = conn.Close()
	}
	if upstreamCalls.Load() != 1 {
		t.Fatalf("upstream CONNECT calls = %d", upstreamCalls.Load())
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		bridge.mu.Lock()
		count := len(bridge.conns)
		bridge.mu.Unlock()
		if count == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("closed CONNECT sockets retained: %d", count)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCloseClosesTunnelAndStopsNewRequests(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = rw.Flush()
		_, _ = io.Copy(io.Discard, conn)
	}))
	defer upstream.Close()
	bridge, err := Start(Config{UpstreamURL: upstream.URL})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("tcp", strings.TrimPrefix(bridge.URL(), "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = fmt.Fprint(conn, "CONNECT public.invalid:443 HTTP/1.1\r\nHost: public.invalid:443\r\n\r\n")
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT: %v, %#v", err, response)
	}
	closed := make(chan error, 1)
	go func() { closed <- bridge.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
		bridge.mu.Lock()
		count := len(bridge.conns)
		bridge.mu.Unlock()
		if count != 0 {
			t.Fatalf("Close retained %d connections", count)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Close blocked on tunnel")
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := reader.Read(make([]byte, 1)); err == nil {
		t.Fatal("tunnel remained open")
	}
	if err := bridge.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := net.DialTimeout("tcp", strings.TrimPrefix(bridge.URL(), "http://"), 100*time.Millisecond); err == nil {
		t.Fatal("bridge still accepts connections")
	}
}

func TestConnectResponseHeaderCapAndReadAhead(t *testing.T) {
	const prefix = "HTTP/1.1 200 Connection Established\r\nX-Pad: "
	const suffix = "\r\n\r\n"
	capHeader := prefix + strings.Repeat("x", maxConnectResponseHeaderBytes-len(prefix)-len(suffix)) + suffix
	payload := bytes.Repeat([]byte("0123456789abcdef"), 8192)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		header := capHeader
		if r.Host == "oversized.invalid:443" {
			header = prefix + strings.Repeat("x", maxConnectResponseHeaderBytes+1-len(prefix)-len(suffix)) + suffix
		}
		_, _ = rw.WriteString(header)
		if r.Host == "valid.invalid:443" {
			_, _ = rw.Write(payload)
		}
		_ = rw.Flush()
	}))
	defer upstream.Close()
	bridge, err := Start(Config{UpstreamURL: upstream.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	for _, tc := range []struct {
		target string
		status int
	}{
		{"valid.invalid:443", http.StatusOK},
		{"oversized.invalid:443", http.StatusBadGateway},
	} {
		conn, err := net.DialTimeout("tcp", strings.TrimPrefix(bridge.URL(), "http://"), 3*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		_, _ = fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", tc.target, tc.target)
		reader := bufio.NewReader(conn)
		response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != tc.status {
			t.Fatalf("%s status: got %d, want %d", tc.target, response.StatusCode, tc.status)
		}
		if tc.status == http.StatusOK {
			got := make([]byte, len(payload))
			if _, err := io.ReadFull(reader, got); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, payload) {
				t.Fatal("tunnel payload order or content changed after capped header")
			}
		} else {
			_ = response.Body.Close()
		}
		_ = conn.Close()
	}
}

func TestConnectClientDisconnectClosesIdleUpstream(t *testing.T) {
	upstreamSawEOF := make(chan struct{})
	releaseUpstream := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_, _ = rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = rw.Flush()
		// Observe the bridge's FIN, then keep the socket open. A bridge that
		// only calls CloseWrite(remote) stays blocked in its reverse copy.
		_, _ = io.Copy(io.Discard, rw)
		close(upstreamSawEOF)
		<-releaseUpstream
	}))
	defer func() { close(releaseUpstream); upstream.Close() }()
	bridge, err := Start(Config{UpstreamURL: upstream.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	conn, err := net.DialTimeout("tcp", strings.TrimPrefix(bridge.URL(), "http://"), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fmt.Fprint(conn, "CONNECT idle.invalid:443 HTTP/1.1\r\nHost: idle.invalid:443\r\n\r\n")
	response, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodConnect})
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT: %v, %#v", err, response)
	}
	_ = conn.Close()
	select {
	case <-upstreamSawEOF:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream did not observe client disconnect")
	}
	handlerDone := make(chan struct{})
	go func() { bridge.handlers.Wait(); close(handlerDone) }()
	select {
	case <-handlerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("tunnel handler waited for idle upstream to close")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		bridge.mu.Lock()
		count := len(bridge.conns)
		bridge.mu.Unlock()
		if count == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("disconnected tunnel retained %d sockets", count)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestUpstreamFailureNeverFallsBack(t *testing.T) {
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer target.Close()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	failedUpstream := "http://" + listener.Addr().String()
	_ = listener.Close()
	bridge, err := Start(Config{UpstreamURL: failedUpstream})
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	client := proxyClient(t, bridge)
	response, err := client.Get(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = readBody(t, response)
	if response.StatusCode != http.StatusBadGateway || hits.Load() != 0 {
		t.Fatalf("status %d, direct target hits %d", response.StatusCode, hits.Load())
	}
}

func TestConfigValidationAndFingerprint(t *testing.T) {
	for _, c := range []Config{
		{UpstreamURL: "http://user:pass@localhost:8080"},
		{UpstreamURL: "socks5://localhost:8080"},
		{UpstreamURL: "http://localhost:8080", Bypass: "<unknown>"},
		{UpstreamURL: "http://localhost:8080", Bypass: "10.0.0.0/8"},
		{UpstreamURL: "http://localhost:8080", Bypass: "foo:abc"},
		{UpstreamURL: "http://localhost:8080", Bypass: "one, two"},
	} {
		if _, err := Start(c); err == nil {
			t.Fatalf("accepted unsupported config: %+v", c)
		}
	}
	c := Config{UpstreamURL: "http://127.0.0.1:8080", Bypass: "<local>;*.corp"}
	want, err := Fingerprint(c)
	if err != nil {
		t.Fatal(err)
	}
	b1, err := Start(c)
	if err != nil {
		t.Fatal(err)
	}
	defer b1.Close()
	b2, err := Start(c)
	if err != nil {
		t.Fatal(err)
	}
	defer b2.Close()
	if b1.URL() == b2.URL() || b1.Fingerprint() != want || b2.Fingerprint() != want {
		t.Fatal("fingerprint depended on allocated listener port")
	}
	other, err := Fingerprint(Config{UpstreamURL: c.UpstreamURL, Bypass: "<local>"})
	if err != nil {
		t.Fatal(err)
	}
	if other == want {
		t.Fatal("fingerprint ignored bypass change")
	}
}
