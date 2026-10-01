package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func rejectionTestSpec() ToolSpec {
	return ToolSpec{Name: "browser_type", Parameters: json.RawMessage(`{"type":"object","required":["version","snapshot_id","value"],"properties":{"version":{"const":"browser_type.v2"},"snapshot_id":{"type":"string"},"value":{"type":"string"},"url":{"type":"string"},"target":{"properties":{"name":{"type":"string"},"role":{"type":"string"}}}}}`)}
}

func TestToolRejectionRetainsVersionAndStructureWithoutFreeValues(t *testing.T) {
	spec := rejectionTestSpec()
	if !json.Valid(spec.Parameters) {
		t.Fatal("invalid offered schema fixture")
	}
	call := ToolCall{ID: "short-private-id", Name: spec.Name, Arguments: json.RawMessage(`{"version":"agent-browser-runtime.v1","value":"pw7","snapshot_id":"private-id","url":"https://x.invalid/?q=privatequery","target":{"name":"private label","role":"private role"},"unknown-secret-key":"short"}`)}
	original := append([]byte(nil), call.Arguments...)
	diagnostic := NewToolRequestRejection([]ToolCall{call}, []ToolSpec{spec})
	raw := diagnostic.DiagnosticJSON()
	for _, secret := range []string{"short-private-id", "pw7", "private-id", "privatequery", "private label", "private role", "unknown-secret-key", "short"} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatalf("free-form evidence leaked %q: %s", secret, raw)
		}
	}
	var doc rejectionDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	entry := doc.Calls[0]
	if entry.ReceivedVersion.Value != "agent-browser-runtime.v1" || entry.ExpectedVersion != "browser_type.v2" || entry.SchemaSHA256 != rejectionHash(spec.Parameters) || entry.Shape.UnknownFields != 1 || entry.Shape.Fields["target"].Fields["name"].Type != "string" {
		t.Fatalf("lost protocol/shape facts: %s", raw)
	}
	if !strings.Contains(diagnostic.VersionRepairFeedback(), `version="browser_type.v2"`) || !diagnostic.MatchesReceived([]ToolCall{call}) || !bytes.Equal(original, call.Arguments) {
		t.Fatal("feedback or binding changed the original arguments")
	}
	// Callers cannot change the evidence through the returned slice.
	raw[0] = '!'
	if !json.Valid(diagnostic.DiagnosticJSON()) {
		t.Fatal("diagnostic was mutable")
	}
}

func TestToolRejectionVersionStatesAndNoRawFallback(t *testing.T) {
	cases := []struct{ name, payload, state, value string }{
		{"missing", `{}`, "missing", ""},
		{"null", `{"version":null}`, "null", ""},
		{"number", `{"version":2}`, "number", ""},
		{"wrong", `{"version":"browser_type.v1"}`, "string", "browser_type.v1"},
		{"correct", `{"version":"browser_type.v2"}`, "string", "browser_type.v2"},
		{"duplicate", `{"version":"browser_type.v2","version":"pw7"}`, "duplicate", ""},
		{"triple", `{"version":"pw7","version":"browser_type.v2","version":"pw8"}`, "duplicate", ""},
		{"short_secret", `{"version":"pw7"}`, "string", ""},
		{"malformed", `{"version":"pw7"`, "not_observed", ""},
		{"array", `["pw7"]`, "not_observed", ""},
		{"oversized", fmt.Sprintf(`{"value":%q,"version":"pw7"}`, strings.Repeat("z", MaxToolRequestRejectionBytes)), "not_observed", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := NewToolRequestRejection([]ToolCall{{Name: "browser_type", Arguments: json.RawMessage(tc.payload)}}, []ToolSpec{rejectionTestSpec()})
			var doc rejectionDocument
			if err := json.Unmarshal(d.DiagnosticJSON(), &doc); err != nil {
				t.Fatal(err)
			}
			if got := doc.Calls[0].ReceivedVersion; got.State != tc.state || got.Value != tc.value {
				t.Fatalf("got %+v want state=%s value=%s", got, tc.state, tc.value)
			}
			if bytes.Contains(d.DiagnosticJSON(), []byte("pw7")) || len(d.DiagnosticJSON()) > MaxToolRequestRejectionBytes {
				t.Fatal("raw secret fallback or unbounded diagnostic")
			}
		})
	}
}

