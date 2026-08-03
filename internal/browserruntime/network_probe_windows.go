//go:build windows

package browserruntime

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

const browserNetworkProbeProfilePrefix = "prayu-browser-network-probe-"

var (
	errBrowserNetworkProbeProcessExited = errors.New(
		"browser network probe process exited before its canaries")
	errBrowserNetworkProbeProfilePrepare = errors.New(
		"browser network probe Profile preparation failed")
	errBrowserNetworkProbeProfileCleanup = errors.New(
		"browser network probe Profile cleanup failed")
	errBrowserNetworkProbeProcessStop = errors.New(
		"browser network probe process stop failed")
	errBrowserNetworkProbeTreeNotReaped = errors.New(
		"browser network probe process tree was not reaped")
)

type browserNetworkProbeEndpoint struct {
	address  netip.Addr
	port     uint16
	listener net.Listener
	server   *http.Server
	mu       sync.Mutex
	hits     map[string]int
}

type browserNetworkProbeHarness struct {
	exact         *browserNetworkProbeEndpoint
	wrongPort     *browserNetworkProbeEndpoint
	wrongLoopback *browserNetworkProbeEndpoint
	nonLoopback   *browserNetworkProbeEndpoint
	ipv6          *browserNetworkProbeEndpoint
}

type browserNetworkProbeObservation struct {
	Exact         bool
	WrongPort     bool
	WrongLoopback bool
	NonLoopback   bool
	IPv6          bool
}

func runPlatformBrowserNetworkContainmentProbe(ctx context.Context,
	identity BrowserExecutableIdentity, request BrowserNetworkProbeRequest,
) BrowserNetworkProbeReport {
	report := BrowserNetworkProbeReport{
		ID: request.ID, CollectorIdentity: request.CollectorIdentity,
		Adapter: WindowsWFPBrowserContainmentAdapterName, Production: true,
		StartedAt: time.Now().UTC(),
	}
	if !windows.GetCurrentProcessToken().IsElevated() {
		return finishBrowserNetworkProbeReport(report, "wfp_elevation_required")
	}
	if !((windowsWFPBrowserContainmentFactory{}).Available()) {
		return finishBrowserNetworkProbeReport(report, "wfp_adapter_unavailable")
	}
	if err := rejectExistingExecutableProcesses(identity.CanonicalPath); err != nil {
		return finishBrowserNetworkProbeReport(report, "selected_browser_already_running")
	}
	harness, err := newBrowserNetworkProbeHarness(ctx)
	if err != nil {
		return finishBrowserNetworkProbeReport(report, "local_canary_setup_failed")
	}
	defer harness.Close()

	baselineToken := browserRuntimeFingerprint(struct {
		ID    string `json:"id"`
		Phase string `json:"phase"`
		At    int64  `json:"at"`
	}{request.ID, "baseline", time.Now().UnixNano()})[:24]
	baselineGuard, err := prepareWindowsWFPProbeGuard(identity.CanonicalPath,
		harness.Targets(), "Prayu browser network probe baseline", baselineToken)
	if err != nil {
		return finishBrowserNetworkProbeReport(report, "baseline_wfp_install_failed")
	}
	report.DynamicSessionObserved = true
	report.AtomicInstallObserved = true
	baselineRunErr := runBrowserNetworkProbePhase(ctx, identity, harness,
		baselineToken, true)
	baselineCleanupErr := baselineGuard.Close()
	baseline := harness.Observe(baselineToken)
	if baselineCleanupErr != nil || !baselineGuard.CleanupVerified() {
		return finishBrowserNetworkProbeReport(report, "baseline_wfp_cleanup_failed")
	}
	if baselineRunErr != nil {
		return finishBrowserNetworkProbeReport(report,
			browserNetworkProbeRunFailureCode("baseline", baselineRunErr))
	}
	if !baseline.All() {
		return finishBrowserNetworkProbeReport(report, "baseline_canaries_not_observed")
	}

	if err := rejectExistingExecutableProcesses(identity.CanonicalPath); err != nil {
		return finishBrowserNetworkProbeReport(report, "baseline_browser_tree_not_reaped")
	}
	restrictedToken := browserRuntimeFingerprint(struct {
		ID    string `json:"id"`
		Phase string `json:"phase"`
		At    int64  `json:"at"`
	}{request.ID, "restricted", time.Now().UnixNano()})[:24]
	restrictedGuard, err := prepareWindowsWFPProbeGuard(identity.CanonicalPath,
		[]windowsWFPRemoteTarget{harness.exact.Target()},
		"Prayu browser network probe restricted", restrictedToken)
	if err != nil {
		return finishBrowserNetworkProbeReport(report, "restricted_wfp_install_failed")
	}
	restrictedRunErr := runBrowserNetworkProbePhase(ctx, identity, harness,
		restrictedToken, false)
	restrictedCleanupErr := restrictedGuard.Close()
	restricted := harness.Observe(restrictedToken)
	report.ExactTargetObserved = restricted.Exact
	report.WrongPortDenied = baseline.WrongPort && !restricted.WrongPort
	report.WrongLoopbackAddressDenied = baseline.WrongLoopback && !restricted.WrongLoopback
	report.NonLoopbackAddressDenied = baseline.NonLoopback && !restricted.NonLoopback
	report.IPv6Denied = baseline.IPv6 && !restricted.IPv6
	report.RuleCleanupObserved = baselineGuard.CleanupVerified() &&
		restrictedCleanupErr == nil && restrictedGuard.CleanupVerified()
	if restrictedCleanupErr != nil || !restrictedGuard.CleanupVerified() {
		return finishBrowserNetworkProbeReport(report, "restricted_wfp_cleanup_failed")
	}
	if restrictedRunErr != nil {
		return finishBrowserNetworkProbeReport(report,
			browserNetworkProbeRunFailureCode("restricted", restrictedRunErr))
	}
	if !restricted.Exact {
		return finishBrowserNetworkProbeReport(report, "restricted_target_not_observed")
	}
	switch {
	case restricted.WrongPort:
		return finishBrowserNetworkProbeReport(report, "wrong_port_scope_leak")
	case restricted.WrongLoopback:
		return finishBrowserNetworkProbeReport(report, "wrong_loopback_scope_leak")
	case restricted.NonLoopback:
		return finishBrowserNetworkProbeReport(report, "non_loopback_scope_leak")
	case restricted.IPv6:
		return finishBrowserNetworkProbeReport(report, "ipv6_scope_leak")
	}
	return finishBrowserNetworkProbeReport(report, "")
}

