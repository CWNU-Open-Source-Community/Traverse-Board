package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/commandruntimeadapter"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/mcp"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/toolgateway"
)

func TestThreadActivityDetailHTTPIsLazyThreadScopedAndRedacted(t *testing.T) {
	fixture := newAPIFixture(t)
	maxBytes := runner.MinCommandRuntimeOutputRead
	input := toolgateway.CommandRuntimeInput{
		Version: toolgateway.CommandRuntimeToolProtocolVersion,
		Action:  toolgateway.CommandRuntimeActionRun,
		Commands: []runner.CommandRuntimeSpec{{
			Version: runner.CommandRuntimeProtocolVersion, Profile: runner.CommandRuntimePowerShell,
			Script: "Write-Output safe", WorkingDirectory: ".",
			Environment: []runner.CommandRuntimeEnvironment{{Name: "VISIBLE_TEST",
				Value: "never-project-environment-value"}},
			StdinPolicy:  runner.CommandRuntimeStdinPipe,
			InitialStdin: "never-project-stdin-value", CloseInitialStdin: true,
			TimeoutMilliseconds: 1000,
			Output: runner.CommandRuntimeOutputPolicy{InlineBytes: runner.MinCommandRuntimeInlineBytes,
				ArtifactBytes: runner.MinCommandRuntimeInlineBytes},
			Network:     runner.CommandRuntimeNetworkDisabled,
			Credentials: runner.CommandRuntimeCredentialsNone,
			Purpose:     "verify safe lazy command projection",
		}},
		FailurePolicy: toolgateway.CommandRuntimeFailFast, MaxBytes: &maxBytes,
	}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := toolgateway.NormalizeSupervisorToolPayload(
		toolgateway.CommandRuntimeTool, raw)
	if err != nil {
		t.Fatal(err)
	}
	callID, err := runmutation.SupervisorToolCallID(
		runmutation.SupervisorToolOperationKey(fixture.run.ID,
			fixture.checkpoint.NextTurn, string(toolgateway.CommandRuntimeTool), string(canonical)), 1)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := commandruntimeadapter.EncodeAuthority(commandruntimeadapter.NewAuthority(
		fixture.run.ID, commandruntimeadapter.HostUnsandboxed("http-activity-test")))
	if err != nil {
		t.Fatal(err)
	}
	attempt := fixture.attempt
	attempt.Outcome = llm.OutcomeSuccess
	checkpoint, err := fixture.store.RecordSupervisorModelCompleted(t.Context(), fixture.checkpoint,
		attempt, llm.ChatResponse{Provider: attempt.Provider, Model: attempt.Model,
			Usage: llm.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2},
			ToolCalls: []llm.ToolCall{{ID: callID, Name: string(toolgateway.CommandRuntimeTool),
				Arguments: raw, Authority: authority}}})
	if err != nil {
		t.Fatal(err)
	}
	if inserted, err := fixture.store.RecordSupervisorToolExecutionStarted(t.Context(),
		checkpoint, callID); err != nil || !inserted {
		t.Fatalf("record command execution started: inserted=%t err=%v", inserted, err)
	}
	thread, err := fixture.store.GetThreadByRun(t.Context(), fixture.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/threads/" + thread.ID + "/activities/" + callID
	response := fixture.get(t, path)
	if response.Code != http.StatusOK {
		t.Fatalf("activity detail status=%d body=%s", response.Code, response.Body.String())
	}
	var envelope struct {
		Data ThreadActivityDetailView `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Version != application.ThreadActivityDetailProtocolVersion ||
		envelope.Data.ActivityRef != callID || envelope.Data.RunID != fixture.run.ID ||
		len(envelope.Data.Tools) != 1 || envelope.Data.Tools[0].Detail.Command == nil ||
		len(envelope.Data.Tools[0].Detail.Command.Commands) != 1 ||
		envelope.Data.Tools[0].Detail.Command.Commands[0].Command != "Write-Output safe" {
		t.Fatalf("unexpected activity detail: %#v", envelope.Data)
	}
	for _, forbidden := range []string{"never-project-environment-value",
		"never-project-stdin-value", "VISIBLE_TEST", "payload_json", "authority_json"} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Fatalf("activity detail exposed %q: %s", forbidden, response.Body.String())
		}
	}

	transcriptResponse := fixture.get(t, "/api/v1/threads/"+thread.ID+"/transcript")
	if transcriptResponse.Code != http.StatusOK {
		t.Fatalf("transcript status=%d body=%s", transcriptResponse.Code,
			transcriptResponse.Body.String())
	}
	var transcriptEnvelope struct {
		Data []ThreadTranscriptItemView `json:"data"`
	}
	if err := json.Unmarshal(transcriptResponse.Body.Bytes(), &transcriptEnvelope); err != nil {
		t.Fatal(err)
	}
	var summary *ThreadActivitySummaryView
	for index := range transcriptEnvelope.Data {
		if transcriptEnvelope.Data[index].ActivityDetailRef == callID {
			summary = transcriptEnvelope.Data[index].ActivitySummary
		}
	}
	if summary == nil || summary.Version != application.ThreadActivitySummaryProtocolVersion ||
		summary.ActivityRef != callID || summary.Command != "Write-Output safe" ||
		summary.Status != string(domain.SupervisorToolPending) || summary.CommandCount != 1 {
		t.Fatalf("unexpected safe transcript command summary: %#v body=%s", summary,
			transcriptResponse.Body.String())
	}
	for _, forbidden := range []string{"never-project-environment-value",
		"never-project-stdin-value", "VISIBLE_TEST", "payload_json", "authority_json"} {
		if strings.Contains(transcriptResponse.Body.String(), forbidden) {
			t.Fatalf("transcript command summary exposed %q: %s", forbidden,
				transcriptResponse.Body.String())
		}
	}

	_, otherRun, err := application.NewRunService(fixture.store).Create(t.Context(),
		application.CreateRunRequest{Goal: "other activity Thread", Profile: "review",
			WorkspaceID: fixture.workspace.ID, Budget: domain.Budget{MaxTurns: 2}})
	if err != nil {
		t.Fatal(err)
	}
	otherThread, err := fixture.store.GetThreadByRun(t.Context(), otherRun.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertAPIError(t, fixture.get(t, "/api/v1/threads/"+otherThread.ID+"/activities/"+callID),
		http.StatusNotFound, "NOT_FOUND")
	assertAPIError(t, fixture.get(t, path+"?raw=true"),
		http.StatusBadRequest, "INVALID_ARGUMENT")
}

func TestThreadActivityDetailHTTPProjectsTypedMCPFactsAndTranscriptReference(t *testing.T) {
	fixture := newAPIFixture(t)
	payload, err := json.Marshal(toolgateway.MCPToolCallPayload{
		Version:               toolgateway.MCPClientToolProtocolVersion,
		ServerID:              "issue-tracker",
		ToolName:              "create_issue",
		CapabilityFingerprint: strings.Repeat("a", 64),
		Arguments: json.RawMessage(
			`{"title":"public issue title","credential_ref":"credential-http-mcp"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := toolgateway.NormalizeSupervisorToolPayload(
		toolgateway.MCPToolCallTool, payload)
	if err != nil {
		t.Fatal(err)
	}
	callID, err := runmutation.SupervisorToolCallID(
		runmutation.SupervisorToolOperationKey(fixture.run.ID,
			fixture.checkpoint.NextTurn, string(toolgateway.MCPToolCallTool), string(canonical)), 1)
	if err != nil {
		t.Fatal(err)
	}
	attempt := fixture.attempt
	attempt.Outcome = llm.OutcomeSuccess
	authority, err := mcp.EncodeSupervisorCallAuthority(mcp.SupervisorCallAuthority{
		Version: mcp.SupervisorCallAuthorityVersion,
		RunID:   fixture.run.ID, MissionID: fixture.run.MissionID,
		WorkspaceID:          fixture.workspace.ID,
		PermissionSnapshotID: "permission-http-mcp", PermissionRevision: 1,
		PermissionMode: domain.RunExecutionPermissionFullAccess,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.RecordSupervisorModelCompleted(t.Context(), fixture.checkpoint,
		attempt, llm.ChatResponse{Provider: attempt.Provider, Model: attempt.Model,
			Usage: llm.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2},
			ToolCalls: []llm.ToolCall{{ID: callID, Name: string(toolgateway.MCPToolCallTool),
				Arguments: canonical, Authority: authority}}}); err != nil {
		t.Fatal(err)
	}
	thread, err := fixture.store.GetThreadByRun(t.Context(), fixture.run.ID)
	if err != nil {
		t.Fatal(err)
	}

	response := fixture.get(t, "/api/v1/threads/"+thread.ID+"/activities/"+callID)
	if response.Code != http.StatusOK {
		t.Fatalf("activity detail status=%d body=%s", response.Code, response.Body.String())
	}
	var envelope struct {
		Data ThreadActivityDetailView `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data.Tools) != 1 || envelope.Data.Tools[0].Detail.MCP == nil ||
		envelope.Data.Tools[0].Detail.Kind != "mcp" ||
		envelope.Data.Tools[0].Detail.MCP.Server != "issue-tracker" ||
		envelope.Data.Tools[0].Detail.MCP.Tool != "create_issue" {
		t.Fatalf("unexpected MCP detail: %#v", envelope.Data)
	}
	if !strings.Contains(response.Body.String(), "public issue title") ||
		!strings.Contains(response.Body.String(), "[已脱敏]") {
		t.Fatalf("MCP activity detail omitted its safe argument projection: %s", response.Body.String())
	}
	for _, forbidden := range []string{"credential-http-mcp", strings.Repeat("a", 64)} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Fatalf("MCP activity detail exposed %q: %s", forbidden, response.Body.String())
		}
	}

	transcript := fixture.get(t, "/api/v1/threads/"+thread.ID+"/transcript")
	if transcript.Code != http.StatusOK {
		t.Fatalf("transcript status=%d body=%s", transcript.Code, transcript.Body.String())
	}
	var transcriptEnvelope struct {
		Data []ThreadTranscriptItemView `json:"data"`
	}
	if err := json.Unmarshal(transcript.Body.Bytes(), &transcriptEnvelope); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range transcriptEnvelope.Data {
		if item.DurableCallID == callID {
			found = item.DetailAvailable && item.ActivityDetailRef == callID &&
				item.ActivitySummary == nil
		}
	}
	if !found {
		t.Fatalf("MCP transcript item did not expose lazy safe detail ref: %s", transcript.Body.String())
	}
}
