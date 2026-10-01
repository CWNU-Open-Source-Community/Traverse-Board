package llm

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

const MaxToolRequestRejectionBytes = 64 * 1024

// ToolRequestRejection is diagnostic evidence, never executable work or native
// replay. Only the factory can construct its immutable, bounded projection.
// Free-form argument values and unknown keys are not retained, even when a
// textual secret detector would miss them. Hashes bind the received bytes.
type ToolRequestRejection struct {
	json     []byte
	received string
	feedback string
}

type rejectionDocument struct {
	Version        string          `json:"version"`
	ReceivedSHA256 string          `json:"received_sha256"`
	CallCount      int             `json:"call_count"`
	Truncated      bool            `json:"truncated"`
	Calls          []rejectionCall `json:"calls"`
}

type rejectionCall struct {
	Position        int              `json:"position"`
	Name            string           `json:"name"`
	NameSHA256      string           `json:"name_sha256"`
	IDSHA256        string           `json:"id_sha256"`
	ArgumentsBytes  int              `json:"arguments_bytes"`
	ArgumentsSHA256 string           `json:"arguments_sha256"`
	ValidJSON       bool             `json:"valid_json"`
	Offered         string           `json:"offered"`
	SchemaSHA256    string           `json:"schema_sha256,omitempty"`
	ExpectedVersion string           `json:"expected_version,omitempty"`
	ReceivedVersion rejectionVersion `json:"received_version"`
	Shape           rejectionShape   `json:"shape"`
}