func prepareWindowsWFPProbeGuard(executablePath string,
	targets []windowsWFPRemoteTarget, label string, operationFingerprint string,
) (*windowsWFPBrowserContainmentGuard, error) {
	if !validBrowserNetworkProbeToken(operationFingerprint) {
		return nil, ErrBrowserRuntimeBoundary
	}
	engine, filterIDs, err := installWindowsWFPBrowserFiltersForTargets(
		executablePath, targets, label)
	if err != nil {
		return nil, err
	}
	guard := &windowsWFPBrowserContainmentGuard{
		engine: engine, filterIDs: append([]uint64(nil), filterIDs...),
	}
	guard.fingerprint = browserRuntimeFingerprint(struct {
		Adapter              string   `json:"adapter"`
		OperationFingerprint string   `json:"operation_fingerprint"`
		FilterIDs            []uint64 `json:"filter_ids"`
	}{WindowsWFPBrowserContainmentAdapterName, operationFingerprint, guard.filterIDs})
	return guard, nil
}

func newBrowserNetworkProbeHarness(ctx context.Context) (*browserNetworkProbeHarness, error) {
	nonLoopback, err := selectBrowserProbeNonLoopbackAddress()
	if err != nil {
		return nil, err
	}
	harness := &browserNetworkProbeHarness{}
	created := make([]*browserNetworkProbeEndpoint, 0, 5)
	cleanup := func() {
		for _, endpoint := range created {
			endpoint.Close()
		}
	}
	newEndpoint := func(address netip.Addr,
		handler http.HandlerFunc,
	) (*browserNetworkProbeEndpoint, error) {
		endpoint, endpointErr := startBrowserNetworkProbeEndpoint(ctx, address, handler)
		if endpointErr == nil {
			created = append(created, endpoint)
		}
		return endpoint, endpointErr
	}
	canaryHandler := func(endpoint **browserNetworkProbeEndpoint) http.HandlerFunc {
		return func(writer http.ResponseWriter, request *http.Request) {
			if *endpoint != nil {
				(*endpoint).Record(request.URL.Query().Get("token"))
			}
			writer.Header().Set("Cache-Control", "no-store")
			writer.WriteHeader(http.StatusNoContent)
		}
	}
	if harness.wrongPort, err = newEndpoint(netip.MustParseAddr("127.0.0.1"),
		canaryHandler(&harness.wrongPort)); err != nil {
		cleanup()
		return nil, err
	}
	if harness.wrongLoopback, err = newEndpoint(netip.MustParseAddr("127.0.0.2"),
		canaryHandler(&harness.wrongLoopback)); err != nil {
		cleanup()
		return nil, err
	}
	if harness.nonLoopback, err = newEndpoint(nonLoopback,
		canaryHandler(&harness.nonLoopback)); err != nil {
		cleanup()
		return nil, err
	}
	if harness.ipv6, err = newEndpoint(netip.IPv6Loopback(),
		canaryHandler(&harness.ipv6)); err != nil {
		cleanup()
		return nil, err
	}
	exactHandler := func(writer http.ResponseWriter, request *http.Request) {
		token := request.URL.Query().Get("token")
		if harness.exact != nil {
			harness.exact.Record(token)
		}
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprintf(writer,
			"<!doctype html><meta charset=utf-8><title>Prayu network probe</title>"+
				"<img src=\"%s\"><img src=\"%s\"><img src=\"%s\"><img src=\"%s\">",
			html.EscapeString(harness.wrongPort.URL(token)),
			html.EscapeString(harness.wrongLoopback.URL(token)),
			html.EscapeString(harness.nonLoopback.URL(token)),
			html.EscapeString(harness.ipv6.URL(token)))
	}
	if harness.exact, err = newEndpoint(netip.MustParseAddr("127.0.0.1"), exactHandler); err != nil {
		cleanup()
		return nil, err
	}
	return harness, nil
}

