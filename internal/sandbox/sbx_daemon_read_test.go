package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestSBXDaemonReadMethodsUseOnlyTheCompiledLocalRoutes(t *testing.T) {
	for _, test := range []struct {
		name, path, body, want string
		read                   func(*sbxDaemonHTTPTransport, context.Context) ([]byte, error)
	}{
		{"inventory", "/sandbox", `[{"id":"00000000-0000-4000-8000-000000000001","name":"owned","status":"running","workspace":"/workspace"}]`,
			`[{"id":"00000000-0000-4000-8000-000000000001","name":"owned","workspaces":["/workspace"]}]`,
			func(transport *sbxDaemonHTTPTransport, ctx context.Context) ([]byte, error) {
				return transport.Inventory(ctx)
			}},
		{"ssh", "/daemon/settings/ssh.agentForwardingEnabled", `{"key":"ssh.agentForwardingEnabled","source":"override","type":"bool","value":false,"requires_restart":true}`,
			`{"key":"ssh.agentForwardingEnabled","source":"override","type":"bool","value":false,"requires_restart":true}`,
			func(transport *sbxDaemonHTTPTransport, ctx context.Context) ([]byte, error) {
				return transport.IsolationSetting(ctx, "ssh.agentForwardingEnabled")
			}},
		{"gateway", "/daemon/settings/mcp.forceLocalGateway", `{"key":"mcp.forceLocalGateway","source":"override","type":"bool","value":true,"requires_restart":true}`,
			`{"key":"mcp.forceLocalGateway","source":"override","type":"bool","value":true,"requires_restart":true}`,
			func(transport *sbxDaemonHTTPTransport, ctx context.Context) ([]byte, error) {
				return transport.IsolationSetting(ctx, "mcp.forceLocalGateway")
			}},
	} {
		t.Run(test.name, func(t *testing.T) {
			requests := 0
			transport := &sbxDaemonHTTPTransport{client: &http.Client{Transport: sbxDaemonRoundTrip(func(request *http.Request) (*http.Response, error) {
				requests++
				if request.Method != http.MethodGet || request.URL.Host != "sbx.local" || request.URL.Scheme != "http" ||
					request.URL.Path != test.path || request.URL.RawQuery != "" || request.Body != nil ||
					request.Header.Get("Authorization") != "" || request.Header.Get("Accept") != "application/json" {
					t.Fatal("read escaped its compiled route or carried caller input")
				}
				return sbxDaemonTestResponse(http.StatusOK, test.body), nil
			})}}
			body, err := test.read(transport, t.Context())
			if err != nil || requests != 1 || string(body) != test.want {
				t.Fatalf("read changed the validated JSON projection: requests=%d error=%v", requests, err)
			}
		})
	}
}

func TestSBXDaemonReadInventoryPreservesNativeMountAuthority(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	gitDirectory := filepath.Join(workspace, ".git")
	for _, test := range []struct {
		name     string
		readOnly bool
		extra    bool
		match    bool
	}{
		{"expected-readonly-git", true, false, true},
		{"writable-git", false, false, false},
		{"extra-mount", true, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			additional := []map[string]any{{"dir": gitDirectory, "read_only": test.readOnly}}
			if test.extra {
				additional = append(additional, map[string]any{"dir": filepath.Join(workspace, "extra")})
			}
			body, err := json.Marshal([]map[string]any{{
				"id": "immutable-owned-id", "name": "owned", "workspace": workspace,
				"additional_workspaces": additional,
				// These optional debug entries cannot override the advertised
				// workspace permissions used by the CLI inventory contract.
				"runtime_mounts":        []map[string]any{{"host_path": gitDirectory, "read_only": !test.readOnly}},
				"future_additive_field": true,
			}})
			if err != nil {
				t.Fatal(err)
			}
			transport := &sbxDaemonHTTPTransport{client: &http.Client{Transport: sbxDaemonRoundTrip(func(*http.Request) (*http.Response, error) {
				return sbxDaemonTestResponse(http.StatusOK, string(body)), nil
			})}}
			backend := &SBXBackend{daemon: transport}
			entries, err := backend.inventory(t.Context())
			if err != nil || len(entries) != 1 || entries[0].ID != "immutable-owned-id" || entries[0].Name != "owned" {
				t.Fatalf("native identity could not reach the inventory validator: %v", err)
			}
			if sbxMountsMatch(entries[0], sbxRecord{Workspace: workspace}) != test.match {
				t.Fatal("native mount permissions or an additional mount changed authority")
			}
		})
	}
}

func TestSBXDaemonReadInventoryRejectsNonNativeAndMalformedMountShapes(t *testing.T) {
	for _, body := range []string{
		`null`, `{}`, `{"sandboxes":[]}`,
		`[{"id":"one","name":"owned","workspace":42}]`,
		`[{"id":"one","name":"owned","workspace":"/workspace","additional_workspaces":[{"dir":"/workspace/.git","read_only":"true"}]}]`,
	} {
		transport := &sbxDaemonHTTPTransport{client: &http.Client{Transport: sbxDaemonRoundTrip(func(*http.Request) (*http.Response, error) {
			return sbxDaemonTestResponse(http.StatusOK, body), nil
		})}}
		if projected, err := transport.Inventory(t.Context()); projected != nil || !errors.Is(err, ErrSBXBoundary) {
			t.Fatal("non-native inventory or malformed mount evidence was accepted")
		}
	}
}

