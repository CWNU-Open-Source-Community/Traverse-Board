package sbxmcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMain(m *testing.M) {
	if handled, code := Execute(os.Args[1:], os.Stdin, os.Stdout); handled {
		os.Exit(code)
	}
	os.Exit(m.Run())
}

func TestExecuteAcceptsOnlyTheFixedHelperInvocation(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		handled bool
		code    int
	}{
		{nil, false, 0},
		{[]string{"--version"}, false, 0},
		{[]string{Arg}, true, 0},
		{[]string{Arg, "--version"}, true, 2},
		{[]string{"mcp", Arg}, true, 2},
		{[]string{Arg + "=serve"}, true, 2},
		{[]string{Arg, Arg}, true, 2},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			var output bytes.Buffer
			handled, code := Execute(tc.args, strings.NewReader(""), &output)
			if handled != tc.handled || code != tc.code || output.Len() != 0 {
				t.Fatalf("dispatch = (%t, %d), output bytes = %d", handled, code, output.Len())
			}
		})
	}
	if handled, code := Execute([]string{Arg}, nil, io.Discard); !handled || code != 2 {
		t.Fatal("nil stream was accepted")
	}
}

const initialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"regression","version":"1"}}}`
const initialized = `{"jsonrpc":"2.0","method":"notifications/initialized"}`

type testReply struct {
	JSONRPC string                     `json:"jsonrpc"`
	ID      json.RawMessage            `json:"id"`
	Result  map[string]json.RawMessage `json:"result"`
	Error   *rpcError                  `json:"error"`
}

func transcript(t *testing.T, frames ...string) []testReply {
	t.Helper()
	var output bytes.Buffer
	handled, code := Execute([]string{Arg}, strings.NewReader(strings.Join(frames, "\n")+"\n"), &output)
	if !handled || code != 0 {
		t.Fatalf("stream ended with %d", code)
	}
	decoder := json.NewDecoder(&output)
	var replies []testReply
	for {
		var r testReply
		if err := decoder.Decode(&r); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatal(err)
		}
		if r.JSONRPC != "2.0" || (r.Result == nil) == (r.Error == nil) {
			t.Fatalf("invalid reply: %+v", r)
		}
		replies = append(replies, r)
	}
	return replies
}

func TestStdioInitializationAndSealedCatalog(t *testing.T) {
	replies := transcript(t,
		`{"jsonrpc":"2.0","id":0,"method":"tools/list"}`,
		initialized,
		initialize,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		initialized,
		`{"jsonrpc":"2.0","id":"tools","method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":"resources","method":"resources/list"}`,
		`{"jsonrpc":"2.0","id":"templates","method":"resources/templates/list"}`,
		`{"jsonrpc":"2.0","id":"prompts","method":"prompts/list"}`,
		`{"jsonrpc":"2.0","id":9007199254740993,"method":"ping"}`,
		initialize,
	)
	if len(replies) != 9 {
		t.Fatalf("reply count = %d", len(replies))
	}
	for _, i := range []int{0, 2, 8} {
		if replies[i].Error == nil || replies[i].Error.Code != -32600 {
			t.Fatalf("initialization state was bypassed at reply %d", i)
		}
	}
	var info struct{ Name, Version string }
	if err := json.Unmarshal(replies[1].Result["serverInfo"], &info); err != nil ||
		info.Name != ServerName || info.Version != ServerVersion {
		t.Fatal("helper identity does not match the registration contract")
	}
	for i, key := range []string{"tools", "resources", "resourceTemplates", "prompts"} {
		if string(replies[i+3].Result[key]) != "[]" || len(replies[i+3].Result) != 1 {
			t.Fatalf("%s catalog is not empty", key)
		}
	}
	if string(replies[7].ID) != "9007199254740993" || len(replies[7].Result) != 0 {
		t.Fatal("integer request identity was not retained exactly")
	}
}

func TestEveryHostEffectAndGatewayEscapeRequestFails(t *testing.T) {
	const secret = "SYNTHETIC_HOST_SECRET_NEVER_ECHO"
	frames := []string{initialize, initialized}
	for _, method := range []string{
		"tools/call", "resources/read", "resources/subscribe", "resources/unsubscribe", "prompts/get",
		"mcp-find", "mcp-add", "mcp-exec", "code-mode", "sampling/createMessage", "elicitation/create", "unknown",
	} {
		raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": len(frames), "method": method,
			"params": map[string]any{"name": "file_read", "uri": "file:///host/secret", "arguments": map[string]any{"secret": secret}}})
		frames = append(frames, string(raw))
		// A forged notification must not invoke any action or emit a response.
		raw, _ = json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method,
			"params": map[string]any{"secret": secret}})
		frames = append(frames, string(raw))
	}
	replies := transcript(t, frames...)
	if len(replies) != 13 {
		t.Fatalf("effect requests or notifications returned unexpected replies: %d", len(replies))
	}
	for _, reply := range replies[1:] {
		if reply.Result != nil || reply.Error == nil ||
			(reply.Error.Code != -32601 && reply.Error.Code != -32602) {
			t.Fatal("host action or gateway escape request was accepted")
		}
		raw, _ := json.Marshal(reply)
		if bytes.Contains(raw, []byte(secret)) || bytes.Contains(raw, []byte("file:///host/secret")) {
			t.Fatal("request content was reflected into an error")
		}
	}
}

func TestMalformedRequestsAndAmbiguousEnvelopesAreRejected(t *testing.T) {
	for _, raw := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/list","method":"ping"}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list","Method":"ping"}`,
		`{"jsonrpc":"2.0","id":1,"method":"ping","params":{"_meta":{"key":1,"key":2}}}`,
		`{"jsonrpc":"2.0","id":1,"method":"ping","unknown":true}`,
		`{"jsonrpc":"2.0","id":null,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":{},"method":"ping"}`,
		`{"jsonrpc":"2.0","id":1.25,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":1e2,"method":"ping"}`,
		`[{"jsonrpc":"2.0","id":1,"method":"ping"}]`,
		`{"jsonrpc":"1.0","id":1,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":1,"method":"ping","params":{"_meta":` + strings.Repeat("[", 34) + "0" + strings.Repeat("]", 34) + `}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			replies := transcript(t, raw)
			if len(replies) != 1 || replies[0].Error == nil || replies[0].Error.Code != -32600 || string(replies[0].ID) != "null" {
				t.Fatal("ambiguous or invalid request was accepted")
			}
		})
	}
	for _, raw := range []string{"{", "{} {}", string([]byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'})} {
		replies := transcript(t, raw)
		if len(replies) != 1 || replies[0].Error == nil || replies[0].Error.Code != -32700 {
			t.Fatal("malformed JSON did not receive a fixed parse error")
		}
	}
	for _, params := range []string{"null", "[]", `{"unexpected":true}`, `{"_meta":null}`} {
		replies := transcript(t, `{"jsonrpc":"2.0","id":1,"method":"ping","params":`+params+`}`)
		if len(replies) != 1 || replies[0].Error == nil || replies[0].Error.Code != -32602 {
			t.Fatal("invalid parameters were accepted")
		}
	}
}

func TestInvalidInitializedNotificationDoesNotCompleteHandshake(t *testing.T) {
	replies := transcript(t, initialize,
		`{"jsonrpc":"2.0","method":"notifications/initialized","params":{"tools":true}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		initialized,
		`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`)
	if len(replies) != 3 || replies[1].Error == nil || string(replies[2].Result["tools"]) != "[]" {
		t.Fatal("invalid initialization notification changed the handshake state")
	}
}

