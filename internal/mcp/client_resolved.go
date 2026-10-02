package mcp

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"runtime"
	"slices"
	"sync"
	"time"

	"cyberagent-workbench/internal/toolcontract"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type resolvedClient struct {
	launch      toolcontract.ResolvedLaunch
	key         []byte
	connect     toolcontract.Operation
	guards      toolcontract.ExecutionGuards
	fingerprint string
	mu          sync.Mutex
	capability  string
}
type resolvedDiscoveryKey struct{}
type resolvedDiscovery struct {
	scope toolcontract.DiscoveryScope
	guard toolcontract.DiscoverySendGuard
}
type resolvedCatalogKey struct{}
type resolvedCatalog struct {
	results []json.RawMessage
	bytes   int
}
type resolvedCallKey struct{}
type resolvedCall struct{ operation toolcontract.Operation }

// NewResolvedClient freezes the approved contract without connecting. The host
// supplies authorization and revision-bound credentials; no policy or ledger is
// created here. A nil guard at a traversed boundary denies that operation.
func NewResolvedClient(launch toolcontract.ResolvedLaunch, hostKey []byte, connect toolcontract.Operation,
	guards toolcontract.ExecutionGuards, credential func(context.Context, toolcontract.CredentialRef) (string, error),
	base *http.Client,
) (*Client, error) {
	launch = cloneResolvedLaunch(launch)
	key := bytes.Clone(hostKey)
	fingerprint, err := toolcontract.FingerprintLaunch(key, launch)
	if err != nil || connect.Validate() != nil || connect.Kind != toolcontract.OperationConnect ||
		connect.Component != launch.Component || connect.InputFingerprint != fingerprint {
		return nil, errors.New("MCP connect operation does not bind the resolved launch")
	}
	for _, version := range launch.ProtocolVersions {
		if !supportedClientProtocol(version) {
			return nil, errors.New("unsupported MCP client profile")
		}
	}
	r := &resolvedClient{launch: launch, key: key, connect: cloneOperation(connect), guards: guards, fingerprint: fingerprint}
	var transport *sdkClientTransport
	switch launch.Transport {
	case toolcontract.TransportStdio:
		env, err := normalizeLaunchEnvironment(launch.Stdio.Env)
		if err != nil || len(env) != len(launch.Stdio.Env) {
			return nil, errors.New("resolved MCP environment is ambiguous")
		}
		// Requiring canonical keys makes the execution and HMAC representations identical.
		for key, value := range env {
			if original, exists := launch.Stdio.Env[key]; !exists || original != value {
				return nil, errors.New("resolved MCP environment was not normalized")
			}
		}
		if runtime.GOOS == "windows" && env["SYSTEMROOT"] == "" {
			return nil, errors.New("resolved Windows launch is missing SystemRoot")
		}
		cmd := exec.Command(launch.Stdio.Command, launch.Stdio.Args...)
		cmd.Dir, cmd.Env = launch.Stdio.Cwd, launchEnvironmentEntries(env)
		transport = newSDKClientTransport(&commandClientTransport{cmd: cmd, beforeStart: r.beforeConnect, beforeSend: r.beforeSend})
	case toolcontract.TransportStreamableHTTP:
		var remote *mcpHTTPTransport
		if base == nil {
			configured := http.DefaultTransport.(*http.Transport).Clone()
			configured.Proxy = nil // no ambient proxy configuration in a frozen launch
			base = &http.Client{Transport: configured}
		}
		transport, remote = makeHTTPClientTransport(launch.HTTP.Endpoint, "", base)
		remote.headers = make(http.Header, len(launch.HTTP.Headers))
		for name, value := range launch.HTTP.Headers {
			if slices.Contains([]string{"Host", "Content-Length", "Transfer-Encoding", "Connection", "Trailer", "Mcp-Protocol-Version", "Mcp-Session-Id", "Mcp-Method", "Mcp-Name"}, http.CanonicalHeaderKey(name)) {
				return nil, errors.New("MCP launch contains a transport-controlled HTTP header")
			}
			remote.headers.Set(name, value)
		}
		remote.beforeConnect = func(ctx context.Context) error {
			if err := r.beforeConnect(ctx); err != nil {
				return err
			}
			if launch.HTTP.Credential != nil {
				if credential == nil {
					return errors.New("MCP credential resolver is unavailable")
				}
				value, err := credential(ctx, *launch.HTTP.Credential)
				if err != nil || value == "" {
					return errors.New("bound MCP credential is unavailable")
				}
				remote.bearer = value
			}
			return nil
		}
		remote.beforeSend = r.beforeSend
	}
	transport.protocolVersion = launch.ProtocolVersions[0]
	transport.allowedVersions = slices.Clone(launch.ProtocolVersions)
	transport.tap.acceptProtocol = func(version string) error {
		if !slices.Contains(launch.ProtocolVersions, version) {
			return errors.New("MCP peer selected an unauthorized profile")
		}
		return nil
	}
	client := newClient(transport, ServerDescriptor{DeclaredCapabilities: []CapabilityKind{CapabilityTools, CapabilityResources, CapabilityPrompts}})
	client.resolved = r
	return client, nil
}

