package application

import (
	"encoding/json"
	"strings"
	"testing"

	"cyberagent-workbench/internal/domain"
)

func TestSupervisorBrowserLayoutOmittedWholeInBothReceiptPaths(t *testing.T) {
	raw := `{"version":"agent-browser-runtime.v1","snapshot_id":"old-snapshot","document_epoch":4,"canonical_url":"https://example.org","untrusted_evidence":true,"layout":{"truncated":false,"nodes":[{"id":"one"},{"id":"two"},{"id":"three"},{"id":"four"}],"viewport":{"width":390,"height":844}},"elements":[{"ref":"old-ref","role":"button","name":"Delete"}]}`
	call := segmentReceiptCall(t, 1, 1, raw)
	call.ToolName = "browser_snapshot"
	call.PayloadJSON = `{"version":"browser_snapshot.v2"}`
	envelope, err := marshalSupervisorToolResultEnvelope(supervisorToolResultEnvelope{Version: supervisorToolResultVersion, Tool: call.ToolName, Status: "completed", Stdout: raw})
	if err != nil {
		t.Fatal(err)
	}
	call.ResultJSON = string(envelope)
	before := call.ResultJSON
	projection, ok := supervisorSegmentReceiptProjection(call)
	if !ok {
		t.Fatal("snapshot projection unavailable")
	}
	check := func(value any) {
		t.Helper()
		object, ok := value.(map[string]any)
		if !ok || object["layout_omitted"] != true || object["browser_references_historical"] != true || object["layout"] != nil || object["snapshot_id"] != "old-snapshot" {
			t.Fatalf("historical layout became a partial current observation: %+v", value)
		}
	}
	check(projection)
	plan, err := minimalSupervisorBoundaryReceipt([]domain.SupervisorToolCall{call}, "receipt-session", "receipt-attempt")
	if err != nil {
		t.Fatal(err)
	}
	content, err := supervisorBoundaryReceiptContent(plan, 32768, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	entries := boundaryBudgetEntries(t, content)
	entry := entries[call.CallID]
	check(entry["result"])
	if entry["original_result"] == nil || !strings.Contains(content, "expected_sha256") || call.ResultJSON != before || !json.Valid([]byte(call.ResultJSON)) {
		t.Fatal("exact sealed readback lost or changed")
	}
	// The native pair still carries the full, fresh layout; receipt projection
	// must not change the source or the normal provider input.
	projected, err := supervisorToolContextResult(call)
	var native supervisorToolResultEnvelope
	if err != nil || json.Unmarshal([]byte(projected), &native) != nil || native.Stdout != raw {
		t.Fatal("fresh native snapshot was altered by historical projection")
	}
}
