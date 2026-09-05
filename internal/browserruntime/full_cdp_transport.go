package browserruntime

import (
	"context"
	"errors"
	"sync"
	"time"

	"cyberagent-workbench/internal/domain"
)

const (
	FullCDPRequestCaptureProtocolVersion = "full_cdp_request_capture.v1"
	FullCDPCookieAccessProtocolVersion   = "full_cdp_cookie_access.v1"
	fullCDPAuthorityMonitorInterval      = 20 * time.Millisecond
)

// FullCDPRequestCapture is bounded request metadata only. It never retains
// headers, cookies, response bodies, or any secret-bearing field.
type FullCDPRequestCapture struct {
	ProtocolVersion string    `json:"protocol_version"`
	Authorization   string    `json:"authorization_fingerprint"`
	AllowedRequests int       `json:"allowed_requests"`
	BlockedRequests int       `json:"blocked_requests"`
	CapturedCount   int       `json:"captured_count"`
	CompletedAt     time.Time `json:"completed_at"`
	Fingerprint     string    `json:"fingerprint"`
}

// FullCDPCookieAccess is bounded cookie-name metadata only. Cookie values,
// headers, and other secret-bearing fields are never retained.
type FullCDPCookieAccess struct {
	ProtocolVersion string    `json:"protocol_version"`
	Authorization   string    `json:"authorization_fingerprint"`
	CookieNames     []string  `json:"cookie_names"`
	CompletedAt     time.Time `json:"completed_at"`
	Fingerprint     string    `json:"fingerprint"`
}

// FullCDPSession is the highly-sensitive CDP debug channel. It is independent
// from the Safe Web restricted session and only operates under a confirmed,
// TTL-bounded FullCDPAuthorization. Every result is metadata-only: no raw
// request body, header, cookie value, or page content is ever returned.
type FullCDPSession struct {
	authorization         FullCDPAuthorization
	session               SessionPlan
	permission            domain.RunBrowserCDPPermissionSnapshot
	executionPermission   *domain.RunExecutionPermissionSnapshot
	executionCapabilities domain.ExecutionPermissionRuntimeCapabilities
	executionFence        uint64
	identity              BrowserExecutableIdentity
	client                *restrictedCDPClient
	process               *BrowserProcess
	operation             chan struct{}
	closed                chan struct{}
	selectorSnapshot      fullCDPSelectorSnapshot
	interruptOnce         sync.Once
	transportInterrupted  chan struct{}
}

// OpenFullCDPSession dials a dedicated DevTools endpoint for the already
// launched, contained browser process and returns a Full CDP session gated by
// the confirmed FullCDPAuthorization. It never connects to an existing browser.
func OpenFullCDPSession(ctx context.Context, authorization FullCDPAuthorization,
	session SessionPlan, identity BrowserExecutableIdentity,
	acceptance BrowserAcceptanceCandidate, ownership ProfileOwnershipPlan,
	permission domain.RunBrowserCDPPermissionSnapshot,
	executionPermission domain.RunExecutionPermissionSnapshot,
	executionCapabilities domain.ExecutionPermissionRuntimeCapabilities,
	executionFence uint64, profileLease ProfileRuntimeLease, process *BrowserProcess,
) (*FullCDPSession, error) {
	if err := executionPermission.Validate(); err != nil {
		return nil, err
	}
	if executionPermission.RunID != session.RunID ||
		(executionPermission.Mode != domain.RunExecutionPermissionFullAccess &&
			executionPermission.Mode != domain.RunExecutionPermissionDebug) ||
		!executionCapabilities.AllowsSnapshot(executionPermission) ||
		executionCapabilities.RuntimeAuthority == nil || executionFence == 0 ||
		!executionCapabilities.RuntimeAuthority.AllowsRunAuthorizationFence(
			executionPermission.RunID, executionFence) {
		return nil, errors.New(
			"full CDP requires the exact live Full Access or Debug execution authority")
	}
	return openFullCDPSession(ctx, authorization, session, identity, acceptance,
		ownership, permission, executionPermission, executionCapabilities,
		executionFence, profileLease, process)
}

