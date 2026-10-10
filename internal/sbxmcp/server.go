// Package sbxmcp implements the fixed, zero-capability stdio MCP service used by
// the Docker Sandboxes adapter's static catalog. It deliberately has no access
// to the application control plane, credentials, filesystem, or network.
package sbxmcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

const (
	// Arg is the only supported helper invocation. Both product entrypoints
	// dispatch it before opening a database, reading credentials, or starting UI.
	Arg             = "--internal-sbx-zero-mcp-v1"
	ServerName      = "universal-code-sbx-zero-mcp"
	ServerVersion   = "1"
	ProtocolVersion = "2025-11-25"
	MaxFrameBytes   = 64 * 1024
)

var errFrame = errors.New("invalid bounded MCP frame")

// Execute recognizes the reserved helper argument anywhere in args, but serves
// only its exact singleton form. A malformed invocation never falls through to
// the ordinary application, and it prints no argument or stream content.
func Execute(args []string, input io.Reader, output io.Writer) (handled bool, code int) {
	reserved := false
	for _, arg := range args {
		if arg == Arg || strings.HasPrefix(arg, Arg+"=") {
			reserved = true
			break
		}
	}
	if !reserved {
		return false, 0
	}
	if len(args) != 1 || args[0] != Arg || input == nil || output == nil {
		return true, 2
	}
	if err := serve(input, output); err != nil {
		return true, 1
	}
	return true, 0
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type server struct {
	started, initialized bool
}

func serve(input io.Reader, output io.Writer) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), MaxFrameBytes+1)
	scanner.Split(splitFrame)
	encoder := json.NewEncoder(output)
	s := server{}
	for scanner.Scan() {
		if !utf8.Valid(scanner.Bytes()) || !json.Valid(scanner.Bytes()) {
			if err := encoder.Encode(response{JSONRPC: "2.0", ID: json.RawMessage("null"),
				Error: &rpcError{-32700, "Parse error"}}); err != nil {
				return err
			}
			continue
		}
		var r request
		if object(scanner.Bytes(), &r, "jsonrpc", "id", "method", "params") != nil || r.JSONRPC != "2.0" || r.Method == "" ||
			(len(r.ID) != 0 && !validID(r.ID)) {
			if err := encoder.Encode(response{JSONRPC: "2.0", ID: json.RawMessage("null"),
				Error: &rpcError{-32600, "Invalid Request"}}); err != nil {
				return err
			}
			continue
		}
		if len(r.ID) == 0 {
			if r.Method == "notifications/initialized" && s.started {
				var params struct {
					Meta json.RawMessage `json:"_meta,omitempty"`
				}
				if object(r.Params, &params, "_meta") == nil && optionalObject(params.Meta) {
					s.initialized = true
				}
			}
			// Notifications never receive a reply and never perform a host action.
			continue
		}
		result, failure := s.handle(r)
		if err := encoder.Encode(response{JSONRPC: "2.0", ID: r.ID, Result: result, Error: failure}); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func splitFrame(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		if i > MaxFrameBytes {
			return 0, nil, errFrame
		}
		return i + 1, bytes.TrimSuffix(data[:i], []byte("\r")), nil
	}
	if len(data) > MaxFrameBytes || (atEOF && len(data) != 0) {
		return 0, nil, errFrame
	}
	return 0, nil, nil
}

