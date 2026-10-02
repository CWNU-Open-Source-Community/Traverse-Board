package mcp

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type clientTransport interface {
	Exchange(context.Context, Envelope) (Envelope, error)
	Notify(context.Context, Envelope) error
	Close() error
}

func newStdioClientTransport(descriptor ServerDescriptor) (clientTransport, error) {
	if descriptor.Transport != TransportStdio || descriptor.Validate() != nil {
		return nil, errors.New("valid stdio MCP descriptor is required")
	}
	cmd := exec.Command(descriptor.Target, descriptor.Arguments...)
	cmd.Env = minimalMCPEnvironment()
	cmd.Dir = os.TempDir()
	transport := newSDKClientTransport(&commandClientTransport{cmd: cmd})
	// Historical descriptors retain their exact negotiated profile.
	transport.protocolVersion = legacyClientProtocolVersion
	return transport, nil
}

func newRemoteClientTransport(descriptor ServerDescriptor, bearer string, base *http.Client) (clientTransport, error) {
	if descriptor.Transport != TransportStreamableHTTP || descriptor.Validate() != nil {
		return nil, errors.New("valid remote MCP descriptor is required")
	}
	if descriptor.CredentialRef != "" && bearer == "" {
		return nil, errors.New("configured MCP credential is unavailable")
	}
	transport, _ := makeHTTPClientTransport(descriptor.Target, bearer, base)
	transport.protocolVersion = legacyClientProtocolVersion
	return transport, nil
}

func makeHTTPClientTransport(endpoint, bearer string, base *http.Client) (*sdkClientTransport, *mcpHTTPTransport) {
	var roundTripper http.RoundTripper = http.DefaultTransport.(*http.Transport).Clone()
	if base != nil && base.Transport != nil {
		roundTripper = base.Transport
	}
	httpTransport := &mcpHTTPTransport{base: roundTripper, bearer: bearer}
	client := &http.Client{Transport: httpTransport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("MCP redirects are forbidden") }}
	if base != nil {
		client.Timeout = base.Timeout
	}
	transport := newSDKClientTransport(&sdk.StreamableClientTransport{
		Endpoint: endpoint, HTTPClient: client, MaxEventSize: MaxMessageBytes,
		// The host owns reconciliation or explicit retry under fresh authority.
		MaxRetries: -1, DisableStandaloneSSE: true,
		// No OAuthHandler: authentication and interaction belong to the host.
	})
	// The SDK's session-update callback is private and cannot pass through a
	// public Connection observer. Restore only its negotiated header state;
	// the other callback behavior (standalone SSE) is explicitly disabled.
	transport.tap.negotiated = func(version string) { httpTransport.protocolVersion.Store(version) }
	return transport, httpTransport
}

// The SDK owns framing, multiplexing, notifications, and cancellation.
// This adapter owns only process lifetime and bounded stderr retention.
type commandClientTransport struct {
	cmd         *exec.Cmd
	beforeStart func(context.Context) error
	beforeSend  func(context.Context, Envelope, int64) error
}

func (t *commandClientTransport) Connect(ctx context.Context) (sdk.Connection, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	stdin, err := t.cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := t.cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	// Continue draining after the retention bound, so a chatty child cannot stall.
	t.cmd.Stderr = newBoundedBuffer(16 * 1024)
	if t.beforeStart != nil {
		if err := t.beforeStart(ctx); err != nil {
			_ = stdin.Close()
			_ = stdout.Close()
			return nil, err
		}
	}
	if err := t.cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, err
	}
	process := &clientProcess{cmd: t.cmd, stdin: stdin, stdout: stdout, done: make(chan struct{})}
	conn, err := (&sdk.IOTransport{Reader: process, Writer: stdin, MaxLineLength: MaxMessageBytes}).Connect(ctx)
	if err != nil {
		_ = process.Close()
		return nil, err
	}
	return &stdioSDKConnection{Connection: conn, writeGate: make(chan struct{}, 1), beforeSend: t.beforeSend}, nil
}

type clientProcess struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdout    io.ReadCloser
	done      chan struct{}
	waitErr   error
	waitOnce  sync.Once
	closeOnce sync.Once
	closeErr  error
}

func (p *clientProcess) Read(b []byte) (int, error) {
	n, err := p.stdout.Read(b)
	if err != nil {
		p.wait()
	}
	return n, err
}
func (p *clientProcess) wait() {
	// Wait closes StdoutPipe: defer it until its reader finishes or Close
	// explicitly terminates the connection, so the final response cannot race it.
	p.waitOnce.Do(func() { go func() { p.waitErr = p.cmd.Wait(); close(p.done) }() })
}
func (p *clientProcess) Close() error {
	p.closeOnce.Do(func() {
		_ = p.stdin.Close()
		p.wait()
		select {
		case <-p.done:
			p.closeErr = p.waitErr
		case <-time.After(250 * time.Millisecond):
			_ = p.cmd.Process.Kill()
			select {
			case <-p.done:
			case <-time.After(2 * time.Second):
				p.closeErr = errors.New("MCP stdio process did not exit after termination")
			}
		}
		_ = p.stdout.Close()
	})
	return p.closeErr
}