func TestProtocolNegotiation(t *testing.T) {
	for _, version := range []string{"2024-11-05", "2025-03-26", "2025-06-18", ProtocolVersion, "unsupported"} {
		replies := transcript(t, strings.Replace(initialize, ProtocolVersion, version, 1))
		want := version
		if version == "unsupported" {
			want = ProtocolVersion
		}
		if len(replies) != 1 || string(replies[0].Result["protocolVersion"]) != `"`+want+`"` {
			t.Fatalf("negotiated protocol for %s does not match", version)
		}
	}
}

func TestStdioFramesAndReadWriteFailuresAreBounded(t *testing.T) {
	const prefix = `{"jsonrpc":"2.0","id":1,"method":"ping","params":{"_meta":{"padding":"`
	const suffix = `"}}}`
	frame := prefix + strings.Repeat("x", MaxFrameBytes-len(prefix)-len(suffix)) + suffix
	for _, tc := range []struct {
		name, stream string
		wantCode     int
		wantOutput   bool
	}{
		{"at-limit", frame + "\n", 0, true},
		{"over-limit", frame + " \n", 1, false},
		{"unterminated", frame, 1, false},
		{"crlf", `{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\r\n", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			_, code := Execute([]string{Arg}, strings.NewReader(tc.stream), &output)
			if code != tc.wantCode || (output.Len() != 0) != tc.wantOutput {
				t.Fatalf("code=%d, output bytes=%d", code, output.Len())
			}
		})
	}
	if _, code := Execute([]string{Arg}, failingStream{}, io.Discard); code != 1 {
		t.Fatal("read failure was reported as a successful stream")
	}
	if _, code := Execute([]string{Arg}, strings.NewReader(initialize+"\n"), failingStream{}); code != 1 {
		t.Fatal("write failure was reported as a successful stream")
	}
}

type failingStream struct{}

func (failingStream) Read([]byte) (int, error)  { return 0, io.ErrClosedPipe }
func (failingStream) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestRealStdioProcessWorksWithTheMCPClientAndHasNoHostCapabilities(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	root := t.TempDir()
	const secret = "SYNTHETIC_HOST_SECRET_NEVER_RETURN"
	marker := filepath.Join(root, "host-sentinel")
	if err := os.WriteFile(marker, []byte(secret), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, os.Args[0], Arg)
	command.Dir = root
	command.Env = []string{"CYBERAGENT_HOME=" + filepath.Join(root, "must-not-be-created"), "TRAVERSE_SBX_TEST_SECRET=" + secret}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	client := sdk.NewClient(&sdk.Implementation{Name: "sbx-zero-service-regression", Version: "1"}, nil)
	// Race-instrumented child binaries retain Go's one-second exit delay. Leave
	// enough grace for normal EOF shutdown without signaling a healthy helper.
	session, err := client.Connect(ctx, &sdk.CommandTransport{Command: command, TerminateDuration: 3 * time.Second}, nil)
	if err != nil {
		t.Fatalf("real stdio initialize failed: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	identity := session.InitializeResult()
	if identity.ServerInfo.Name != ServerName || identity.ServerInfo.Version != ServerVersion || identity.ProtocolVersion != ProtocolVersion {
		t.Fatal("subprocess helper identity changed")
	}
	tools, err := session.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 0 || tools.NextCursor != "" {
		t.Fatalf("tools list: %v", err)
	}
	resources, err := session.ListResources(ctx, nil)
	if err != nil || len(resources.Resources) != 0 || resources.NextCursor != "" {
		t.Fatalf("resources list: %v", err)
	}
	templates, err := session.ListResourceTemplates(ctx, nil)
	if err != nil || len(templates.ResourceTemplates) != 0 || templates.NextCursor != "" {
		t.Fatalf("resource templates list: %v", err)
	}
	prompts, err := session.ListPrompts(ctx, nil)
	if err != nil || len(prompts.Prompts) != 0 || prompts.NextCursor != "" {
		t.Fatalf("prompts list: %v", err)
	}
	for _, name := range []string{"file_read", "shell", "mcp-find", "mcp-add", "mcp-exec", "code-mode"} {
		if _, err := session.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: map[string]any{"path": marker, "command": "ignored"}}); err == nil {
			t.Fatalf("host capability %s was accepted", name)
		} else if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), marker) {
			t.Fatal("a tool error exposed caller or host data")
		}
	}
	if _, err := session.ReadResource(ctx, &sdk.ReadResourceParams{URI: "file:///" + filepath.ToSlash(marker)}); err == nil {
		t.Fatal("host resource was exposed")
	}
	if _, err := session.GetPrompt(ctx, &sdk.GetPromptParams{Name: "host-state"}); err == nil {
		t.Fatal("host prompt was exposed")
	}
	if err := session.Close(); err != nil {
		t.Fatalf("helper did not exit cleanly after stdin closed: %v", err)
	}
	if command.ProcessState == nil || !command.ProcessState.Success() || stderr.Len() != 0 {
		t.Fatal("helper process failed or emitted non-protocol output")
	}
	data, err := os.ReadFile(marker)
	entries, listErr := os.ReadDir(root)
	if err != nil || listErr != nil || string(data) != secret || len(entries) != 1 || entries[0].Name() != "host-sentinel" {
		t.Fatal("helper changed host state or created application state")
	}
}
