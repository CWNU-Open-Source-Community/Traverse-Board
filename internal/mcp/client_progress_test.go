package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"testing"
	"time"
)

// Regression: unsolicited notifications must not replace or exhaust the wait
// for the response carrying this request's ID.
func TestUCProbeStdioProgressDoesNotConsumeResponseBudget(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	descriptor := clientTransportDescriptor(TransportStdio, executable,
		[]string{"-test.run=^TestUCProbeHelperProcess$", "--", "uc-progress-probe"})
	transport, err := newStdioClientTransport(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	client := newClient(transport, descriptor)
	defer client.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if _, err := client.Discover(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	request := ucProbeRequest()
	response, err := transport.Exchange(ctx, request)
	if err != nil {
		t.Fatalf("40 valid progress notifications exhausted response wait: %v", err)
	}
	if !bytes.Equal(response.ID, request.ID) || !bytes.Contains(response.Result, []byte("remote-result")) {
		t.Fatalf("response was not the tools/call result: %#v", response)
	}
}

func TestUCProbeHTTPProgressDoesNotReplaceResponse(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request Envelope
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: message\ndata: %s\n\n", ucProbeProgress(1))
		w.(http.Flusher).Flush()
		fmt.Fprintf(w, "event: message\ndata: %s\n\n", clientFixtureResponse(request))
	}))
	defer server.Close()
	descriptor := clientTransportDescriptor(TransportStreamableHTTP, server.URL, nil)
	transport, err := newRemoteClientTransport(descriptor, "", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	request := ucProbeRequest()
	response, err := transport.Exchange(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(response.ID, request.ID) || !bytes.Contains(response.Result, []byte("remote-result")) {
		t.Fatalf("progress event replaced the requested result: method=%q id=%s result=%s", response.Method, response.ID, response.Result)
	}
}

func ucProbeRequest() Envelope {
	return Envelope{JSONRPC: "2.0", ID: json.RawMessage(`77`), Method: "tools/call",
		Params: json.RawMessage(`{"name":"lookup","arguments":{"query":"one"},"_meta":{"progressToken":"uc-progress"}}`)}
}

func ucProbeProgress(index int) []byte {
	return []byte(fmt.Sprintf(`{"jsonrpc":"2.0","method":"notifications/progress","params":{"progressToken":"uc-progress","progress":%d,"total":40}}`, index))
}

func TestUCProbeHelperProcess(t *testing.T) {
	if !slices.Contains(os.Args, "uc-progress-probe") {
		t.Skip("helper subprocess only")
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), MaxMessageBytes)
	for scanner.Scan() {
		request, err := DecodeEnvelope(scanner.Bytes())
		if err != nil {
			return
		}
		if len(request.ID) == 0 {
			continue
		}
		if request.Method == "tools/call" {
			for i := 1; i <= 40; i++ {
				fmt.Fprintln(os.Stdout, string(ucProbeProgress(i)))
			}
		}
		fmt.Fprintln(os.Stdout, string(clientFixtureResponse(request)))
	}
}
