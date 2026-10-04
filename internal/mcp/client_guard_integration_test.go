package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"cyberagent-workbench/internal/executionauth"
	"cyberagent-workbench/internal/toolcontract"
)

// These use C's approved Decision/ExecutionGuards and actual bounded-send guard.
// The mutable host state is a local authority fixture, not production policy.
func TestResolvedCGuardRevocationDuringDiscoveryInvalidatesCalls(t *testing.T) {
	var revoke, refresh atomic.Bool
	var lists, calls, authorizations atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request Envelope
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			w.WriteHeader(400)
			return
		}
		if len(request.ID) == 0 {
			w.WriteHeader(202)
			return
		}
		if request.Method == "tools/list" {
			lists.Add(1)
			if refresh.Load() {
				revoke.Store(true)
				sdkWriteResponse(w, request.ID, `{"tools":[],"nextCursor":"must-not-send"}`)
				return
			}
		}
		if request.Method == "tools/call" {
			calls.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(clientFixtureResponse(request))
	}))
	defer server.Close()
	declaration, host := testResolvedDeclaration(t)
	declaration.Transport, declaration.Stdio = toolcontract.TransportStreamableHTTP, nil
	declaration.HTTP = &toolcontract.HTTPLaunch{Endpoint: server.URL}
	launch, err := ResolveLaunch(declaration, host)
	if err != nil {
		t.Fatal(err)
	}
	input, _ := toolcontract.FingerprintLaunch(resolvedTestKey, launch)
	connect := testResolvedOperation(toolcontract.OperationConnect, launch.Component, input)
	probe := &resolvedGuardProbe{}
	guards := probe.guards(t, connect)
	guards.BeginDiscovery = func(_ context.Context, scope toolcontract.DiscoveryScope) (toolcontract.DiscoverySendGuard, error) {
		authorizations.Add(1)
		return toolcontract.NewDiscoverySendGuard(scope, func(context.Context) error {
			if revoke.Load() {
				return errors.New("host discovery authority revoked")
			}
			return nil
		})
	}
	client, err := NewResolvedClient(launch, resolvedTestKey, connect, guards, nil, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.DiscoverWithScope(t.Context(), testResolvedScope(t, launch, 4)); err != nil {
		t.Fatal(err)
	}
	operation := resolvedFixtureCall(t, client, launch, "{}")
	refresh.Store(true)
	if _, err := client.DiscoverWithScope(t.Context(), testResolvedScope(t, launch, 4)); err == nil {
		t.Fatal("revoked discovery completed")
	}
	_, native, receipt, err := client.CallToolWithReceipt(t.Context(), operation, "lookup", json.RawMessage("{}"))
	if err == nil || receipt.State != toolcontract.ReceiptNotDispatched || len(native) != 0 || calls.Load() != 0 || lists.Load() != 2 || authorizations.Load() != 2 {
		t.Fatalf("revocation/receipt/one discovery authorization: %v %v lists=%d calls=%d starts=%d", receipt, err, lists.Load(), calls.Load(), authorizations.Load())
	}
}

func TestResolvedCDecisionRejectsCredentialDriftAndDuplicateOperation(t *testing.T) {
	for _, scenario := range []string{"credential-revision", "duplicate-operation"} {
		t.Run(scenario, func(t *testing.T) {
			var calls, credentialReads atomic.Int32
			var currentRevision atomic.Int32
			currentRevision.Store(1)
			nativeResult := `{"content":[{"type":"text","text":"received"}],"structuredContent":{"large":9007199254740993},"_meta":{"vendor":"retained"}}`
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request Envelope
				if json.NewDecoder(r.Body).Decode(&request) != nil {
					w.WriteHeader(400)
					return
				}
				if len(request.ID) == 0 {
					w.WriteHeader(202)
					return
				}
				if request.Method == "tools/call" {
					calls.Add(1)
					sdkWriteResponse(w, request.ID, nativeResult)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(clientFixtureResponse(request))
			}))
			defer server.Close()
			declaration, host := testResolvedDeclaration(t)
			declaration.Transport, declaration.Stdio = toolcontract.TransportStreamableHTTP, nil
			declaration.HTTP = &toolcontract.HTTPLaunch{Endpoint: server.URL, Credential: &toolcontract.CredentialRef{ID: "fixture", Revision: "1"}}
			launch, err := ResolveLaunch(declaration, host)
			if err != nil {
				t.Fatal(err)
			}
			input, _ := toolcontract.FingerprintLaunch(resolvedTestKey, launch)
			connect := testResolvedOperation(toolcontract.OperationConnect, launch.Component, input)
			probe := &resolvedGuardProbe{}
			guards := probe.guards(t, connect)
			var consumed atomic.Bool
			var approvedFingerprint string
			decision := executionauth.Decision{Outcome: "allow", ReasonCode: "fixture_authority", AuthorizationRef: "fixture-auth",
				BeforeDispatch: func(_ context.Context, actual string) error {
					if actual != approvedFingerprint || currentRevision.Load() != 1 {
						return errors.New("host credential revision or operation changed")
					}
					if !consumed.CompareAndSwap(false, true) {
						return errors.New("operation already consumed")
					}
					return nil
				}}
			if err := decision.Validate(); err != nil {
				t.Fatal(err)
			}
			guards.BeforeToolCall = decision.BeforeDispatch
			client, err := NewResolvedClient(launch, resolvedTestKey, connect, guards, func(_ context.Context, ref toolcontract.CredentialRef) (string, error) {
				credentialReads.Add(1)
				if ref.Revision != "1" || currentRevision.Load() != 1 {
					return "", errors.New("credential revision unavailable")
				}
				return "fixture-token", nil
			}, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			if _, err := client.DiscoverWithScope(t.Context(), testResolvedScope(t, launch, 3)); err != nil {
				t.Fatal(err)
			}
			operation := resolvedFixtureCall(t, client, launch, "{}")
			approvedFingerprint, _ = toolcontract.FingerprintOperation(operation)
			if scenario == "credential-revision" {
				currentRevision.Store(2)
			} else {
				result, native, receipt, err := client.CallToolWithReceipt(t.Context(), operation, "lookup", json.RawMessage("{}"))
				if err != nil || result == nil || receipt.State != toolcontract.ReceiptResultReceived || !bytes.Equal(native, []byte(nativeResult)) {
					t.Fatalf("typed/native receipt lost: %v %v", receipt, err)
				}
			}
			_, native, receipt, err := client.CallToolWithReceipt(t.Context(), operation, "lookup", json.RawMessage("{}"))
			wantCalls := int32(0)
			if scenario == "duplicate-operation" {
				wantCalls = 1
			}
			if err == nil || receipt.State != toolcontract.ReceiptNotDispatched || len(native) != 0 || calls.Load() != wantCalls || credentialReads.Load() != 1 || strings.Contains(err.Error(), "fixture-token") {
				t.Fatalf("C guard did not prevent a new send: %v %v calls=%d credentials=%d", receipt, err, calls.Load(), credentialReads.Load())
			}
		})
	}
}