// SSE is incremental and bounded per event by the SDK, not per stream.
type mcpHTTPTransport struct {
	protocolVersion atomic.Value
	base            http.RoundTripper
	bearer          string
	headers         http.Header
	beforeConnect   func(context.Context) error
	beforeSend      func(context.Context, Envelope, int64) error
	connectOnce     sync.Once
	connectErr      error
}

func (t *mcpHTTPTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if err := request.Context().Err(); err != nil {
		return nil, err
	}
	request = request.Clone(request.Context())
	for name, values := range t.headers {
		if existing := request.Header.Values(name); len(existing) > 0 && strings.Join(existing, ", ") != strings.Join(values, ", ") {
			return nil, errors.New("configured MCP header conflicts with SDK protocol framing")
		}
		request.Header[name] = append([]string(nil), values...)
	}
	if t.beforeConnect != nil {
		t.connectOnce.Do(func() { t.connectErr = t.beforeConnect(request.Context()) })
		if t.connectErr != nil {
			return nil, t.connectErr
		}
	}
	version, _ := t.protocolVersion.Load().(string)
	if request.Header.Get("MCP-Protocol-Version") == "" && version != "" {
		request.Header.Set("MCP-Protocol-Version", version)
	}
	var envelope Envelope
	var wireBytes int64
	if request.Method == http.MethodPost {
		if request.GetBody == nil {
			return nil, errors.New("MCP HTTP dispatch requires a prepared request body")
		}
		body, err := request.GetBody()
		if err != nil {
			return nil, err
		}
		raw, err := io.ReadAll(io.LimitReader(body, MaxMessageBytes+1))
		_ = body.Close()
		if err != nil {
			return nil, err
		}
		if len(raw) > MaxMessageBytes {
			return nil, errors.New("MCP request exceeds the transport limit")
		}
		envelope, err = DecodeEnvelope(raw)
		if err != nil {
			return nil, err
		}
		// Modern HTTP cancellation is closing the request stream. SDK v1.8.0
		// also emits a legacy cancellation notification; do not send it on
		// this profile after the HTTP request context has already been cancelled.
		if envelope.Method == "notifications/cancelled" && version == preferredClientProtocolVersion {
			return &http.Response{StatusCode: http.StatusAccepted, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
		}
		wireBytes = int64(len(raw))
	}
	// Even an Idempotency-Key must not make net/http replay a tool POST.
	// SDK-level reconnect and multi-round-trip retries are disabled separately.
	request.GetBody = nil
	if t.bearer != "" {
		request.Header.Set("Authorization", "Bearer "+t.bearer)
	}
	if request.Method == http.MethodPost {
		if t.beforeSend != nil {
			if err := t.beforeSend(request.Context(), envelope, wireBytes); err != nil {
				return nil, err
			}
		}
		// Final HTTP guard: after body/header/credential projection.
		if err := beforeSDKDispatch(request.Context(), envelope.Method); err != nil {
			return nil, err
		}
	}
	response, err := t.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	contentType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if contentType != "text/event-stream" {
		response.Body = &limitedMCPBody{ReadCloser: response.Body, remaining: MaxMessageBytes}
	}
	return response, nil
}
func (t *mcpHTTPTransport) CloseIdleConnections() {
	if closer, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

type limitedMCPBody struct {
	io.ReadCloser
	remaining int
}

func (b *limitedMCPBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if b.remaining == 0 {
		var probe [1]byte
		n, err := b.ReadCloser.Read(probe[:])
		if n > 0 {
			return 0, errors.New("remote MCP response exceeds the transport limit")
		}
		return 0, err
	}
	if len(p) > b.remaining {
		p = p[:b.remaining]
	}
	n, err := b.ReadCloser.Read(p)
	b.remaining -= n
	return n, err
}
func minimalMCPEnvironment() []string {
	allowed := map[string]struct{}{"PATH": {}, "PATHEXT": {}, "SYSTEMROOT": {}, "WINDIR": {},
		"TMP": {}, "TEMP": {}, "TMPDIR": {}, "LANG": {}, "LC_ALL": {}}
	values := make([]string, 0, len(allowed))
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if _, found := allowed[strings.ToUpper(name)]; found {
			values = append(values, entry)
		}
	}
	return values
}

type boundedBuffer struct {
	mu    sync.Mutex
	limit int
	value []byte
}

func newBoundedBuffer(limit int) *boundedBuffer { return &boundedBuffer{limit: limit} }
func (b *boundedBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(value)
	if remaining := b.limit - len(b.value); remaining > 0 {
		if len(value) > remaining {
			value = value[:remaining]
		}
		b.value = append(b.value, value...)
	}
	return n, nil
}
func (b *boundedBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return string(b.value) }
