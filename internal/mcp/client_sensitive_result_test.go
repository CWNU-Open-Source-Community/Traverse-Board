package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type sensitiveResultTransport struct {
	content json.RawMessage
}

func (t sensitiveResultTransport) Exchange(_ context.Context, request Envelope) (Envelope, error) {
	result, err := json.Marshal(map[string]json.RawMessage{"content": t.content})
	if err != nil {
		return Envelope{}, err
	}
	return Envelope{JSONRPC: "2.0", ID: append(json.RawMessage(nil), request.ID...),
		Result: result}, nil
}

func (sensitiveResultTransport) Notify(context.Context, Envelope) error { return nil }
func (sensitiveResultTransport) Close() error                           { return nil }

func TestClientSanitizesSensitiveResultFieldsBeforeTruncation(t *testing.T) {
	passwordCanary := "short phrase canary"
	authCanary := "ordinary auth canary"
	content, err := json.Marshal(map[string]any{
		"a_password":    passwordCanary,
		"b_auth_header": authCanary,
		"z_padding":     strings.Repeat("x", 512),
	})
	if err != nil {
		t.Fatal(err)
	}
	client := newClient(sensitiveResultTransport{content: content}, ServerDescriptor{})
	result, err := client.CallTool(t.Context(), "lookup", json.RawMessage(`{}`), 128)
	if err != nil || !result.Truncated || strings.Contains(result.Content, passwordCanary) ||
		strings.Contains(result.Content, authCanary) ||
		!strings.Contains(result.Content, "[REDACTED:sensitive-field]") {
		t.Fatalf("bounded MCP result was not sanitized before truncation: result=%#v err=%v",
			result, err)
	}
}
