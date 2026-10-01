package application

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
)

func TestAnthropicBrowserVersionRejectionAndCorrectionUsesActualOfferedSchema(t *testing.T) {
	_, st, runtime, supervisor, turn := newAgentBrowserFixture(t)
	caps, authority, err := supervisor.supervisorBrowserActionCapabilities(t.Context(), turn, st.base.executionPermission)
	if err != nil || !caps.Available {
		t.Fatal("fixture has no browser authority", err)
	}
	options := supervisorToolOptions{BrowserActions: supervisorBrowserActionTools{Capabilities: caps, Authority: authority}}
	specs := supervisorStructuredToolSpecs(turn.Mode.Surface, turn.Mode.Phase, st.base.executionPermission.Mode, false, false, options)
	var snapshotSpec llm.ToolSpec
	for _, spec := range specs {
		if spec.Name == "browser_snapshot" {
			snapshotSpec = spec
		}
	}
	if snapshotSpec.Name == "" {
		t.Fatal("snapshot schema not offered")
	}
	type wireRequest struct {
		Tools []struct {
			Name   string          `json:"name"`
			Schema json.RawMessage `json:"input_schema"`
		} `json:"tools"`
		Messages json.RawMessage `json:"messages"`
		System   json.RawMessage `json:"system"`
	}
	requests := make(chan wireRequest, 2)
	count := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var received wireRequest
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Error(err)
			http.Error(w, "invalid fixture request", 400)
			return
		}
		requests <- received
		count++
		version := "agent-browser-runtime.v1"
		if count == 2 {
			version = "browser_snapshot.v2"
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":"response-%d","type":"message","role":"assistant","model":"model","content":[{"type":"tool_use","id":"wire-%d","name":"browser_snapshot","input":{"version":%q}}],"stop_reason":"tool_use","usage":{"input_tokens":2,"output_tokens":3}}`, count, count, version)
	}))
	defer server.Close()
	provider, err := llm.NewAnthropicCompatibleProvider(llm.AnthropicCompatibleConfig{Name: "browser-wire-fixture", BaseURL: server.URL, APIKey: "fixture-only", DefaultModel: "model"})
	if err != nil {
		t.Fatal(err)
	}
	request := llm.ChatRequest{Model: "model", MaxTokens: 512, Tools: specs, Messages: []llm.Message{{Role: "system", Content: "original policy"}, {Role: "user", Content: "Inspect the already-open browser."}}}
	response, err := provider.Chat(t.Context(), request)
	if err != nil || len(response.ToolCalls) != 1 {
		t.Fatal("adapter lost received call", err)
	}
	original := append([]byte(nil), response.ToolCalls[0].Arguments...)
	prepared, parseErr := prepareSupervisorToolCalls(response.ToolCalls, turn.Run.ID, 1, 1,
		turn.Mode.Surface, turn.Mode.Phase, st.base.executionPermission.Mode, false, false, options)
	if parseErr == nil || len(prepared) != 0 || !strings.Contains(string(original), "agent-browser-runtime.v1") || len(st.calls) != 0 || len(runtime.actions) != 0 {
		t.Fatal("wrong request version was rewritten, executed, or lost", parseErr)
	}
	diagnostic := llm.NewToolRequestRejection(response.ToolCalls, request.Tools)
	if !strings.Contains(string(diagnostic.DiagnosticJSON()), `"expected_version":"browser_snapshot.v2"`) || !bytes.Equal(original, response.ToolCalls[0].Arguments) {
		t.Fatal("diagnostic does not bind the received version and actual offered schema")
	}
	reason, err := domain.NewSupervisorToolRequestRepairReason(0, supervisorProtocolRepairReason(parseErr)+" "+diagnostic.VersionRepairFeedback())
	if err != nil {
		t.Fatal(err)
	}
	repair := supervisorProtocolRepairRequest(request, reason, supervisorRepairContext{ThreadEndTurn: true})
	corrected, err := provider.Chat(t.Context(), repair)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := prepareSupervisorToolCalls(corrected.ToolCalls, turn.Run.ID, 1, 1,
		turn.Mode.Surface, turn.Mode.Phase, st.base.executionPermission.Mode, false, false, options)
	if err != nil || len(accepted) != 1 || len(accepted[0].Authority) == 0 {
		t.Fatal("schema-correct request failed original preparation gates", err)
	}
	for i := 0; i < 2; i++ {
		wire := <-requests
		found := false
		for _, spec := range wire.Tools {
			if spec.Name == "browser_snapshot" {
				var expected, actual any
				_ = json.Unmarshal(snapshotSpec.Parameters, &expected)
				_ = json.Unmarshal(spec.Schema, &actual)
				a, _ := json.Marshal(actual)
				e, _ := json.Marshal(expected)
				found = bytes.Equal(a, e)
			}
		}
		if !found || (i == 1 && !bytes.Contains(append(wire.System, wire.Messages...), []byte("browser_snapshot.v2"))) {
			t.Fatal("adapter changed offered schema or dropped actionable correction")
		}
	}
	if repair.Metadata["protocol_repair"] != "1" || len(st.calls) != 0 || len(runtime.actions) != 0 {
		t.Fatal("protocol experiment acquired another repair or created effects")
	}
}
