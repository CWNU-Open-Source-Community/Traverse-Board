package application

import (
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/mcp"
	"encoding/json"
	"strings"
	"testing"
)

func TestSupervisorMCPPreparePersistsExactDurableAuthority(t *testing.T) {
	f := newMCPOperationApprovalFixture(t, domain.RunExecutionPermissionFull, false, false)
	authority, err := mcp.DecodeSupervisorCallAuthority(json.RawMessage(f.call.AuthorityJSON))
	permission, permissionErr := f.st.GetRunExecutionPermission(t.Context(), f.call.RunID)
	if err != nil || permissionErr != nil || authority.Version != mcp.SupervisorOperationAuthorityVersion ||
		authority.RunID != f.call.RunID || authority.MissionID != f.turn.Mission.ID || authority.WorkspaceID != f.turn.Mission.WorkspaceID ||
		authority.PermissionSnapshotID != permission.ID || authority.PermissionRuntimeEpoch != f.capabilities.RuntimeAuthority.RuntimeEpoch() ||
		!authority.MatchesServer(f.server) {
		t.Fatalf("durable source/permission binding: %+v err=%v %v", authority, err, permissionErr)
	}
	const passwordCanary = "MCP_RESULT_PASSWORD_CANARY_SHORT_PHRASE"
	const authCanary = "MCP_RESULT_AUTH_CANARY_ORDINARY_PHRASE"
	f.response.Store(json.RawMessage(`{"content":[{"type":"text","text":"fixture complete"}],"structuredContent":{"password":"` + passwordCanary + `","nested":{"auth_header":"` + authCanary + `"},"status":"created"}}`))
	if waiting, err := f.resume(t); err != nil || waiting || f.calls.Load() != 1 {
		t.Fatal("real MCP execution failed", waiting, err)
	}
	call, started, err := f.st.GetSupervisorApprovalCall(t.Context(), f.call.RunID, f.call.CallID)
	if err != nil || !started || call.Status != domain.SupervisorToolCompleted || call.AuthorityJSON != f.call.AuthorityJSON ||
		strings.Contains(call.ResultJSON, passwordCanary) || strings.Contains(call.ResultJSON, authCanary) ||
		!strings.Contains(call.ResultJSON, "[REDACTED:sensitive-field]") || !strings.Contains(call.ResultJSON, "created") {
		t.Fatalf("MCP durable authority/redaction changed: %+v %v", call, err)
	}
}