func TestToolRejectionBoundsBatchDeepObjectsAndUnknownNames(t *testing.T) {
	deep := strings.Repeat(`{"target":`, 400) + `"pw7"` + strings.Repeat(`}`, 400)
	calls := make([]ToolCall, MaxProviderToolCalls+3)
	for i := range calls {
		calls[i] = ToolCall{ID: "pw8", Name: "private_tool_name", Arguments: json.RawMessage(deep)}
	}
	d := NewToolRequestRejection(calls, []ToolSpec{rejectionTestSpec()})
	var doc rejectionDocument
	if err := json.Unmarshal(d.DiagnosticJSON(), &doc); err != nil || len(d.DiagnosticJSON()) > MaxToolRequestRejectionBytes || doc.CallCount != len(calls) || !doc.Truncated || len(doc.Calls) != MaxProviderToolCalls {
		t.Fatalf("unbounded projection: %s %v", d.DiagnosticJSON(), err)
	}
	for _, secret := range []string{"pw7", "pw8", "private_tool_name"} {
		if bytes.Contains(d.DiagnosticJSON(), []byte(secret)) {
			t.Fatal("unknown identity or deep secret leaked", secret)
		}
	}
	changed := append([]ToolCall(nil), calls...)
	changed[len(changed)-1].Arguments = json.RawMessage(`{}`)
	if d.MatchesReceived(changed) {
		t.Fatal("omitted tail did not bind original bytes")
	}
}

func TestToolRejectionSchemaIsActualOfferedFact(t *testing.T) {
	call := ToolCall{Name: "browser_type", Arguments: json.RawMessage(`{"version":"browser_type.v1"}`)}
	before := NewToolRequestRejection([]ToolCall{call}, []ToolSpec{rejectionTestSpec()})
	changed := rejectionTestSpec()
	changed.Parameters = bytes.ReplaceAll(changed.Parameters, []byte("browser_type.v2"), []byte("browser_type.v3"))
	after := NewToolRequestRejection([]ToolCall{call}, []ToolSpec{changed})
	if before.DiagnosticSHA256() == after.DiagnosticSHA256() || !strings.Contains(before.VersionRepairFeedback(), "browser_type.v2") || strings.Contains(before.VersionRepairFeedback(), "browser_type.v3") {
		t.Fatal("offered evidence was rebuilt from later schema")
	}
	for _, specs := range [][]ToolSpec{nil, {rejectionTestSpec(), rejectionTestSpec()}} {
		if got := NewToolRequestRejection([]ToolCall{call}, specs); got.VersionRepairFeedback() != "" {
			t.Fatal("unoffered/ambiguous tool invented a version correction")
		}
	}
	response, err := json.Marshal(ChatResponse{ToolRequestRejection: before})
	if err != nil || bytes.Contains(response, []byte("tool_request_rejection.v1")) {
		t.Fatal("private evidence serialized into a provider response", err)
	}
}

func TestToolRejectionFindsVersionDuplicateAfterShapeLimit(t *testing.T) {
	fields := []string{`"version":"browser_type.v2"`}
	for i := 0; i < 63; i++ {
		fields = append(fields, fmt.Sprintf(`"unknown%d":0`, i))
	}
	fields = append(fields, `"version":"pw7"`)
	d := NewToolRequestRejection([]ToolCall{{Name: "browser_type", Arguments: json.RawMessage("{" + strings.Join(fields, ",") + "}")}}, []ToolSpec{rejectionTestSpec()})
	var doc rejectionDocument
	_ = json.Unmarshal(d.DiagnosticJSON(), &doc)
	if doc.Calls[0].ReceivedVersion.State != "duplicate" || !doc.Calls[0].Shape.Truncated || strings.Contains(string(d.DiagnosticJSON()), "pw7") {
		t.Fatal("early unique version was certified despite a late duplicate", string(d.DiagnosticJSON()))
	}
}

func TestToolRejectionDistinguishesUnobservedSchemaAndNormalizedName(t *testing.T) {
	call := ToolCall{Name: " browser_type ", Arguments: json.RawMessage(`{"version":"browser_type.v1"}`)}
	for _, tc := range []struct {
		specs          []ToolSpec
		state, version string
	}{
		{nil, "not_observed", ""},
		{[]ToolSpec{}, "not_offered", ""},
		{[]ToolSpec{rejectionTestSpec()}, "offered", "browser_type.v2"},
	} {
		d := NewToolRequestRejection([]ToolCall{call}, tc.specs)
		var doc rejectionDocument
		_ = json.Unmarshal(d.DiagnosticJSON(), &doc)
		if entry := doc.Calls[0]; entry.Offered != tc.state || entry.ExpectedVersion != tc.version || !d.MatchesReceived([]ToolCall{call}) {
			t.Fatalf("schema observation or original binding changed: %+v", entry)
		}
	}
}
