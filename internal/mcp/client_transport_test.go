package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

type blockingMCPWriteCloser struct {
	closed chan struct{}
}

func (w *blockingMCPWriteCloser) Write([]byte) (int, error) {
	<-w.closed
	return 0, io.ErrClosedPipe
}

func (w *blockingMCPWriteCloser) Close() error {
	select {
	case <-w.closed:
	default:
		close(w.closed)
	}
	return nil
}

func TestRemoteClientTransportPerformsTLSHandshakeDiscoveryAndCall(t *testing.T) {
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		if request.Method != http.MethodPost || request.Header.Get("Authorization") != "Bearer fixture-token" {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		var envelope Envelope
		if err := json.NewDecoder(request.Body).Decode(&envelope); err != nil {
			http.Error(writer, "invalid", http.StatusBadRequest)
			return
		}
		if len(envelope.ID) == 0 {
			writer.WriteHeader(http.StatusAccepted)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Mcp-Session-Id", "session-one")
		_, _ = writer.Write(clientFixtureResponse(envelope))
	}))
	defer server.Close()
	descriptor := clientTransportDescriptor(TransportStreamableHTTP, server.URL, nil)
	transport, err := newRemoteClientTransport(descriptor, "fixture-token", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	client := newClient(transport, descriptor)
	defer client.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	capabilities, err := client.Discover(ctx, time.Now())
	if err != nil || len(capabilities.Tools) != 1 || capabilities.Tools[0].Name != "lookup" {
		t.Fatalf("remote discovery=%#v err=%v", capabilities, err)
	}
	result, err := client.CallTool(ctx, "lookup", json.RawMessage(`{"query":"one"}`), 4096)
	if err != nil || !strings.Contains(result.Content, "remote-result") || requests < 4 {
		t.Fatalf("remote call=%#v requests=%d err=%v", result, requests, err)
	}
}

func TestStdioClientTransportRunsApprovedAbsoluteExecutable(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	descriptor := clientTransportDescriptor(TransportStdio, executable,
		[]string{"-test.run=^TestMCPStdioHelperProcess$", "--", "mcp-stdio-helper"})
	transport, err := newStdioClientTransport(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	client := newClient(transport, descriptor)
	defer client.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	capabilities, err := client.Discover(ctx, time.Now())
	if err != nil || len(capabilities.Tools) != 1 {
		t.Fatalf("stdio discovery=%#v err=%v", capabilities, err)
	}
	result, err := client.CallTool(ctx, "lookup", json.RawMessage(`{"query":"two"}`), 4096)
	if err != nil || !strings.Contains(result.Content, "stdio-result") {
		t.Fatalf("stdio call=%#v err=%v", result, err)
	}
}

func TestStdioClientTransportCancelsBlockedWrite(t *testing.T) {
	writer := &blockingMCPWriteCloser{closed: make(chan struct{})}
	transport := &stdioClientTransport{stdin: writer, done: writer.closed,
		stderr: newBoundedBuffer(128)}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	err := transport.write(ctx, Envelope{JSONRPC: "2.0", ID: json.RawMessage(`1`),
		Method: "tools/call"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked stdio write error=%v, want deadline exceeded", err)
	}
}

func TestMCPStdioHelperProcess(t *testing.T) {
	if !slices.Contains(os.Args, "mcp-stdio-helper") {
		t.Skip("helper subprocess only")
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), MaxMessageBytes)
	for scanner.Scan() {
		envelope, err := DecodeEnvelope(scanner.Bytes())
		if err != nil {
			return
		}
		if len(envelope.ID) == 0 {
			continue
		}
		response := clientFixtureResponseWithText(envelope, "stdio-result")
		_, _ = fmt.Fprintln(os.Stdout, string(response))
	}
}

func clientTransportDescriptor(kind TransportKind, target string,
	arguments []string,
) ServerDescriptor {
	return ServerDescriptor{ProtocolVersion: ClientProtocolVersion, ID: "fixture",
		Name: "Fixture", Transport: kind, Target: target, Arguments: arguments,
		DeclaredCapabilities: []CapabilityKind{CapabilityTools}, Scope: ScopeWorkspace,
		WorkspaceID: "workspace-1", Source: Source{Kind: "manual", URI: "test://fixture"},
		CallTimeoutMillis: 5_000, MaxResultBytes: 4096}
}

func clientFixtureResponse(request Envelope) []byte {
	return clientFixtureResponseWithText(request, "remote-result")
}

func clientFixtureResponseWithText(request Envelope, text string) []byte {
	var result any
	switch request.Method {
	case "initialize":
		result = map[string]any{"protocolVersion": ProtocolVersion,
			"capabilities": map[string]any{"tools": map[string]any{}},
			"serverInfo":   map[string]string{"name": "fixture", "version": "1.0.0"}}
	case "tools/list":
		result = map[string]any{"tools": []map[string]any{{"name": "lookup",
			"description": "Look up a fixture.",
			"inputSchema": json.RawMessage(`{"type":"object","additionalProperties":false,"required":["query"],"properties":{"query":{"type":"string"}}}`)}}}
	case "tools/call":
		result = map[string]any{"content": []map[string]string{{"type": "text", "text": text}}}
	default:
		result = map[string]any{}
	}
	resultRaw, _ := json.Marshal(result)
	responseRaw, _ := json.Marshal(Envelope{JSONRPC: "2.0",
		ID: append(json.RawMessage(nil), request.ID...), Result: resultRaw})
	return responseRaw
}