func TestSBXDaemonReadRejectsUncompiledKeysAndCancelledWaitersBeforeIO(t *testing.T) {
	requests := 0
	transport := &sbxDaemonHTTPTransport{client: &http.Client{Transport: sbxDaemonRoundTrip(func(*http.Request) (*http.Response, error) {
		requests++
		return sbxDaemonTestResponse(http.StatusOK, `{}`), nil
	})}}
	for _, key := range []string{"", "proxy", "../secret", "mcp.forceLocalGateway?all=true", "SSH.agentForwardingEnabled"} {
		if body, err := transport.IsolationSetting(t.Context(), key); body != nil || !errors.Is(err, ErrSBXBoundary) {
			t.Fatal("caller-selected setting or URL was accepted")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, read := range []func(context.Context) ([]byte, error){transport.Inventory, func(ctx context.Context) ([]byte, error) {
		return transport.IsolationSetting(ctx, "mcp.forceLocalGateway")
	}} {
		if body, err := read(ctx); body != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("cancelled read was dispatched")
		}
		if body, err := read(nil); body != nil || !errors.Is(err, ErrSBXBoundary) {
			t.Fatal("nil context was accepted")
		}
	}
	if requests != 0 {
		t.Fatal("invalid read reached the daemon transport")
	}
}

func TestSBXDaemonReadRefusesStatusFailuresAmbiguousJSONAndOversizeBodies(t *testing.T) {
	for _, reader := range []struct {
		name  string
		limit int
		read  func(*sbxDaemonHTTPTransport, context.Context) ([]byte, error)
	}{
		{"inventory", 1024 * 1024, func(transport *sbxDaemonHTTPTransport, ctx context.Context) ([]byte, error) {
			return transport.Inventory(ctx)
		}},
		{"setting", sbxDaemonBodyLimit, func(transport *sbxDaemonHTTPTransport, ctx context.Context) ([]byte, error) {
			return transport.IsolationSetting(ctx, "mcp.forceLocalGateway")
		}},
	} {
		for _, test := range []struct {
			name   string
			status int
			body   string
			want   error
		}{
			{"unauthorized", http.StatusUnauthorized, `{"error":"synthetic-private-error"}`, ErrSBXUnavailable},
			{"redirect", http.StatusTemporaryRedirect, `{}`, ErrSBXUnavailable},
			{"unexpected-success", http.StatusAccepted, `{}`, ErrSBXUnavailable},
			{"duplicate-field", http.StatusOK, `[{"id":"one","id":"two"}]`, ErrSBXBoundary},
			{"multiple-values", http.StatusOK, `[] {}`, ErrSBXBoundary},
			{"invalid-utf8", http.StatusOK, "[\"\xff\"]", ErrSBXBoundary},
			{"empty", http.StatusOK, "", ErrSBXBoundary},
			{"oversize", http.StatusOK, strings.Repeat(" ", reader.limit+1), ErrSBXOutputLimit},
		} {
			t.Run(reader.name+"/"+test.name, func(t *testing.T) {
				transport := &sbxDaemonHTTPTransport{client: &http.Client{Transport: sbxDaemonRoundTrip(func(*http.Request) (*http.Response, error) {
					return sbxDaemonTestResponse(test.status, test.body), nil
				})}}
				body, err := reader.read(transport, t.Context())
				if body != nil || !errors.Is(err, test.want) || strings.Contains(err.Error(), "synthetic-private-error") {
					t.Fatalf("unsafe read response was exposed: error=%v", err)
				}
			})
		}
	}
}

func TestSBXDaemonReadDefaultClientNeverFollowsRedirects(t *testing.T) {
	transport, supported := newSBXDaemonTransport().(*sbxDaemonHTTPTransport)
	if !supported {
		t.Skip("fixed local endpoint is unavailable on this platform")
	}
	requests := 0
	transport.client.Transport = sbxDaemonRoundTrip(func(*http.Request) (*http.Response, error) {
		requests++
		response := sbxDaemonTestResponse(http.StatusTemporaryRedirect, "")
		response.Header.Set("Location", "https://unrelated.invalid/secret")
		return response, nil
	})
	if body, err := transport.Inventory(t.Context()); body != nil || !errors.Is(err, ErrSBXUnavailable) || requests != 1 {
		t.Fatal("daemon read followed a redirect or accepted its response")
	}
}

func TestSBXDaemonReadPropagatesCallerCancellation(t *testing.T) {
	for _, responseReceived := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		transport := &sbxDaemonHTTPTransport{client: &http.Client{Transport: sbxDaemonRoundTrip(func(request *http.Request) (*http.Response, error) {
			cancel()
			<-request.Context().Done()
			if responseReceived {
				return sbxDaemonTestResponse(http.StatusOK, `[]`), nil
			}
			return nil, request.Context().Err()
		})}}
		if body, err := transport.Inventory(ctx); body != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("in-flight read lost cancellation")
		}
	}
}