func (s *server) handle(r request) (any, *rpcError) {
	invalid := func() (any, *rpcError) { return nil, &rpcError{-32602, "Invalid params"} }
	if r.Method == "initialize" {
		if s.started {
			return nil, &rpcError{-32600, "Already initialized"}
		}
		var params struct {
			ProtocolVersion string          `json:"protocolVersion"`
			Capabilities    json.RawMessage `json:"capabilities"`
			ClientInfo      json.RawMessage `json:"clientInfo"`
			Meta            json.RawMessage `json:"_meta,omitempty"`
		}
		if object(r.Params, &params, "protocolVersion", "capabilities", "clientInfo", "_meta") != nil || params.ProtocolVersion == "" ||
			!requiredObject(params.Capabilities) || !requiredObject(params.ClientInfo) || !optionalObject(params.Meta) {
			return invalid()
		}
		version := params.ProtocolVersion
		switch version {
		case "2024-11-05", "2025-03-26", "2025-06-18", ProtocolVersion:
		default:
			version = ProtocolVersion
		}
		s.started = true
		return map[string]any{
			"protocolVersion": version,
			"capabilities": map[string]any{
				"tools": struct{}{}, "resources": struct{}{}, "prompts": struct{}{},
			},
			"serverInfo": map[string]string{"name": ServerName, "version": ServerVersion},
		}, nil
	}
	if r.Method == "ping" {
		var params struct {
			Meta json.RawMessage `json:"_meta,omitempty"`
		}
		if object(r.Params, &params, "_meta") != nil || !optionalObject(params.Meta) {
			return invalid()
		}
		return struct{}{}, nil
	}
	if !s.initialized {
		return nil, &rpcError{-32600, "Initialization incomplete"}
	}
	switch r.Method {
	case "tools/list", "resources/list", "resources/templates/list", "prompts/list":
		var params struct {
			Cursor string          `json:"cursor,omitempty"`
			Meta   json.RawMessage `json:"_meta,omitempty"`
		}
		if object(r.Params, &params, "cursor", "_meta") != nil || params.Cursor != "" || !optionalObject(params.Meta) {
			return invalid()
		}
		key := map[string]string{
			"tools/list": "tools", "resources/list": "resources",
			"resources/templates/list": "resourceTemplates", "prompts/list": "prompts",
		}[r.Method]
		return map[string]any{key: []any{}}, nil
	case "tools/call", "resources/read", "resources/subscribe", "resources/unsubscribe", "prompts/get":
		return invalid()
	default:
		return nil, &rpcError{-32601, "Method not found"}
	}
}

// object rejects duplicate keys at every depth, trailing JSON, excessive nesting,
// non-object parameters, and unknown fields on the request/parameter envelope.
func object(raw []byte, target any, fields ...string) error {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	if !requiredObject(raw) {
		return errFrame
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	allowed := make(map[string]bool, len(fields))
	for _, field := range fields {
		allowed[field] = true
	}
	if err := validateValue(decoder, 0, allowed); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errFrame
	}
	decoder = json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func requiredObject(raw []byte) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) >= 2 && trimmed[0] == '{' && trimmed[len(trimmed)-1] == '}'
}

func optionalObject(raw []byte) bool {
	return len(raw) == 0 || requiredObject(raw)
}

func validateValue(decoder *json.Decoder, depth int, allowed map[string]bool) error {
	if depth > 32 {
		return errFrame
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]bool)
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok || seen[key] || (allowed != nil && !allowed[key]) {
				return errFrame
			}
			seen[key] = true
			if err := validateValue(decoder, depth+1, nil); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := validateValue(decoder, depth+1, nil); err != nil {
				return err
			}
		}
	default:
		return errFrame
	}
	closing, err := decoder.Token()
	if err != nil || (delim == '{' && closing != json.Delim('}')) ||
		(delim == '[' && closing != json.Delim(']')) {
		return errFrame
	}
	return nil
}

func validID(raw []byte) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return false
	}
	if trimmed[0] == '"' {
		var id string
		return json.Unmarshal(trimmed, &id) == nil && len(id) <= 256
	}
	if len(trimmed) > 64 {
		return false
	}
	if trimmed[0] == '-' {
		trimmed = trimmed[1:]
	}
	if len(trimmed) == 0 || (len(trimmed) > 1 && trimmed[0] == '0') {
		return false
	}
	for _, b := range trimmed {
		if b < '0' || b > '9' {
			return false
		}
	}
	return true
}
