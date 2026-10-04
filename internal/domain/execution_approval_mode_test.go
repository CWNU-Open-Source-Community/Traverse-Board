package domain

import (
	"reflect"
	"testing"
	"time"
)

func TestExecutionApprovalModeRejectsLegacyAndImplicitDefaults(t *testing.T) {
	for _, value := range []string{"ask", "auto", "full"} {
		got, err := ParseExecutionApprovalMode(value)
		if err != nil || string(got) != value {
			t.Fatalf("mode %q: %v", value, err)
		}
	}
	for _, value := range []string{"", "conservative", "workspace_access", "approval", "full_access", "debug", "ASK", " ask "} {
		if _, err := ParseExecutionApprovalMode(value); err == nil {
			t.Fatalf("accepted mode %q", value)
		}
	}
}

func TestLegacyPermissionProjectionPreservesHistoryAndNeverActivates(t *testing.T) {
	now := time.Now().UTC()
	authority := NewExecutionPermissionRuntimeAuthority()
	for _, mode := range []RunExecutionPermissionMode{RunExecutionPermissionConservative,
		RunExecutionPermissionWorkspaceAccess, RunExecutionPermissionApproval,
		RunExecutionPermissionFullAccess, RunExecutionPermissionDebug} {
		snapshot := newRunExecutionPermissionSnapshot("permission", "run", "mission", 1,
			mode, mode != RunExecutionPermissionConservative, "operator", "legacy fixture", now)
		before := snapshot
		projection, err := ProjectLegacyExecutionPermission(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		want := LegacyPermissionProjection{Mode: ExecutionApprovalAsk}
		if mode.IncludesFullAccess() {
			want = LegacyPermissionProjection{Mode: ExecutionApprovalFull, RequiresActivation: true}
		}
		if projection != want || !reflect.DeepEqual(snapshot, before) {
			t.Fatalf("legacy %s projection altered history", mode)
		}
		if _, allowed := authority.AllowsFullAccess(snapshot); allowed {
			t.Fatal("read projection created a runtime grant")
		}
		snapshot.ExecutionAuthorized = true
		if _, err := ProjectLegacyExecutionPermission(snapshot); err == nil {
			t.Fatal("accepted corrupted legacy authority")
		}
	}
}
