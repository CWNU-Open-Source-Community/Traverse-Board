package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"cyberagent-workbench/internal/redact"
)

type clientTransport interface {
	Exchange(context.Context, Envelope) (Envelope, error)
	Notify(context.Context, Envelope) error
	Close() error
}

type stdioClientTransport struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdout    *bufio.Reader
	mu        sync.Mutex
	closeOnce sync.Once
	closeErr  error
	done      chan struct{}
	waitMu    sync.Mutex
	waitErr   error
	stderr    *boundedBuffer
}

func newStdioClientTransport(descriptor ServerDescriptor) (clientTransport, error) {
	if descriptor.Transport != TransportStdio || descriptor.Validate() != nil {
		return nil, errors.New("valid stdio MCP descriptor is required")
	}
	cmd := exec.Command(descriptor.Target, descriptor.Arguments...)
	cmd.Env = minimalMCPEnvironment()
	cmd.Dir = os.TempDir()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("create MCP stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("create MCP stdout: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("create MCP stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("start approved MCP executable: %w", err)
	}
	transport := &stdioClientTransport{cmd: cmd, stdin: stdin, stdout: bufio.NewReader(stdout),
		done: make(chan struct{}), stderr: newBoundedBuffer(16 * 1024)}
	go func() { _, _ = io.Copy(transport.stderr, io.LimitReader(stderrPipe, 64*1024)) }()
	go func() {
		err := cmd.Wait()
		transport.waitMu.Lock()
		transport.waitErr = err
		transport.waitMu.Unlock()
		close(transport.done)
	}()
	return transport, nil
}

func (t *stdioClientTransport) Exchange(ctx context.Context, request Envelope) (Envelope, error) {
	if t == nil {
		return Envelope{}, errors.New("MCP stdio transport is unavailable")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.write(ctx, request); err != nil {
		return Envelope{}, err
	}
	for index := 0; index < 32; index++ {
		response, err := t.read(ctx)
		if err != nil {
			return Envelope{}, err
		}
		if len(response.ID) != 0 && bytes.Equal(bytes.TrimSpace(response.ID), bytes.TrimSpace(request.ID)) {
			return response, nil
		}
	}
	return Envelope{}, errors.New("MCP stdio server did not return the requested response")
}

func (t *stdioClientTransport) Notify(ctx context.Context, request Envelope) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.write(ctx, request)
}

func (t *stdioClientTransport) write(ctx context.Context, value Envelope) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(raw) > MaxMessageBytes {
		return errors.New("MCP request exceeds the transport limit")
	}
	ready := make(chan error, 1)
	go func() {
		_, writeErr := t.stdin.Write(append(raw, '\n'))
		ready <- writeErr
	}()
	select {
	case <-ctx.Done():
		_ = t.Close()
		return ctx.Err()
	case <-t.done:
		return fmt.Errorf("MCP stdio server exited while accepting a request: %v", t.processError())
	case err := <-ready:
		if err != nil {
			return fmt.Errorf("write MCP stdio request: %w", err)
		}
	}
	return nil
}

func (t *stdioClientTransport) read(ctx context.Context) (Envelope, error) {
	type result struct {
		raw []byte
		err error
	}
	ready := make(chan result, 1)
	go func() {
		raw, err := t.stdout.ReadBytes('\n')
		ready <- result{raw: bytes.TrimSpace(raw), err: err}
	}()
	select {
	case <-ctx.Done():
		_ = t.Close()
		return Envelope{}, ctx.Err()
	case <-t.done:
		err := t.processError()
		message := strings.TrimSpace(redact.String(t.stderr.String()))
		if message != "" {
			return Envelope{}, fmt.Errorf("MCP stdio server exited: %v (%s)", err, message)
		}
		return Envelope{}, fmt.Errorf("MCP stdio server exited: %v", err)
	case item := <-ready:
		if item.err != nil {
			return Envelope{}, fmt.Errorf("read MCP stdio response: %w", item.err)
		}
		return DecodeEnvelope(item.raw)
	}
}

func (t *stdioClientTransport) Close() error {
	if t == nil {
		return nil
	}
	t.closeOnce.Do(func() {
		_ = t.stdin.Close()
		if t.cmd != nil && t.cmd.Process != nil {
			_ = t.cmd.Process.Kill()
		}
		select {
		case <-t.done:
			err := t.processError()
			if err != nil && !strings.Contains(strings.ToLower(err.Error()), "killed") {
				t.closeErr = err
			}
		case <-time.After(2 * time.Second):
			t.closeErr = errors.New("MCP stdio process did not exit after termination")
		}
	})
	return t.closeErr
}