func cloneOperation(operation toolcontract.Operation) toolcontract.Operation {
	operation.Targets = slices.Clone(operation.Targets)
	operation.Effects = slices.Clone(operation.Effects)
	return operation
}
func (r *resolvedClient) currentLaunch() (toolcontract.ResolvedLaunch, error) {
	current := cloneResolvedLaunch(r.launch)
	if current.Stdio != nil {
		var err error
		current.Stdio.Cwd, err = canonicalLaunchDirectory(current.Stdio.Cwd)
		if err != nil {
			return current, err
		}
		current.Stdio.Command, err = resolveLaunchExecutable(current.Stdio.Command, current.Stdio.Cwd, current.Stdio.Env)
		if err != nil {
			return current, err
		}
		current.Stdio.ExecutableSHA256, err = launchExecutableDigest(current.Stdio.Command)
		if err != nil {
			return current, err
		}
	}
	return current, nil
}
func (r *resolvedClient) beforeConnect(ctx context.Context) error {
	if r.guards.BeforeConnect == nil {
		return errors.New("MCP startup requires a host guard")
	}
	current, err := r.currentLaunch()
	if err != nil {
		return err
	}
	fingerprint, err := toolcontract.FingerprintLaunch(r.key, current)
	if err != nil || fingerprint != r.fingerprint {
		return errors.New("MCP launch changed after authorization preparation")
	}
	operation := cloneOperation(r.connect)
	operation.InputFingerprint = fingerprint
	actual, err := toolcontract.FingerprintOperation(operation)
	if err != nil {
		return err
	}
	if err := r.guards.BeforeConnect(ctx, actual); err != nil {
		return err
	}
	// A host check may block while the executable/directory is replaced.
	current, err = r.currentLaunch()
	if err != nil {
		return err
	}
	if after, err := toolcontract.FingerprintLaunch(r.key, current); err != nil || after != fingerprint {
		return errors.New("MCP launch changed during startup authorization")
	}
	return ctx.Err()
}

func (r *resolvedClient) beforeSend(ctx context.Context, frame Envelope, size int64) error {
	if frame.Method == "tools/call" {
		attempt, _ := ctx.Value(sdkCallAttemptKey{}).(*sdkCallAttempt)
		call, _ := ctx.Value(resolvedCallKey{}).(*resolvedCall)
		if attempt == nil || call == nil {
			return errors.New("MCP tool call has no host operation")
		}
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if json.Unmarshal(frame.Params, &params) != nil {
			return attempt.reject(errors.New("MCP tool frame is invalid"))
		}
		actualInput, err := FingerprintCallInput(r.key, r.launch, params.Name, params.Arguments)
		if err != nil || actualInput != call.operation.InputFingerprint {
			return attempt.reject(errors.New("MCP tool inputs changed before dispatch"))
		}
		operation := cloneOperation(call.operation)
		operation.InputFingerprint = actualInput
		actual, err := toolcontract.FingerprintOperation(operation)
		if err != nil {
			return attempt.reject(err)
		}
		attempt.mu.Lock()
		attempt.wireID = bytes.Clone(frame.ID)
		attempt.mu.Unlock()
		// beforeSDKDispatch invokes this closure once, after all projection.
		attempt.guard = func(ctx context.Context) error {
			if r.guards.BeforeToolCall == nil {
				return errors.New("MCP tool call requires a host guard")
			}
			if err := r.guards.BeforeToolCall(ctx, actual); err != nil {
				return err
			}
			r.mu.Lock()
			matches := r.capability == call.operation.CapabilityFingerprint
			r.mu.Unlock()
			if !matches {
				return errors.New("MCP capability changed during authorization")
			}
			return nil
		}
		return nil
	}
	if frame.Method == "notifications/cancelled" {
		attempt, _ := ctx.Value(sdkCallAttemptKey{}).(*sdkCallAttempt)
		var params struct {
			RequestID json.RawMessage `json:"requestId"`
		}
		if attempt == nil || !attempt.dispatched.Load() || json.Unmarshal(frame.Params, &params) != nil {
			return errors.New("unbound MCP cancellation")
		}
		attempt.mu.Lock()
		matches := bytes.Equal(bytes.TrimSpace(params.RequestID), bytes.TrimSpace(attempt.wireID))
		attempt.mu.Unlock()
		if !matches || !attempt.cancelSent.CompareAndSwap(false, true) {
			return errors.New("unbound or repeated MCP cancellation")
		}
		return nil // Cancels only this dispatched request; never authorizes a new call.
	}
	discovery, _ := ctx.Value(resolvedDiscoveryKey{}).(*resolvedDiscovery)
	if discovery == nil || discovery.guard == nil {
		return errors.New("MCP protocol send requires a bounded discovery scope")
	}
	if frame.Method == "initialize" {
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if json.Unmarshal(frame.Params, &params) != nil || !slices.Contains(r.launch.ProtocolVersions, params.ProtocolVersion) {
			return errors.New("MCP negotiation exceeds permitted protocol versions")
		}
	}
	return discovery.guard(ctx, toolcontract.DiscoverySend{ConnectionFingerprint: r.fingerprint, Method: frame.Method, Bytes: size})
}

