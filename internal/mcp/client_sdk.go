package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Client negotiation is independent of the native server ProtocolVersion,
// and of persisted descriptor format versions.
const preferredClientProtocolVersion = "2026-07-28"

const legacyClientProtocolVersion = "2025-06-18"

func supportedClientProtocol(version string) bool {
	switch version {
	case "2024-11-05", "2025-03-26", "2025-06-18", "2025-11-25", preferredClientProtocolVersion:
		return true
	default:
		return false
	}
}

// Adapt the existing private facade. This is not another public contract.
type sdkClientTransport struct {
	tap             *sdkResponseTap
	protocolVersion string
	transport       sdk.Transport
	mu              sync.Mutex
	session         *sdk.ClientSession
	connectErr      error
	connected       bool
	lifetime        context.Context
	stop            context.CancelFunc
	closeOnce       sync.Once
	closeErr        error
	// Legacy adapter test hooks stay private; resolved clients use ExecutionGuards.
	// The progress handler is the only callback mutable after connection.
	beforeConnect   func(context.Context) error
	beforeCall      func(context.Context) error
	progress        func(context.Context, *sdk.ProgressNotificationParams)
	progressMu      sync.RWMutex
	allowedVersions []string
}

func newSDKClientTransport(transport sdk.Transport) *sdkClientTransport {
	ctx, cancel := context.WithCancel(context.Background())
	tap := &sdkResponseTap{pending: make(map[jsonrpc.ID]sdkPendingResponse)}
	return &sdkClientTransport{transport: &sdkObservingTransport{Transport: transport, tap: tap}, tap: tap,
		lifetime: ctx, stop: cancel, protocolVersion: preferredClientProtocolVersion}
}
func (t *sdkClientTransport) connect(ctx context.Context) (*sdk.ClientSession, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if t.lifetime.Err() != nil {
		return nil, errors.New("MCP client is closed")
	}
	if t.connected {
		return t.session, t.connectErr
	}
	t.connected = true
	if t.beforeConnect != nil {
		if err := t.beforeConnect(ctx); err != nil {
			t.connectErr = err
			return nil, err
		}
	}
	// After host preparation, before SDK Connect can start the process or
	// make any discovery/initialize request. This does not grant authority.
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(t.lifetime, cancel)
	defer stop()
	defer cancel()
	client := sdk.NewClient(&sdk.Implementation{Name: ClientName, Version: ClientVersion}, &sdk.ClientOptions{
		Capabilities:   &sdk.ClientCapabilities{},
		MultiRoundTrip: &sdk.MultiRoundTripOptions{Disabled: true},
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		ProgressNotificationHandler: func(ctx context.Context, req *sdk.ProgressNotificationClientRequest) {
			t.progressMu.RLock()
			handler := t.progress
			t.progressMu.RUnlock()
			if handler != nil {
				handler(ctx, req.Params)
			}
		},
	})
	client.AddSendingMiddleware(func(next sdk.MethodHandler) sdk.MethodHandler {
		return func(ctx context.Context, method string, request sdk.Request) (sdk.Result, error) {
			result, err := next(ctx, method, request)
			// Discovery is refreshed under host authority. Disable only the
			// SDK's decoded catalog cache before it stores this result; the
			// original native response and its TTL remain available to the host.
			switch result := result.(type) {
			case *sdk.ListToolsResult:
				result.TTLMs = 0
			case *sdk.ListResourcesResult:
				result.TTLMs = 0
			case *sdk.ListPromptsResult:
				result.TTLMs = 0
			}
			return result, err
		}
	})
	t.session, t.connectErr = client.Connect(ctx, t.transport, &sdk.ClientSessionOptions{ProtocolVersion: t.protocolVersion})
	if t.session != nil && len(t.allowedVersions) > 0 && !slices.Contains(t.allowedVersions, t.session.InitializeResult().ProtocolVersion) {
		_ = t.session.Close()
		t.session = nil
		t.connectErr = errors.New("MCP negotiated a protocol outside the authorized profile")
	}
	if t.session != nil && t.tap.negotiated != nil {
		t.tap.negotiated(t.session.InitializeResult().ProtocolVersion)
	}
	return t.session, t.connectErr
}
func (t *sdkClientTransport) Exchange(ctx context.Context, request Envelope) (Envelope, error) {
	if err := ctx.Err(); err != nil {
		return Envelope{}, err
	}
	if request.JSONRPC != "2.0" || len(request.ID) == 0 {
		return Envelope{}, errors.New("MCP request identity is required")
	}
	session, err := t.connect(ctx)
	if err != nil {
		return Envelope{}, err
	}
	var result any
	var attempt *sdkCallAttempt
	captured, _ := ctx.Value(sdkRawResponseKey{}).(*sdkRawResponse)
	if captured == nil {
		captured = &sdkRawResponse{}
	}
	ctx = context.WithValue(ctx, sdkRawResponseKey{}, captured)
	defer t.tap.forget(captured)
	switch request.Method {
	case "initialize":
		result = session.InitializeResult()
	case "tools/list":
		var params sdk.ListToolsParams
		if err = json.Unmarshal(request.Params, &params); err == nil {
			result, err = session.ListTools(ctx, &params)
		}
	case "resources/list":
		var params sdk.ListResourcesParams
		if err = json.Unmarshal(request.Params, &params); err == nil {
			result, err = session.ListResources(ctx, &params)
		}
	case "prompts/list":
		var params sdk.ListPromptsParams
		if err = json.Unmarshal(request.Params, &params); err == nil {
			result, err = session.ListPrompts(ctx, &params)
		}
	case "tools/call":
		var params sdk.CallToolParams
		decoder := json.NewDecoder(bytes.NewReader(request.Params))
		decoder.UseNumber()
		if err = decoder.Decode(&params); err != nil {
			break
		}
		attempt, _ = ctx.Value(sdkCallAttemptKey{}).(*sdkCallAttempt)
		if attempt == nil {
			attempt = &sdkCallAttempt{guard: t.beforeCall}
		}
		ctx = context.WithValue(ctx, sdkCallAttemptKey{}, attempt)
		result, err = session.CallTool(ctx, &params)
	default:
		return Envelope{}, fmt.Errorf("unsupported MCP client method %q", request.Method)
	}
	// Keep the original result after SDK correlation, before its typed decoder
	// can round arbitrary numbers or discard extension fields. The SDK remains
	// responsible for framing, SSE, IDs, notifications, and lifecycle.
	raw, rpcErr := captured.result()
	if len(raw) != 0 {
		if capture, _ := ctx.Value(resolvedCatalogKey{}).(*resolvedCatalog); capture != nil && request.Method != "tools/call" {
			if len(raw) > MaxMessageBytes-capture.bytes {
				return Envelope{}, errors.New("MCP native discovery exceeds its aggregate result limit")
			}
			capture.bytes += len(raw)
			capture.results = append(capture.results, append(json.RawMessage(nil), raw...))
		}
		if len(raw) > MaxMessageBytes {
			return Envelope{}, errors.New("MCP response exceeds the transport limit")
		}
		var shape struct {
			ResultType string `json:"resultType"`
		}
		if decodeErr := json.Unmarshal(raw, &shape); decodeErr != nil {
			return Envelope{}, decodeErr
		}
		if shape.ResultType == "input_required" {
			return Envelope{}, &sdkInputRequiredError{raw: raw}
		}
		return Envelope{JSONRPC: "2.0", ID: append(json.RawMessage(nil), request.ID...), Result: raw}, nil
	}
	if rpcErr != nil {
		return Envelope{JSONRPC: "2.0", ID: request.ID, Error: &RPCError{Code: int(rpcErr.Code), Message: "remote MCP request failed"}}, nil
	}
	if err != nil {
		if attempt != nil && attempt.dispatched.Load() {
			return Envelope{}, &sdkOutcomeUnknownError{cause: err}
		}
		if attempt != nil {
			attempt.mu.Lock()
			rejection := attempt.rejection
			attempt.mu.Unlock()
			if rejection != nil {
				return Envelope{}, rejection
			}
		}
		if ctx.Err() != nil {
			return Envelope{}, ctx.Err()
		}
		return Envelope{}, err
	}
	raw, err = json.Marshal(result)
	if err != nil {
		return Envelope{}, err
	}
	if len(raw) > MaxMessageBytes {
		return Envelope{}, errors.New("MCP response exceeds the transport limit")
	}
	return Envelope{JSONRPC: "2.0", ID: append(json.RawMessage(nil), request.ID...), Result: raw}, nil
}
func (t *sdkClientTransport) Notify(ctx context.Context, request Envelope) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if request.Method != "notifications/initialized" {
		return errors.New("unsupported MCP client notification")
	}
	// SDK already sends this for legacy peers; modern peers do not use it.
	return nil
}
func (t *sdkClientTransport) Close() error {
	t.closeOnce.Do(func() {
		t.stop()
		t.mu.Lock()
		session := t.session
		t.mu.Unlock()
		if session != nil {
			t.closeErr = session.Close()
		}
	})
	return t.closeErr
}