func startBrowserNetworkProbeEndpoint(ctx context.Context, address netip.Addr,
	handler http.HandlerFunc,
) (*browserNetworkProbeEndpoint, error) {
	if ctx == nil || !address.IsValid() || address.IsUnspecified() {
		return nil, ErrBrowserRuntimeBoundary
	}
	network := "tcp6"
	if address.Is4() {
		network = "tcp4"
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, network,
		net.JoinHostPort(address.String(), "0"))
	if err != nil {
		return nil, err
	}
	tcpAddress, ok := listener.Addr().(*net.TCPAddr)
	if !ok || tcpAddress.Port <= 0 || tcpAddress.Port > 65535 {
		listener.Close()
		return nil, ErrBrowserRuntimeBoundary
	}
	endpoint := &browserNetworkProbeEndpoint{
		address: address, port: uint16(tcpAddress.Port), listener: listener,
		hits: make(map[string]int),
	}
	endpoint.server = &http.Server{Handler: handler, ReadHeaderTimeout: 2 * time.Second}
	go func() { _ = endpoint.server.Serve(listener) }()
	return endpoint, nil
}

func (endpoint *browserNetworkProbeEndpoint) Target() windowsWFPRemoteTarget {
	return windowsWFPRemoteTarget{Address: endpoint.address, Port: endpoint.port}
}

func (endpoint *browserNetworkProbeEndpoint) URL(token string) string {
	return "http://" + net.JoinHostPort(endpoint.address.String(),
		fmt.Sprintf("%d", endpoint.port)) + "/probe?token=" + token
}

func (endpoint *browserNetworkProbeEndpoint) Record(token string) {
	if endpoint == nil || !validBrowserNetworkProbeToken(token) {
		return
	}
	endpoint.mu.Lock()
	endpoint.hits[token]++
	endpoint.mu.Unlock()
}

func (endpoint *browserNetworkProbeEndpoint) Hit(token string) bool {
	if endpoint == nil {
		return false
	}
	endpoint.mu.Lock()
	defer endpoint.mu.Unlock()
	return endpoint.hits[token] > 0
}

func (endpoint *browserNetworkProbeEndpoint) Close() {
	if endpoint == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if endpoint.server != nil {
		_ = endpoint.server.Shutdown(ctx)
	}
	if endpoint.listener != nil {
		_ = endpoint.listener.Close()
	}
}

func (harness *browserNetworkProbeHarness) Targets() []windowsWFPRemoteTarget {
	return []windowsWFPRemoteTarget{harness.exact.Target(), harness.wrongPort.Target(),
		harness.wrongLoopback.Target(), harness.nonLoopback.Target(), harness.ipv6.Target()}
}

func (harness *browserNetworkProbeHarness) Observe(token string) browserNetworkProbeObservation {
	return browserNetworkProbeObservation{
		Exact: harness.exact.Hit(token), WrongPort: harness.wrongPort.Hit(token),
		WrongLoopback: harness.wrongLoopback.Hit(token),
		NonLoopback:   harness.nonLoopback.Hit(token), IPv6: harness.ipv6.Hit(token),
	}
}