func openFullCDPSession(ctx context.Context, authorization FullCDPAuthorization,
	session SessionPlan, identity BrowserExecutableIdentity,
	acceptance BrowserAcceptanceCandidate, ownership ProfileOwnershipPlan,
	permission domain.RunBrowserCDPPermissionSnapshot,
	executionPermission domain.RunExecutionPermissionSnapshot,
	executionCapabilities domain.ExecutionPermissionRuntimeCapabilities,
	executionFence uint64, profileLease ProfileRuntimeLease, process *BrowserProcess,
) (*FullCDPSession, error) {
	if err := ValidateFullCDPAuthorization(authorization, session, identity, permission,
		executionPermission, executionCapabilities); err != nil {
		return nil, err
	}
	if err := ValidateBrowserAcceptanceCandidate(acceptance, identity); err != nil {
		return nil, err
	}
	if err := ValidateProfileOwnershipPlan(ownership, session, identity); err != nil {
		return nil, err
	}
	if process == nil || process.PID() <= 0 {
		return nil, errors.New("full CDP requires the exact live browser process")
	}
	if _, exited := process.Exit(); exited {
		return nil, errors.New("full CDP browser process already exited")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	openContext, cancel := boundedRestrictedCDPContext(ctx, authorization.ExpiresAt)
	defer cancel()
	endpoint, err := waitForDevToolsEndpoint(openContext, profileLease.DirectoryPath, process)
	if err != nil {
		return nil, err
	}
	connection, err := dialRestrictedCDP(openContext, endpoint)
	if err != nil {
		return nil, err
	}
	client := &restrictedCDPClient{conn: connection, scope: session.Scope,
		maxRequests: session.Limits.MaxRequests, fullCDP: true}
	if err := client.initialize(openContext); err != nil {
		_ = connection.Close()
		return nil, err
	}
	runtime := &FullCDPSession{
		authorization: authorization, session: session, permission: permission,
		identity: identity, client: client, process: process,
		operation: make(chan struct{}, 1), closed: make(chan struct{}),
		transportInterrupted: make(chan struct{}),
	}
	runtime.executionPermission = &executionPermission
	runtime.executionCapabilities = executionCapabilities
	runtime.executionFence = executionFence
	runtime.operation <- struct{}{}
	go runtime.closeWhenProcessExits()
	return runtime, nil
}

func (runtime *FullCDPSession) RequestCapture(ctx context.Context,
) (FullCDPRequestCapture, error) {
	if runtime == nil || !runtime.authorization.RequestCaptureAuthorized {
		return FullCDPRequestCapture{}, ErrBrowserRuntimeBoundary
	}
	release, operationContext, cancel, err := runtime.beginOperation(ctx)
	if err != nil {
		return FullCDPRequestCapture{}, err
	}
	defer release()
	defer cancel()
	if err := operationContext.Err(); err != nil {
		return FullCDPRequestCapture{}, err
	}
	result := FullCDPRequestCapture{
		ProtocolVersion: FullCDPRequestCaptureProtocolVersion,
		Authorization:   runtime.authorization.Fingerprint,
		AllowedRequests: runtime.client.allowedRequests,
		BlockedRequests: runtime.client.blockedRequests,
		CapturedCount:   len(runtime.client.capturedRequests),
		CompletedAt:     time.Now().UTC(),
	}
	result.Fingerprint = browserRuntimeFingerprint(result)
	return result, nil
}

func (runtime *FullCDPSession) CookieAccess(ctx context.Context,
) (FullCDPCookieAccess, error) {
	if runtime == nil || !runtime.authorization.CookieAccessAuthorized {
		return FullCDPCookieAccess{}, ErrBrowserRuntimeBoundary
	}
	release, operationContext, cancel, err := runtime.beginOperation(ctx)
	if err != nil {
		return FullCDPCookieAccess{}, err
	}
	defer release()
	defer cancel()
	var cookies struct {
		Cookies []struct {
			Name string `json:"name"`
		} `json:"cookies"`
	}
	if err := runtime.client.call(operationContext, runtime.client.sessionID,
		"Network.getCookies", map[string]any{}, &cookies); err != nil {
		return FullCDPCookieAccess{}, err
	}
	names := make([]string, 0, len(cookies.Cookies))
	for _, cookie := range cookies.Cookies {
		if cookie.Name != "" && len(cookie.Name) <= 256 {
			names = append(names, cookie.Name)
		}
	}
	result := FullCDPCookieAccess{
		ProtocolVersion: FullCDPCookieAccessProtocolVersion,
		Authorization:   runtime.authorization.Fingerprint,
		CookieNames:     names,
		CompletedAt:     time.Now().UTC(),
	}
	result.Fingerprint = browserRuntimeFingerprint(result)
	return result, nil
}

func (runtime *FullCDPSession) Close(ctx context.Context) error {
	if runtime == nil {
		return nil
	}
	select {
	case <-runtime.operation:
	case <-runtime.closed:
		return nil
	case <-contextDone(ctx):
		return contextError(ctx)
	}
	close(runtime.closed)
	select {
	case <-runtime.transportInterrupted:
		return nil
	default:
	}
	closeContext, cancel := boundedRestrictedCDPContext(ctx, time.Now().Add(time.Second))
	defer cancel()
	return runtime.client.close(closeContext)
}

func (runtime *FullCDPSession) beginOperation(ctx context.Context,
) (func(), context.Context, context.CancelFunc, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := runtime.validateOperationState(); err != nil {
		return nil, nil, nil, err
	}
	select {
	case <-ctx.Done():
		return nil, nil, nil, ctx.Err()
	case <-runtime.closed:
		return nil, nil, nil, errors.New("full CDP session is closed")
	case <-runtime.operation:
	}
	release := func() {
		select {
		case runtime.operation <- struct{}{}:
		case <-runtime.closed:
		}
	}
	// Authority may change while this operation waits behind another CDP call
	// (or Close) for the single transport token. The admission check above is a
	// fast rejection only; the check after acquisition is the authoritative one.
	if err := ctx.Err(); err != nil {
		release()
		return nil, nil, nil, err
	}
	if err := runtime.validateOperationState(); err != nil {
		release()
		return nil, nil, nil, err
	}
	operationContext, deadlineCancel := boundedRestrictedCDPContext(ctx,
		runtime.authorization.ExpiresAt)
	stopMonitor := make(chan struct{})
	monitorDone := make(chan struct{})
	go runtime.monitorOperationAuthority(operationContext, deadlineCancel,
		stopMonitor, monitorDone)
	var stopOnce sync.Once
	cancel := func() {
		stopOnce.Do(func() {
			close(stopMonitor)
			<-monitorDone
			deadlineCancel()
		})
	}
	return release, operationContext, cancel, nil
}

// monitorOperationAuthority actively interrupts the transport after a runtime
// permission/fence revocation. Context cancellation alone cannot wake a
// websocket ReadMessage whose deadline was already installed, so the
// connection is also closed. Lifecycle cleanup still owns the process/Profile.
func (runtime *FullCDPSession) monitorOperationAuthority(operationContext context.Context,
	cancel context.CancelFunc, stop <-chan struct{}, done chan<- struct{},
) {
	defer close(done)
	ticker := time.NewTicker(fullCDPAuthorityMonitorInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-operationContext.Done():
			runtime.interruptBrowserActionTransport()
			return
		case <-ticker.C:
			if err := runtime.validateOperationState(); err != nil {
				cancel()
				runtime.interruptBrowserActionTransport()
				return
			}
		}
	}
}