type sdkCallAttemptKey struct{}
type sdkCallAttempt struct {
	guard      func(context.Context) error
	dispatched atomic.Bool
	mu         sync.Mutex
	rejection  error
	wireID     json.RawMessage
	cancelSent atomic.Bool
}

func (a *sdkCallAttempt) reject(err error) error {
	a.mu.Lock()
	a.rejection = err
	a.mu.Unlock()
	return err
}

func beforeSDKDispatch(ctx context.Context, method string) error {
	if method != "tools/call" {
		return ctx.Err()
	}
	call, _ := ctx.Value(sdkCallAttemptKey{}).(*sdkCallAttempt)
	if call == nil {
		return errors.New("MCP tool dispatch is missing its host call context")
	}
	if err := ctx.Err(); err != nil {
		return call.reject(err)
	}
	if call.guard != nil {
		if err := call.guard(ctx); err != nil {
			return call.reject(err)
		}
	}
	if err := ctx.Err(); err != nil {
		return call.reject(err)
	}
	if !call.dispatched.CompareAndSwap(false, true) {
		return errors.New("automatic MCP tool call replay is forbidden")
	}
	return nil
}

type sdkOutcomeUnknownError struct{ cause error }

func (e *sdkOutcomeUnknownError) Error() string {
	return "MCP tool outcome is unknown after dispatch; do not automatically repeat the action: " + e.cause.Error()
}
func (e *sdkOutcomeUnknownError) Unwrap() error { return e.cause }

