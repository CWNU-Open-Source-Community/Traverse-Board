package mcp

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// Run the unmodified native server through a real child-process pipe. A mock
// handshake would miss its strict initialize and tools/call parameter decoding.
func TestSDKLegacyClientInteroperatesWithNativeServer(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	descriptor := clientTransportDescriptor(TransportStdio, executable, []string{"-test.run=^TestSDKNativeStdioHelper$", "--", "sdk-native-server"})
	descriptor.DeclaredCapabilities = []CapabilityKind{CapabilityTools, CapabilityResources}
	transport, err := newStdioClientTransport(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	client := newClient(transport, descriptor)
	defer client.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	snapshot, err := client.Discover(ctx, time.Now())
	if err != nil || snapshot.ServerName != ServerName || len(snapshot.Tools) != 2 || len(snapshot.Resources) == 0 {
		t.Fatalf("native discovery: %#v %v", snapshot, err)
	}
	result, err := client.CallTool(ctx, "list_workspace", json.RawMessage("{}"), 4096)
	if err != nil || result.IsError || !strings.Contains(result.Content, "completed") {
		t.Fatalf("native tool call: %#v %v", result, err)
	}
	if ProtocolVersion != "2025-06-18" {
		t.Fatal("outgoing client migration changed native server protocol")
	}
}

func TestSDKNativeStdioHelper(t *testing.T) {
	if !slices.Contains(os.Args, "sdk-native-server") {
		t.Skip("helper subprocess only")
	}
	server, _ := newTestServer(t)
	if err := server.Serve(t.Context(), os.Stdin, os.Stdout); err != nil {
		t.Fatal(err)
	}
}
