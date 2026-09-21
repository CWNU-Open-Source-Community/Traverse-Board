package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"cyberagent-workbench/internal/hostproxy"
)

const maxCommandRuntimeHostProxyRoutes = 4

// A manager owns bridges for the static proxy configurations observed during
// its lifetime. A running command keeps its original route when Windows proxy
// settings change; a new command gets a distinct bridge and fingerprint.
type commandRuntimeHostProxySet struct {
	mu         sync.Mutex
	bridges    map[string]*hostproxy.Bridge
	closed     bool
	readConfig func() (hostproxy.Config, bool)
}

func (p *commandRuntimeHostProxySet) acquire(config hostproxy.Config) (*hostproxy.Bridge, error) {
	fingerprint, err := hostproxy.Fingerprint(config)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, ErrCommandRuntimeUnavailable
	}
	if bridge := p.bridges[fingerprint]; bridge != nil {
		return bridge, nil
	}
	if len(p.bridges) >= maxCommandRuntimeHostProxyRoutes {
		return nil, fmt.Errorf("%w: too many system proxy changes during this process", ErrCommandRuntimeUnavailable)
	}
	bridge, err := hostproxy.Start(config)
	if err != nil {
		return nil, fmt.Errorf("%w: managed host proxy could not start", ErrCommandRuntimeUnavailable)
	}
	if p.bridges == nil {
		p.bridges = make(map[string]*hostproxy.Bridge)
	}
	p.bridges[fingerprint] = bridge
	return bridge, nil
}

func (p *commandRuntimeHostProxySet) close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	bridges := make([]*hostproxy.Bridge, 0, len(p.bridges))
	for _, bridge := range p.bridges {
		bridges = append(bridges, bridge)
	}
	p.mu.Unlock()
	var result error
	for _, bridge := range bridges {
		result = errors.Join(result, bridge.Close())
	}
	return result
}

// NormalizeCommandRuntimeSpec binds the effective proxy environment to the
// same manager that will own the Job. The bridge's random port and stable route
// fingerprint both enter EnvironmentSHA256 and the durable request identity.
func (m *CommandRuntimeManager) NormalizeCommandRuntimeSpec(spec CommandRuntimeSpec,
	workspaceRoot string,
) (CommandRuntimeResolvedSpec, error) {
	// The manager reads one trusted system proxy snapshot below. Calling the
	// ordinary normalizer first would read it twice and could retain a stale
	// static fallback if Windows switches to PAC between those reads.
	resolved, err := normalizeCommandRuntimeSpec(spec, workspaceRoot, false)
	if err != nil || m == nil || m.hostProxy == nil ||
		resolved.Spec.Network != CommandRuntimeNetworkHost {
		return resolved, err
	}
	for _, entry := range spec.Environment {
		if commandRuntimeProxyEnvironmentName(entry.Name) {
			return resolved, nil // explicit per-command proxy choice wins
		}
	}
	readConfig := m.hostProxy.readConfig
	if readConfig == nil {
		readConfig = commandRuntimePlatformHostProxy
	}
	config, available := readConfig()
	if !available {
		return resolved, nil
	}
	bridge, err := m.hostProxy.acquire(config)
	if err != nil {
		return CommandRuntimeResolvedSpec{}, err
	}
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY"} {
		resolved.Environment = replaceCommandRuntimeEnvironment(resolved.Environment,
			name, bridge.URL())
	}
	// The bridge, rather than each client's incompatible NO_PROXY parser,
	// evaluates the complete Windows bypass list on every HTTP request.
	resolved.Environment = replaceCommandRuntimeEnvironment(resolved.Environment,
		"NO_PROXY", "")
	resolved.Environment = replaceCommandRuntimeEnvironment(resolved.Environment,
		"NODE_USE_ENV_PROXY", "1")
	resolved.Environment = replaceCommandRuntimeEnvironment(resolved.Environment,
		"CYBERAGENT_HOST_PROXY_ROUTE_SHA256", bridge.Fingerprint())
	sort.Slice(resolved.Environment, func(left, right int) bool {
		leftKey, _, _ := strings.Cut(resolved.Environment[left], "=")
		rightKey, _, _ := strings.Cut(resolved.Environment[right], "=")
		return strings.ToLower(leftKey) < strings.ToLower(rightKey)
	})
	encoded, err := json.Marshal(resolved.Environment)
	if err != nil {
		return CommandRuntimeResolvedSpec{}, ErrCommandRuntimeBoundary
	}
	digest := sha256.Sum256(encoded)
	resolved.EnvironmentSHA256 = hex.EncodeToString(digest[:])
	return resolved, nil
}