type sdkInputRequiredError struct{ raw json.RawMessage }

func (*sdkInputRequiredError) Error() string {
	return "MCP tool requires additional input; host mediation is required before any retry"
}

// A cancelled response wait stays request-scoped. Only a blocked pipe write
// fails the connection: its byte framing can no longer be safely shared.
type stdioSDKConnection struct {
	sdk.Connection
	writeGate  chan struct{}
	beforeSend func(context.Context, Envelope, int64) error
}

func (c *stdioSDKConnection) Write(ctx context.Context, msg jsonrpc.Message) error {
	select {
	case c.writeGate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-c.writeGate }()
	if request, ok := msg.(*jsonrpc.Request); ok {
		if c.beforeSend != nil {
			raw, err := jsonrpc.EncodeMessage(msg)
			if err != nil {
				return err
			}
			envelope, err := DecodeEnvelope(raw)
			if err != nil {
				return err
			}
			if err := c.beforeSend(ctx, envelope, int64(len(raw)+1)); err != nil {
				return err
			}
		}
		// Final stdio guard: after acquiring the writer, before SDK framing.
		if err := beforeSDKDispatch(ctx, request.Method); err != nil {
			return err
		}
	}
	done := make(chan error, 1)
	go func() { done <- c.Connection.Write(ctx, msg) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		_ = c.Connection.Close()
		return ctx.Err()
	}
}

// Preserve errors.Is/errors.As without exposing unsanitized diagnostics.
type sanitizedClientError struct {
	message string
	cause   error
}

func (e *sanitizedClientError) Error() string { return e.message }
func (e *sanitizedClientError) Unwrap() error { return e.cause }
