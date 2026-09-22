package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/runmutation"
	"cyberagent-workbench/internal/toolgateway"
)

func TestAgentBrowserSchemaV169PreservesV168RowsAndStoresTypedActions(t *testing.T) {
	ctx := context.Background()
	st := openUnmigratedSQLiteStore(t, filepath.Join(t.TempDir(), "agent-browser-v168.db"))
	defer st.Close()
	if e := applyMigrationPrefixForTest(ctx, st, migrationPlan(), 168); e != nil {
		t.Fatal(e)
	}
	_, run := createStructuredToolTestRun(t, ctx, st, "Agent browser migration")
	if _, e := application.NewRunService(st).Start(ctx, run.ID); e != nil {
		t.Fatal(e)
	}
	turn, e := st.BeginSupervisorTurn(ctx, acquireTestRunExecutionLease(t, ctx, st, run.ID), "browser migration")
	if e != nil {
		t.Fatal(e)
	}
	payload, e := toolgateway.NormalizeStructuredMemoryPayload(toolgateway.NoteCreateTool, json.RawMessage(`{"title":"preserve","content":"immutable old note"}`))
	if e != nil {
		t.Fatal(e)
	}
	key := runmutation.SupervisorToolOperationKey(run.ID, 1, "note_create", string(payload))
	id, _ := runmutation.SupervisorToolCallID(key, 1)
	attempt := llm.ModelAttempt{Number: 1, TransportAttempt: 1, MaxAttempts: 1, Provider: "test", Model: "model"}
	if _, e = st.RecordSupervisorModelStarted(ctx, turn.Checkpoint, attempt); e != nil {
		t.Fatal(e)
	}
	attempt.Outcome = llm.OutcomeSuccess
	checkpoint, e := st.RecordSupervisorModelCompleted(ctx, turn.Checkpoint, attempt, llm.ChatResponse{Provider: "test", Model: "model", Usage: llm.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2}, ToolCalls: []llm.ToolCall{{ID: id, Name: "note_create", Arguments: payload}}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = st.db.ExecContext(ctx, `UPDATE run_supervisor_tool_calls SET rowid=87 WHERE call_id=?`, id); e != nil {
		t.Fatal(e)
	}
	if _, e = st.RecordSupervisorToolExecutionStarted(ctx, checkpoint, id); e != nil {
		t.Fatal(e)
	}
	if _, _, e = st.RecordSupervisorToolResult(ctx, checkpoint, domain.SupervisorToolResult{CallID: id, Status: domain.SupervisorToolCompleted, ResultJSON: `{"ok":true}`, CompletedAt: time.Now().UTC()}); e != nil {
		t.Fatal(e)
	}
	before := readV165SupervisorCallRows(t, ctx, st, run.ID)
	var actorBefore string
	if e = st.db.QueryRowContext(ctx, `SELECT agent_id||':'||agent_attempt_id||':'||attribution_source FROM run_supervisor_tool_call_agents WHERE call_id=?`, id).Scan(&actorBefore); e != nil {
		t.Fatal(e)
	}
	if e = st.applyMigration(ctx, migrationPlan()[168]); e != nil {
		t.Fatal(e)
	}
	after := readV165SupervisorCallRows(t, ctx, st, run.ID)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("migration changed existing bytes/rowids: %#v %#v", before, after)
	}
	var actorAfter string
	if e = st.db.QueryRowContext(ctx, `SELECT agent_id||':'||agent_attempt_id||':'||attribution_source FROM run_supervisor_tool_call_agents WHERE call_id=?`, id).Scan(&actorAfter); e != nil || actorBefore != actorAfter {
		t.Fatalf("actor changed %s %s %v", actorBefore, actorAfter, e)
	}
	assertSupervisorToolCallSchemaV150(t, st)
	var ddl string
	_ = st.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type='table' AND name='run_supervisor_tool_calls'`).Scan(&ddl)
	for _, name := range []string{"browser_scroll", "browser_key"} {
		if strings.Count(ddl, "'"+name+"'") != 3 {
			t.Fatalf("authority checks missing %s", name)
		}
	}
	if _, e = st.db.ExecContext(ctx, `UPDATE run_supervisor_tool_calls SET tool_name='browser_arbitrary_execute' WHERE call_id=?`, id); e == nil {
		t.Fatal("unknown browser tool admitted")
	}

	root, found, e := st.GetRootAgent(ctx, run.ID)
	if e != nil || !found {
		t.Fatal(e)
	}
	mission, e := st.GetMission(ctx, run.MissionID)
	if e != nil {
		t.Fatal(e)
	}
	a := toolgateway.AgentBrowserCallAuthority{ProtocolVersion: toolgateway.AgentBrowserAuthorityVersion, RunID: run.ID, MissionID: run.MissionID, SessionID: run.SessionID, WorkspaceID: mission.WorkspaceID, RootAgentID: root.ID, Surface: domain.ExecutionSurfaceCode, Phase: domain.ExecutionPhaseDeliver, Role: domain.AgentRoleRoot, Profile: domain.ProfileCode, PermissionMode: domain.RunExecutionPermissionFullAccess, ModeRevision: 1, PermissionSnapshotID: "permission-browser-test", PermissionRevision: 1, PermissionActivation: 2, RunAuthorizationFence: 3, ManagerBootID: "browser-boot-test", BrowserSessionID: "browser-session-test", SessionGeneration: 1}
	a.Generation = a.Fingerprint()
	auth, _ := json.Marshal(a)
	typed := []struct {
		name toolgateway.ToolName
		raw  string
	}{{toolgateway.BrowserScrollTool, `{"version":"browser_scroll.v2","delta_x":0,"delta_y":500}`}, {toolgateway.BrowserKeyTool, `{"version":"browser_key.v2","key":"Enter","sensitive_intent":{"version":"browser_sensitive_intent.v1","effect":"external_write","target":"https://example.org/form","description":"Submit visible draft","document_epoch":2}}`}}
	calls := []llm.ToolCall{}
	for _, item := range typed {
		p, e := toolgateway.NormalizeAgentBrowserPayload(item.name, json.RawMessage(item.raw))
		if e != nil {
			t.Fatal(e)
		}
		k := runmutation.SupervisorToolOperationKey(run.ID, 1, string(item.name), string(p))
		callID, _ := runmutation.SupervisorToolCallID(k, 2)
		calls = append(calls, llm.ToolCall{ID: callID, Name: string(item.name), Arguments: p, Authority: auth})
	}
	attempt.Number = 2
	attempt.TransportAttempt = 1
	attempt.ToolRound = 1
	attempt.Outcome = ""
	if _, e = st.RecordSupervisorModelStarted(ctx, checkpoint, attempt); e != nil {
		t.Fatal(e)
	}
	attempt.Outcome = llm.OutcomeSuccess
	checkpoint, e = st.RecordSupervisorModelCompleted(ctx, checkpoint, attempt, llm.ChatResponse{Provider: "test", Model: "model", Usage: llm.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2}, ToolCalls: calls})
	if e != nil {
		t.Fatal(e)
	}
	rounds, e := st.ListSupervisorToolRounds(ctx, checkpoint)
	if e != nil || len(rounds) != 2 || len(rounds[1].Calls) != 2 {
		t.Fatalf("new actions readback %v %#v", e, rounds)
	}
	sensitive := rounds[1].Calls[1]
	now := time.Now().UTC()
	proposal := approval.Proposal{IdempotencyKey: "browser-approval-test", ProposalID: sensitive.CallID, SessionID: run.SessionID, WorkspaceID: mission.WorkspaceID, ToolName: toolgateway.AgentBrowserApprovalTool, ActionClass: "browser_external_write", Mode: "per_call", Status: approval.StatusPending, RequestFingerprint: toolgateway.AgentBrowserApprovalFingerprint(sensitive), DecisionReason: "Submit visible draft", RequestedBy: "run_supervisor", CreatedAt: now, UpdatedAt: now}
	bad := proposal
	bad.RequestFingerprint = strings.Repeat("0", 64)
	if _, e = st.EnsureApproval(ctx, bad); e == nil {
		t.Fatal("wrong browser approval fingerprint accepted")
	}
	record, e := st.EnsureApproval(ctx, proposal)
	if e != nil || record.RunID != run.ID {
		t.Fatalf("persisted source approval %v %+v", e, record)
	}
	if _, e = st.DecideApproval(ctx, approval.DecisionRequest{ProposalID: sensitive.CallID, IdempotencyKey: "approve-browser-test", Action: approval.ActionApprove, ReviewedBy: "operator"}); e != nil {
		t.Fatal(e)
	}

	for _, badResult := range []struct {
		status            domain.SupervisorToolCallStatus
		code, fingerprint string
	}{
		{domain.SupervisorToolFailed, "browser_authority_expired", strings.Repeat("0", 64)},
		{domain.SupervisorToolDenied, "policy_denied", toolgateway.AgentBrowserApprovalFingerprint(sensitive)},
		{domain.SupervisorToolCompleted, "", toolgateway.AgentBrowserApprovalFingerprint(sensitive)},
	} {
		raw, _ := json.Marshal(map[string]any{"version": "supervisor_tool_result.v1", "tool": sensitive.ToolName, "status": string(badResult.status), "code": badResult.code, "metadata": map[string]string{"browser_preflight": "not_dispatched", "browser_source_fingerprint": badResult.fingerprint}})
		if _, _, e = st.RecordSupervisorToolResult(ctx, checkpoint, domain.SupervisorToolResult{CallID: sensitive.CallID, Status: badResult.status, ErrorCode: badResult.code, ResultJSON: string(raw), CompletedAt: time.Now().UTC()}); e == nil {
			t.Fatalf("invalid not-started result accepted %+v", badResult)
		}
	}
	fresh, e := st.RecordSupervisorToolExecutionStarted(ctx, checkpoint, sensitive.CallID)
	if e != nil || !fresh {
		t.Fatal(e)
	}
	fresh, e = st.RecordSupervisorToolExecutionStarted(ctx, checkpoint, sensitive.CallID)
	if e != nil || fresh {
		t.Fatal("started gate replayed")
	}
	if _, e = st.EnsureApproval(ctx, proposal); e == nil {
		t.Fatal("already dispatched approval source admitted")
	}
	assertNoForeignKeyViolations(t, st.db)
}
