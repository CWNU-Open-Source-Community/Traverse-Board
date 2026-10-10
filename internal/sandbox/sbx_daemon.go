package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SBXDaemonTransport is the narrow, fixed-namespace local lifecycle contract.
// It carries no endpoints, credentials, policies or caller-supplied HTTP input.
// The version-scoped API shape comes from Docker's signed sbx v0.47.0 binary:
// sandboxapi.DeleteSandboxParams{Force,ExpectedId,DeleteScopedSecrets} and
// DeleteSandboxIfID -> deleteRuntimeWithLifecycleGateHeldIfID. Live acceptance
// must independently establish wrong-ID refusal and whole-VM deletion.
type SBXDaemonTransport interface {
	Check(context.Context) error
	Inventory(context.Context) ([]byte, error)
	IsolationSetting(context.Context, string) ([]byte, error)
	Remove(context.Context, string, string) error
	VerifyHelper(context.Context, string, string, string) error
}

const (
	sbxDaemonVersion    = "v0.47.0"
	sbxDaemonAPIVersion = "0.38.0"
	sbxDaemonBodyLimit  = 8 * 1024
)

type sbxDaemonHTTPTransport struct {
	client *http.Client
}

func (transport *sbxDaemonHTTPTransport) VerifyHelper(ctx context.Context, name, executable, argument string) error {
	return sbxVerifyHelperRegistration(ctx, name, executable, argument)
}

func (transport *sbxDaemonHTTPTransport) Check(ctx context.Context) error {
	if transport == nil || transport.client == nil || ctx == nil {
		return ErrSBXUnavailable
	}
	bounded, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	response, body, err := transport.request(bounded, http.MethodGet, "/daemon/health", nil)
	if err != nil || response != http.StatusOK || sbxUniqueJSON(body) != nil {
		return errors.Join(ErrSBXUnavailable, err)
	}
	var health struct {
		APIVersion string `json:"api_version"`
		Release    bool   `json:"release"`
		Revision   string `json:"revision"`
		Status     string `json:"status"`
		Version    string `json:"version"`
	}
	if json.Unmarshal(body, &health) != nil || health.Version != sbxDaemonVersion ||
		health.APIVersion != sbxDaemonAPIVersion || !health.Release || health.Status != "healthy" ||
		len(health.Revision) != 40 || !sbxSHA(health.Revision+strings.Repeat("0", 24)) {
		return ErrSBXUnavailable
	}
	return nil
}

func (transport *sbxDaemonHTTPTransport) Remove(ctx context.Context, name, id string) error {
	if ctx == nil || !sbxOwnedSandboxName(name) || !sbxUUID(id) {
		return ErrSBXOwnership
	}
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// Check the actual daemon, rather than infer its version from the CLI. A
	// stale/different daemon is not allowed to interpret conditional removal.
	if err := transport.Check(bounded); err != nil {
		return errors.Join(ErrSBXCleanup, err)
	}
	query := url.Values{"force": {"true"}, "expected_id": {id}, "delete_scoped_secrets": {"false"}}
	status, body, err := transport.request(bounded, http.MethodDelete, "/sandbox/"+name, query)
	if err != nil {
		return errors.Join(ErrSBXCleanup, err)
	}
	if status == http.StatusConflict || status == http.StatusPreconditionFailed {
		return ErrSBXOwnership
	}
	if status != http.StatusOK && status != http.StatusNoContent {
		// In particular, 404 does not prove removal: an unhealthy backend can
		// lose its inventory while the VM and exact runtime metadata survive.
		return ErrSBXCleanup
	}
	if status == http.StatusNoContent && len(strings.TrimSpace(string(body))) != 0 {
		return ErrSBXCleanup
	}
	if status == http.StatusOK && len(strings.TrimSpace(string(body))) != 0 {
		if sbxUniqueJSON(body) != nil {
			return ErrSBXCleanup
		}
		var object map[string]json.RawMessage
		if json.Unmarshal(body, &object) != nil || object == nil {
			return ErrSBXCleanup
		}
	}
	// This is the conditional API receipt. The owner still checks that both
	// the recorded UUID and reserved name are absent before journaling removed.
	return nil
}

func (transport *sbxDaemonHTTPTransport) request(ctx context.Context, method, path string, query url.Values) (int, []byte, error) {
	return transport.requestBounded(ctx, method, path, query, sbxDaemonBodyLimit)
}

func (transport *sbxDaemonHTTPTransport) requestBounded(ctx context.Context, method, path string, query url.Values, limit int64) (int, []byte, error) {
	if transport == nil || transport.client == nil || ctx == nil || limit <= 0 || limit > 1024*1024 {
		return 0, nil, ErrSBXBoundary
	}
	endpoint := "http://sbx.local" + path
	if len(query) != 0 {
		endpoint += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		return 0, nil, ErrSBXCLI
	}
	request.Header.Set("Accept", "application/json")
	response, err := transport.client.Do(request)
	if err != nil {
		// Raw errors or daemon payloads are never reflected into product output.
		return 0, nil, errors.Join(ErrSBXCLI, ctx.Err())
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return 0, nil, ErrSBXCLI
	}
	if int64(len(body)) > limit {
		return 0, nil, ErrSBXOutputLimit
	}
	return response.StatusCode, body, nil
}

func sbxOwnedSandboxName(name string) bool {
	return strings.HasPrefix(name, "traverse-sbx-") && len(name) == 45 &&
		sbxSHA(name[13:]+name[13:])
}

func sbxUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for index, character := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			if character != '-' {
				return false
			}
			continue
		}
		if !(character >= '0' && character <= '9' || character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}