func (runtime *FullCDPSession) interruptBrowserActionTransport() {
	if runtime == nil || runtime.client == nil || runtime.client.conn == nil {
		return
	}
	runtime.interruptOnce.Do(func() {
		_ = runtime.client.conn.Close()
		if runtime.transportInterrupted != nil {
			close(runtime.transportInterrupted)
		}
	})
}

func (runtime *FullCDPSession) validateOperationState() error {
	if runtime == nil || runtime.executionPermission == nil ||
		runtime.process == nil {
		return ErrBrowserRuntimeBoundary
	}
	select {
	case <-runtime.closed:
		return errors.New("full CDP session is closed")
	default:
	}
	permission := *runtime.executionPermission
	if err := ValidateFullCDPAuthorization(runtime.authorization, runtime.session,
		runtime.identity, runtime.permission, permission,
		runtime.executionCapabilities); err != nil {
		return err
	}
	authority := runtime.executionCapabilities.RuntimeAuthority
	if !runtime.executionCapabilities.AllowsSnapshot(permission) ||
		authority == nil || !authority.AllowsRunAuthorizationFence(
		permission.RunID, runtime.executionFence) {
		return errors.New("full CDP execution permission was revoked or changed")
	}
	if !time.Now().UTC().Before(runtime.authorization.ExpiresAt) {
		return errors.New("full CDP authorization expired")
	}
	if _, exited := runtime.process.Exit(); exited {
		return errors.New("full CDP browser process exited")
	}
	return nil
}

func (runtime *FullCDPSession) closeWhenProcessExits() {
	select {
	case <-runtime.process.Done():
		_ = runtime.Close(context.Background())
	case <-runtime.closed:
	}
}