func (observation browserNetworkProbeObservation) All() bool {
	return observation.Exact && observation.WrongPort && observation.WrongLoopback &&
		observation.NonLoopback && observation.IPv6
}

func (harness *browserNetworkProbeHarness) Close() {
	if harness == nil {
		return
	}
	for _, endpoint := range []*browserNetworkProbeEndpoint{harness.exact,
		harness.wrongPort, harness.wrongLoopback, harness.nonLoopback, harness.ipv6} {
		endpoint.Close()
	}
}

func runBrowserNetworkProbePhase(ctx context.Context, identity BrowserExecutableIdentity,
	harness *browserNetworkProbeHarness, token string, requireAll bool,
) (runErr error) {
	profilePath, err := os.MkdirTemp("", browserNetworkProbeProfilePrefix)
	if err != nil {
		return errors.Join(errBrowserNetworkProbeProfilePrepare, err)
	}
	defer func() {
		if cleanupErr := cleanupBrowserNetworkProbeProfile(profilePath); cleanupErr != nil && runErr == nil {
			runErr = errors.Join(errBrowserNetworkProbeProfileCleanup, cleanupErr)
		}
	}()
	if err := ensureProfileEnvironmentDirectories(profilePath); err != nil {
		return errors.Join(errBrowserNetworkProbeProfilePrepare, err)
	}
	startedAt := time.Now().UTC()
	deadline := startedAt.Add(BrowserNetworkProbeTimeout)
	targetURL := harness.exact.URL(token)
	spec := BrowserStartSpec{
		ProtocolVersion:               BrowserStartSpecProtocolVersion,
		ExecutableIdentityFingerprint: identity.Fingerprint,
		ExecutablePath:                identity.CanonicalPath, ExecutableSHA256: identity.ExecutableSHA256,
		ProfilePath: profilePath, Arguments: fixedBrowserNetworkProbeArguments(
			profilePath, targetURL, harness.Targets()),
		InitialURL: targetURL, ActiveProcessLimit: MaxBrowserProcessCount,
		JobMemoryLimitBytes: MaxBrowserJobMemoryBytes, LoopbackNavigationRequired: true,
		HostNameResolutionDisabled: true, NetworkDefaultDeny: true,
		CreatedAt: startedAt, RuntimeDeadline: deadline,
	}
	spec.Fingerprint = browserRuntimeFingerprint(spec)
	process, err := (windowsBrowserProcessStarter{}).Start(ctx, spec)
	if err != nil {
		return err
	}
	phaseContext, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	condition := func() bool {
		observation := harness.Observe(token)
		if requireAll {
			return observation.All()
		}
		return observation.Exact
	}
waitForCanaries:
	for !condition() {
		select {
		case <-phaseContext.Done():
			if condition() {
				break waitForCanaries
			}
			_ = process.Stop(context.Background(), true)
			return phaseContext.Err()
		case <-process.Done():
			if condition() {
				break waitForCanaries
			}
			return errBrowserNetworkProbeProcessExited
		case <-time.After(20 * time.Millisecond):
		}
	}
	if !requireAll {
		select {
		case <-phaseContext.Done():
		case <-process.Done():
		case <-time.After(2 * time.Second):
		}
	}
	stopContext, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	if err := process.Stop(stopContext, false); err != nil {
		return errors.Join(errBrowserNetworkProbeProcessStop, err)
	}
	exit, ok := process.Exit()
	if !ok || !exit.TreeReaped {
		return errBrowserNetworkProbeTreeNotReaped
	}
	return nil
}