// DiscoverWithScope authorizes negotiation plus bounded pagination once and
// rechecks each actual encoded JSON-RPC send. Incoming progress uses no send budget.
func (c *Client) DiscoverWithScope(ctx context.Context, scope toolcontract.DiscoveryScope) (CapabilitySnapshot, error) {
	if c == nil || c.resolved == nil {
		return CapabilitySnapshot{}, errors.New("client has no resolved host contract")
	}
	r := c.resolved
	if scope.Validate() != nil || scope.ConnectionFingerprint != r.fingerprint || scope.Profile != r.launch.ProtocolVersions[0] || r.guards.BeginDiscovery == nil {
		return CapabilitySnapshot{}, errors.New("MCP discovery scope does not bind this connection")
	}
	scope.Methods = slices.Clone(scope.Methods)
	ctx, cancel := context.WithDeadline(ctx, scope.ExpiresAt)
	defer cancel()
	guard, err := r.guards.BeginDiscovery(ctx, scope)
	if err != nil {
		return CapabilitySnapshot{}, err
	}
	if guard == nil {
		return CapabilitySnapshot{}, errors.New("MCP discovery was not authorized")
	}
	r.mu.Lock()
	r.capability = ""
	r.mu.Unlock()
	capture := &resolvedCatalog{}
	ctx = context.WithValue(ctx, resolvedDiscoveryKey{}, &resolvedDiscovery{scope: scope, guard: guard})
	ctx = context.WithValue(ctx, resolvedCatalogKey{}, capture)
	snapshot, err := c.Discover(ctx, time.Now())
	if err != nil {
		return CapabilitySnapshot{}, err
	}
	// Bind the native pages, including annotations, output schemas and extensions,
	// without forcing them through the legacy storage projection.
	encoded, _ := json.Marshal(struct {
		Protocol, SnapshotFingerprint string
		Pages                         []json.RawMessage
	}{snapshot.ProtocolVersion, snapshot.Fingerprint, capture.results})
	hash := hmac.New(sha256.New, r.key)
	_, _ = hash.Write([]byte("mcp.capabilities.v1\x00"))
	_, _ = hash.Write(encoded)
	r.mu.Lock()
	r.capability = hex.EncodeToString(hash.Sum(nil))
	r.mu.Unlock()
	return snapshot, nil
}

// NativeCapabilityFingerprint binds the last successful bounded discovery. It
// is ephemeral; C attaches it to the existing operation/authorization ledger.
func (c *Client) NativeCapabilityFingerprint() string {
	if c == nil || c.resolved == nil {
		return ""
	}
	c.resolved.mu.Lock()
	defer c.resolved.mu.Unlock()
	return c.resolved.capability
}

