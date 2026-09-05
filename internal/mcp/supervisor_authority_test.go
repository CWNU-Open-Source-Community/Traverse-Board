package mcp

import (
	"encoding/json"
	"testing"

	"cyberagent-workbench/internal/domain"
)

func TestSupervisorCallAuthorityCodecIsClosedAndCanonical(t *testing.T) {
	authority := SupervisorCallAuthority{Version: SupervisorCallAuthorityVersion,
		RunID: "run-mcp", MissionID: "mission-mcp", WorkspaceID: "workspace-mcp",
		PermissionSnapshotID: "permission-mcp", PermissionRevision: 2,
		PermissionMode:       domain.RunExecutionPermissionFullAccess,
		PermissionGeneration: 3, RunAuthorizationFence: 4}
	encoded, err := EncodeSupervisorCallAuthority(authority)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeSupervisorCallAuthority(append(json.RawMessage(" \n"), encoded...))
	if err != nil || decoded != authority {
		t.Fatalf("decoded=%#v err=%v", decoded, err)
	}
	for _, raw := range []json.RawMessage{
		nil,
		json.RawMessage(`{"version":1}`),
		append(append(json.RawMessage(nil), encoded...), []byte(` {}`)...),
		json.RawMessage(`{"version":1,"run_id":"run-mcp","mission_id":"mission-mcp","workspace_id":"workspace-mcp","permission_snapshot_id":"permission-mcp","permission_revision":2,"permission_mode":"full_access","permission_generation":3,"run_authorization_fence":4,"credential":"forbidden"}`),
	} {
		if _, err := DecodeSupervisorCallAuthority(raw); err == nil {
			t.Fatalf("malformed authority was accepted: %s", raw)
		}
	}
}