func browserNetworkProbeRunFailureCode(phase string, runErr error) string {
	prefix := "restricted"
	if phase == "baseline" {
		prefix = "baseline"
	}
	if stage, ok := browserProcessStartFailureStage(runErr); ok {
		switch stage {
		case "executable_pin", "profile_validate", "job_create", "job_bind",
			"job_bind_after_token",
			"command_prepare", "environment_prepare", "authority_acquire",
			"process_create", "process_create_with_token", "child_authority",
			"process_resume":
			if reason := browserProcessStartFailureReason(runErr); reason != "" {
				return prefix + "_" + stage + "_" + reason
			}
			if errors.Is(runErr, ErrBrowserStandardUserTokenUnavailable) {
				return prefix + "_" + stage + "_standard_user_token_unavailable"
			}
			return prefix + "_" + stage + "_failed"
		}
	}
	switch {
	case errors.Is(runErr, errBrowserNetworkProbeProcessExited):
		return prefix + "_browser_exited_before_canaries"
	case errors.Is(runErr, ErrBrowserStandardUserTokenUnavailable):
		return prefix + "_standard_user_token_unavailable"
	case errors.Is(runErr, context.DeadlineExceeded):
		return prefix + "_canary_timeout"
	case errors.Is(runErr, context.Canceled):
		return prefix + "_probe_cancelled"
	case errors.Is(runErr, errBrowserNetworkProbeProfilePrepare):
		return prefix + "_profile_prepare_failed"
	case errors.Is(runErr, errBrowserNetworkProbeProfileCleanup):
		return prefix + "_profile_cleanup_failed"
	case errors.Is(runErr, errBrowserNetworkProbeProcessStop):
		return prefix + "_process_stop_failed"
	case errors.Is(runErr, errBrowserNetworkProbeTreeNotReaped):
		return prefix + "_process_tree_not_reaped"
	default:
		return prefix + "_runtime_failed"
	}
}

func fixedBrowserNetworkProbeArguments(profilePath string, targetURL string,
	targets []windowsWFPRemoteTarget,
) []string {
	return []string{
		"--headless=new", "--user-data-dir=" + profilePath, "--no-first-run",
		"--no-default-browser-check", "--disable-background-networking",
		"--disable-component-update", "--disable-default-apps", "--disable-extensions",
		"--disable-sync", "--disable-translate", "--disable-breakpad",
		"--disable-crash-reporter", "--metrics-recording-only", "--password-store=basic",
		"--no-proxy-server", "--host-resolver-rules=" +
			browserNetworkProbeResolverRules(targets), targetURL,
	}
}

func browserNetworkProbeResolverRules(targets []windowsWFPRemoteTarget) string {
	addresses := make([]string, 0, len(targets))
	seen := make(map[netip.Addr]struct{}, len(targets))
	for _, target := range targets {
		address := target.Address.Unmap()
		if !address.IsValid() {
			continue
		}
		if _, exists := seen[address]; exists {
			continue
		}
		seen[address] = struct{}{}
		addresses = append(addresses, address.String())
	}
	sort.Strings(addresses)
	rules := []string{"MAP * ~NOTFOUND"}
	for _, address := range addresses {
		rules = append(rules, "EXCLUDE "+address)
	}
	return strings.Join(rules, ", ")
}

func removeBrowserNetworkProbeProfile(path string) error {
	temporaryRoot := filepath.Clean(os.TempDir())
	cleaned := filepath.Clean(path)
	if !filepath.IsAbs(cleaned) || filepath.Dir(cleaned) != temporaryRoot ||
		!strings.HasPrefix(filepath.Base(cleaned), browserNetworkProbeProfilePrefix) ||
		!profilePathHasNoIndirection(cleaned) {
		return ErrBrowserRuntimeBoundary
	}
	return os.RemoveAll(cleaned)
}

func cleanupBrowserNetworkProbeProfile(path string) error {
	if err := removeBrowserNetworkProbeProfile(path); err != nil {
		return err
	}
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	return errors.New("browser network probe Profile remained after cleanup")
}

func selectBrowserProbeNonLoopbackAddress() (netip.Addr, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return netip.Addr{}, err
	}
	candidates := make([]netip.Addr, 0)
	for _, networkInterface := range interfaces {
		if networkInterface.Flags&net.FlagUp == 0 || networkInterface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, addressErr := networkInterface.Addrs()
		if addressErr != nil {
			continue
		}
		for _, raw := range addresses {
			prefix, parseErr := netip.ParsePrefix(raw.String())
			if parseErr != nil {
				continue
			}
			address := prefix.Addr().Unmap()
			if address.Is4() && !address.IsLoopback() && !address.IsUnspecified() &&
				!address.IsMulticast() && !address.IsLinkLocalUnicast() {
				candidates = append(candidates, address)
			}
		}
	}
	if len(candidates) == 0 {
		return netip.Addr{}, errors.New("no non-loopback IPv4 canary address is available")
	}
	sort.Slice(candidates, func(left int, right int) bool {
		leftPrivate, rightPrivate := candidates[left].IsPrivate(), candidates[right].IsPrivate()
		if leftPrivate != rightPrivate {
			return leftPrivate
		}
		return candidates[left].String() < candidates[right].String()
	})
	return candidates[0], nil
}