func (t *stdioClientTransport) processError() error {
	t.waitMu.Lock()
	defer t.waitMu.Unlock()
	return t.waitErr
}

type remoteClientTransport struct {
	target    string
	bearer    string
	client    *http.Client
	mu        sync.Mutex
	sessionID string
}

func newRemoteClientTransport(descriptor ServerDescriptor, bearer string,
	base *http.Client,
) (clientTransport, error) {
	if descriptor.Transport != TransportStreamableHTTP || descriptor.Validate() != nil {
		return nil, errors.New("valid remote MCP descriptor is required")
	}
	if descriptor.CredentialRef != "" && bearer == "" {
		return nil, errors.New("configured MCP credential is unavailable")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("MCP redirects are forbidden")
	}}
	if base != nil {
		client.Transport = base.Transport
		if client.Transport == nil {
			client.Transport = transport
		}
	}
	return &remoteClientTransport{target: descriptor.Target, bearer: bearer, client: client}, nil
}

func (t *remoteClientTransport) Exchange(ctx context.Context, request Envelope) (Envelope, error) {
	response, err := t.send(ctx, request, false)
	if err != nil {
		return Envelope{}, err
	}
	if len(response) == 0 {
		return Envelope{}, errors.New("remote MCP server returned an empty response")
	}
	return DecodeEnvelope(response)
}

func (t *remoteClientTransport) Notify(ctx context.Context, request Envelope) error {
	_, err := t.send(ctx, request, true)
	return err
}

func (t *remoteClientTransport) send(ctx context.Context, envelope Envelope,
	notification bool,
) ([]byte, error) {
	raw, err := json.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, t.target, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("MCP-Protocol-Version", ProtocolVersion)
	if t.bearer != "" {
		request.Header.Set("Authorization", "Bearer "+t.bearer)
	}
	t.mu.Lock()
	if t.sessionID != "" {
		request.Header.Set("Mcp-Session-Id", t.sessionID)
	}
	t.mu.Unlock()
	response, err := t.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("remote MCP request failed: %w", err)
	}
	defer response.Body.Close()
	if sessionID := strings.TrimSpace(response.Header.Get("Mcp-Session-Id")); sessionID != "" {
		if !validClientText(sessionID, 512, false) {
			return nil, errors.New("remote MCP session identity is invalid")
		}
		t.mu.Lock()
		t.sessionID = sessionID
		t.mu.Unlock()
	}
	if notification && (response.StatusCode == http.StatusAccepted || response.StatusCode == http.StatusNoContent) {
		return nil, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return nil, fmt.Errorf("remote MCP server returned HTTP %d", response.StatusCode)
	}
	contentType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil {
		return nil, errors.New("remote MCP response Content-Type is invalid")
	}
	bounded, err := io.ReadAll(io.LimitReader(response.Body, MaxMessageBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read remote MCP response: %w", err)
	}
	if len(bounded) > MaxMessageBytes {
		return nil, errors.New("remote MCP response exceeds the transport limit")
	}
	switch contentType {
	case "application/json":
		return bytes.TrimSpace(bounded), nil
	case "text/event-stream":
		return decodeMCPServerSentEvent(bounded)
	default:
		return nil, fmt.Errorf("remote MCP response Content-Type %q is unsupported", contentType)
	}
}

func (t *remoteClientTransport) Close() error {
	if t != nil && t.client != nil {
		t.client.CloseIdleConnections()
	}
	return nil
}

func decodeMCPServerSentEvent(raw []byte) ([]byte, error) {
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 4096), MaxMessageBytes)
	var data strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data:") {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
		if line == "" && data.Len() > 0 {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if data.Len() == 0 || data.Len() > MaxMessageBytes {
		return nil, errors.New("remote MCP event stream contains no bounded data event")
	}
	return []byte(data.String()), nil
}

func minimalMCPEnvironment() []string {
	allowed := map[string]struct{}{
		"PATH": {}, "PATHEXT": {}, "SYSTEMROOT": {}, "WINDIR": {},
		"TMP": {}, "TEMP": {}, "TMPDIR": {}, "LANG": {}, "LC_ALL": {},
	}
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
	written := len(value)
	remaining := b.limit - len(b.value)
	if remaining > 0 {
		if len(value) > remaining {
			value = value[:remaining]
		}
		b.value = append(b.value, value...)
	}
	return written, nil
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(bytes.Clone(b.value))
}