// FingerprintCallInput binds the frozen launch and semantic tool inputs using
// the host's private key. JSON numbers retain their original precision.
func FingerprintCallInput(key []byte, launch toolcontract.ResolvedLaunch, name string, arguments json.RawMessage) (string, error) {
	if !validRemoteName(name) || len(arguments) == 0 || len(arguments) > MaxClientArgumentsBytes || !json.Valid(arguments) || bytes.TrimSpace(arguments)[0] != '{' {
		return "", errors.New("invalid MCP tool name or arguments")
	}
	launchFingerprint, err := toolcontract.FingerprintLaunch(key, launch)
	if err != nil {
		return "", err
	}
	var args any
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.UseNumber()
	if err := decoder.Decode(&args); err != nil {
		return "", errors.New("invalid MCP arguments")
	}
	raw, err := json.Marshal(struct {
		Launch, Name string
		Arguments    any
	}{launchFingerprint, name, args})
	if err != nil {
		return "", errors.New("MCP tool input could not be fingerprinted")
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("mcp.call.input.v1\x00"))
	_, _ = mac.Write(raw)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

// CallToolWithReceipt returns a receipt even on errors. Native JSON is transient
// sensitive data for the host's redaction boundary, never a logging payload. The
// SDK result provides typed content; raw preserves unknown fields and numbers.
func (c *Client) CallToolWithReceipt(ctx context.Context, operation toolcontract.Operation, name string, arguments json.RawMessage) (*sdk.CallToolResult, json.RawMessage, toolcontract.Receipt, error) {
	receipt := toolcontract.Receipt{OperationID: operation.ID, State: toolcontract.ReceiptNotDispatched}
	if c == nil || c.resolved == nil || c.closed.Load() || operation.Validate() != nil || operation.Kind != toolcontract.OperationToolCall ||
		operation.Component != c.resolved.launch.Component || operation.CapabilityFingerprint != c.NativeCapabilityFingerprint() {
		return nil, nil, receipt, errors.New("MCP tool operation is not bound to current discovery")
	}
	input, err := FingerprintCallInput(c.resolved.key, c.resolved.launch, name, arguments)
	if err != nil || input != operation.InputFingerprint {
		return nil, nil, receipt, errors.New("MCP tool input fingerprint differs from its operation")
	}
	attempt, capture := &sdkCallAttempt{}, &sdkRawResponse{}
	ctx = context.WithValue(ctx, resolvedCallKey{}, &resolvedCall{operation: cloneOperation(operation)})
	ctx = context.WithValue(ctx, sdkCallAttemptKey{}, attempt)
	ctx = context.WithValue(ctx, sdkRawResponseKey{}, capture)
	params, _ := json.Marshal(struct {
		Name      string            `json:"name"`
		Arguments json.RawMessage   `json:"arguments"`
		Meta      map[string]string `json:"_meta,omitempty"`
	}{name, bytes.Clone(arguments), c.callProgressMeta(operation.ID)})
	response, err := c.transport.Exchange(ctx, Envelope{JSONRPC: "2.0", ID: json.RawMessage(fmt.Sprint(c.nextID.Add(1))), Method: "tools/call", Params: params})
	raw, wireError := capture.result()
	if len(raw) > 0 || wireError != nil {
		receipt.State = toolcontract.ReceiptResultReceived
	} else if attempt.dispatched.Load() {
		receipt.State = toolcontract.ReceiptOutcomeUnknown
	}
	if wireError != nil || response.Error != nil {
		receipt.ErrorCode = "remote_rpc_error"
		if wireError != nil {
			raw, _ = json.Marshal(struct {
				Error any `json:"error"`
			}{wireError})
		}
		return nil, raw, receipt, errors.New("MCP peer returned a JSON-RPC error")
	}
	if err != nil {
		receipt.ErrorCode = "mcp_call_failed"
		return nil, raw, receipt, &sanitizedClientError{message: "MCP tool call failed; inspect its execution receipt before recovery", cause: err}
	}
	var result sdk.CallToolResult
	var shape struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &shape); err != nil || len(bytes.TrimSpace(shape.Content)) == 0 || bytes.TrimSpace(shape.Content)[0] != '[' {
		receipt.ErrorCode = "invalid_result"
		return nil, raw, receipt, errors.New("MCP tool result content is missing or invalid")
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		receipt.ErrorCode = "invalid_result"
		return nil, raw, receipt, errors.New("MCP tool result could not be decoded")
	}
	return &result, raw, receipt, nil
}

// SetProgressHandler opts into MCP progress notifications. A call then uses its
// operation ID as the progress token. Handlers must return promptly; callbacks
// contain untrusted peer data and are not authorization or durable state.
func (c *Client) SetProgressHandler(handler func(context.Context, *sdk.ProgressNotificationParams)) error {
	if c == nil || c.closed.Load() {
		return errors.New("MCP client is closed")
	}
	transport, ok := c.transport.(*sdkClientTransport)
	if !ok {
		return errors.New("MCP transport does not support SDK progress")
	}
	transport.progressMu.Lock()
	transport.progress = handler
	transport.progressMu.Unlock()
	return nil
}
func (c *Client) callProgressMeta(operationID string) map[string]string {
	transport, ok := c.transport.(*sdkClientTransport)
	if !ok {
		return nil
	}
	transport.progressMu.RLock()
	enabled := transport.progress != nil
	transport.progressMu.RUnlock()
	if enabled {
		return map[string]string{"progressToken": operationID}
	}
	return nil
}