type rejectionVersion struct {
	State  string `json:"state"`
	Value  string `json:"value,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}

type rejectionShape struct {
	Type          string                    `json:"type"`
	Fields        map[string]rejectionShape `json:"fields,omitempty"`
	UnknownFields int                       `json:"unknown_fields,omitempty"`
	DuplicateKeys int                       `json:"duplicate_keys,omitempty"`
	Truncated     bool                      `json:"truncated,omitempty"`
}

type rejectionSchema struct {
	Properties map[string]json.RawMessage `json:"properties"`
}

func rejectionHash(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func rejectionReceivedHash(calls []ToolCall) string {
	digest := sha256.New()
	write := func(value []byte) {
		fmt.Fprintf(digest, "%d:", len(value))
		_, _ = digest.Write(value)
	}
	for _, call := range calls {
		write([]byte(call.ID))
		write([]byte(call.Name))
		write(call.Arguments)
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func rejectionSafeName(value string) bool {
	if len(value) == 0 || len(value) > 80 {
		return false
	}
	for _, c := range value {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' {
			return false
		}
	}
	return true
}

// Retain protocol markers from the trusted offered family, and the fixed
// browser result envelope (which is not a valid browser request version).
// Arbitrary strings in a version field remain hash-only evidence.
func rejectionSafeVersion(value, expected string) bool {
	if value == "agent-browser-runtime.v1" {
		return true
	}
	family, _, ok := strings.Cut(expected, ".v")
	if !ok || !rejectionSafeName(family) || len(value) > 96 || !strings.HasPrefix(value, family+".v") {
		return false
	}
	suffix := strings.TrimPrefix(value, family+".v")
	if len(suffix) < 1 || len(suffix) > 3 {
		return false
	}
	for _, c := range suffix {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func rejectionExpectedVersion(schema json.RawMessage) string {
	var root rejectionSchema
	if len(schema) > MaxProviderToolPayloadSize || json.Unmarshal(schema, &root) != nil {
		return ""
	}
	var constraint struct {
		Const json.RawMessage   `json:"const"`
		Enum  []json.RawMessage `json:"enum"`
	}
	if json.Unmarshal(root.Properties["version"], &constraint) != nil {
		return ""
	}
	var expected string
	if len(constraint.Const) != 0 {
		_ = json.Unmarshal(constraint.Const, &expected)
	} else if len(constraint.Enum) == 1 {
		_ = json.Unmarshal(constraint.Enum[0], &expected)
	}
	if !rejectionSafeVersion(expected, expected) {
		return ""
	}
	if len(constraint.Enum) > 0 {
		var only string
		if len(constraint.Enum) != 1 || json.Unmarshal(constraint.Enum[0], &only) != nil || only != expected {
			return ""
		}
	}
	return expected
}

func rejectionJSONType(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "absent"
	}
	switch raw[0] {
	case '{':
		return "object"
	case '[':
		return "array"
	case '"':
		return "string"
	case 'n':
		return "null"
	case 't', 'f':
		return "boolean"
	default:
		return "number"
	}
}

func rejectionObject(raw json.RawMessage) (map[string]json.RawMessage, int, bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	_, _ = decoder.Token() // Caller already proved this is valid object JSON.
	fields := make(map[string]json.RawMessage)
	duplicates := 0
	for count := 0; decoder.More(); count++ {
		if count == 64 {
			return fields, duplicates, true
		}
		key, _ := decoder.Token()
		var value json.RawMessage
		_ = decoder.Decode(&value)
		name := key.(string)
		if _, exists := fields[name]; exists {
			duplicates++
			// Do not select a winner for a duplicated protocol version.
			if name == "version" {
				value = nil
			}
		}
		fields[name] = value
	}
	return fields, duplicates, false
}

func rejectionVersionField(raw json.RawMessage) (json.RawMessage, int) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	_, _ = decoder.Token()
	var version json.RawMessage
	count := 0
	// The argument byte limit bounds this complete scan independently of the
	// smaller shape projection. A late duplicate must not certify an early value.
	for decoder.More() {
		key, _ := decoder.Token()
		var value json.RawMessage
		_ = decoder.Decode(&value)
		if key == "version" {
			count++
			version = value
		}
	}
	return version, count
}

func rejectionProjectShape(raw, schema json.RawMessage, depth int, remaining *int) rejectionShape {
	shape := rejectionShape{Type: rejectionJSONType(raw)}
	if shape.Type != "object" || depth >= 3 || *remaining <= 0 {
		shape.Truncated = shape.Type == "object" || shape.Type == "array"
		return shape
	}
	*remaining--
	fields, duplicates, truncated := rejectionObject(raw)
	shape.DuplicateKeys, shape.Truncated = duplicates, truncated
	var definition rejectionSchema
	_ = json.Unmarshal(schema, &definition)
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		subschema, known := definition.Properties[name]
		if !known || !rejectionSafeName(name) {
			shape.UnknownFields++
			continue
		}
		if *remaining <= 0 || len(shape.Fields) == 24 {
			shape.Truncated = true
			continue
		}
		*remaining--
		if shape.Fields == nil {
			shape.Fields = make(map[string]rejectionShape)
		}
		shape.Fields[name] = rejectionProjectShape(fields[name], subschema, depth+1, remaining)
	}
	return shape
}

func NewToolRequestRejection(calls []ToolCall, offered []ToolSpec) *ToolRequestRejection {
	document := rejectionDocument{Version: "tool_request_rejection.v1", ReceivedSHA256: rejectionReceivedHash(calls), CallCount: len(calls), Calls: []rejectionCall{}}
	feedback := make([]string, 0, 4)
	for index, call := range calls {
		if index == MaxProviderToolCalls {
			document.Truncated = true
			break
		}
		entry := rejectionCall{Position: index, Name: "[unoffered]", NameSHA256: rejectionHash([]byte(call.Name)), IDSHA256: rejectionHash([]byte(call.ID)), ArgumentsBytes: len(call.Arguments), ArgumentsSHA256: rejectionHash(call.Arguments), Offered: "not_offered", ReceivedVersion: rejectionVersion{State: "not_observed"}}
		var schema json.RawMessage
		matches := 0
		name := strings.TrimSpace(call.Name)
		if offered == nil {
			entry.Offered = "not_observed"
		}
		for _, spec := range offered {
			if strings.TrimSpace(spec.Name) == name {
				matches++
				schema = spec.Parameters
			}
		}
		if matches == 1 && rejectionSafeName(name) {
			entry.Name, entry.Offered = name, "offered"
			entry.SchemaSHA256 = rejectionHash(schema)
			entry.ExpectedVersion = rejectionExpectedVersion(schema)
		} else {
			schema = nil
			if matches > 1 {
				entry.Offered = "ambiguous"
			}
		}
		entry.ValidJSON = utf8.Valid(call.Arguments) && json.Valid(call.Arguments)
		entry.Shape = rejectionShape{Type: "invalid"}
		if entry.ValidJSON {
			entry.Shape.Type = rejectionJSONType(call.Arguments)
			if len(call.Arguments) <= MaxToolRequestRejectionBytes {
				remaining := 48
				entry.Shape = rejectionProjectShape(call.Arguments, schema, 0, &remaining)
				if entry.Shape.Type == "object" {
					raw, count := rejectionVersionField(call.Arguments)
					entry.ReceivedVersion.State = "missing"
					if count > 1 {
						entry.ReceivedVersion.State = "duplicate"
					} else if count == 1 {
						entry.ReceivedVersion = rejectionVersion{State: rejectionJSONType(raw), SHA256: rejectionHash(raw)}
						if len(raw) <= 128 && entry.ReceivedVersion.State == "string" {
							var value string
							_ = json.Unmarshal(raw, &value)
							if rejectionSafeVersion(value, entry.ExpectedVersion) {
								entry.ReceivedVersion.Value = value
							}
						}
					}
				}
			} else {
				entry.Shape.Truncated = true
			}
		}
		if len(feedback) < 4 && entry.ExpectedVersion != "" && entry.ReceivedVersion.State != "not_observed" &&
			(entry.ReceivedVersion.State != "string" || entry.ReceivedVersion.Value != entry.ExpectedVersion) {
			feedback = append(feedback, fmt.Sprintf("The offered %s request requires version=%q; result-envelope versions are not request versions.", entry.Name, entry.ExpectedVersion))
		}
		document.Calls = append(document.Calls, entry)
	}
	raw, _ := json.Marshal(document)
	if len(raw) > MaxToolRequestRejectionBytes {
		// No raw prefix fallback: retain only the bound digest and count.
		document.Calls, document.Truncated = nil, true
		raw, _ = json.Marshal(document)
	}
	return &ToolRequestRejection{json: raw, received: document.ReceivedSHA256, feedback: strings.Join(feedback, " ")}
}

func (d *ToolRequestRejection) DiagnosticJSON() []byte   { return append([]byte(nil), d.json...) }
func (d *ToolRequestRejection) DiagnosticSHA256() string { return rejectionHash(d.json) }
func (d *ToolRequestRejection) MatchesReceived(calls []ToolCall) bool {
	return d.received == rejectionReceivedHash(calls)
}
func (d *ToolRequestRejection) VersionRepairFeedback() string { return d.feedback }
