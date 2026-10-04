package application

import (
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/approval"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/toolgateway"
)

func TestCommandBoundedGrantTransactionRetainsBindingsAndLimits(t *testing.T) {
	f := newCommandApprovalFixture(t, domain.RunExecutionPermissionAsk, false)
	f.record(t, boundedCommandInput(t, "1"), 1)
	if waiting, err := f.resume(t); err != nil || !waiting {
		t.Fatal(waiting, err)
	}
	control := NewApprovalControlService(f.st, toolgateway.New(nil, f.checker), f.checker)
	decision, err := control.Decide(t.Context(), boundedCommandRequest(t, f, 2))
	if err != nil {
		t.Fatal(err)
	}
	original := *decision.Grant
	query, err := f.st.GetCommandApprovalGrantScope(t.Context(), f.call.CallID)
	if err != nil {
		t.Fatal(err)
	}
	request := approval.CreateGrantRequest{SessionID: query.SessionID, WorkspaceID: query.WorkspaceID, ToolName: query.ToolName, ActionClass: query.ActionClass,
		Reason: "independent exact review in the existing group", GrantedBy: "operator", IdempotencyKey: "transaction-reuse",
		ScopeFingerprint: query.ScopeFingerprint, Generation: 999, MaxUses: original.MaxUses, TTL: original.ExpiresAt.Sub(original.CreatedAt),
		ModeSnapshotID: query.ModeSnapshotID, ModeRevision: query.ModeRevision, InteractionSnapshotID: query.InteractionSnapshotID, InteractionRevision: query.InteractionRevision,
		ExecutionProfileSnapshotID: query.ExecutionProfileSnapshotID, ExecutionProfileRevision: query.ExecutionProfileRevision,
		PermissionSnapshotID: query.PermissionSnapshotID, PermissionRevision: query.PermissionRevision, PermissionMode: query.PermissionMode,
		WorkspaceRootFingerprint: query.WorkspaceRootFingerprint, CapabilityGeneration: query.CapabilityGeneration}
	for i := 0; i < 2; i++ {
		request.Generation++
		replayed, err := f.st.CreateSessionGrant(t.Context(), request)
		if err != nil || !replayed.Replayed || replayed.Grant.ID != original.ID || replayed.Grant.Generation != original.Generation || !replayed.Grant.ExpiresAt.Equal(*original.ExpiresAt) {
			t.Fatalf("host generation estimate renewed or changed the saved grant: %+v %v", replayed, err)
		}
	}
	for _, limit := range []string{"ttl", "uses"} {
		for _, sameKey := range []bool{true, false} {
			name := limit + "/new_key"
			if sameKey {
				name = limit + "/same_key"
			}
			t.Run(name, func(t *testing.T) {
				changed := request
				if !sameKey {
					changed.IdempotencyKey += "-" + limit
				}
				if limit == "ttl" {
					changed.TTL += time.Second
				} else {
					changed.MaxUses++
				}
				if _, err := f.st.CreateSessionGrant(t.Context(), changed); err == nil {
					t.Fatal("transaction accepted different limits for the active scope")
				}
			})
		}
	}
	mutations := map[string]func(*approval.GrantQuery){
		"run":                    func(q *approval.GrantQuery) { q.RunID += "-other" },
		"session":                func(q *approval.GrantQuery) { q.SessionID += "-other" },
		"workspace":              func(q *approval.GrantQuery) { q.WorkspaceID += "-other" },
		"tool":                   func(q *approval.GrantQuery) { q.ToolName = "host_command_propose" },
		"action":                 func(q *approval.GrantQuery) { q.ActionClass = "risk_escalation" },
		"scope":                  func(q *approval.GrantQuery) { q.ScopeFingerprint = strings.Repeat("0", 64) },
		"mode_snapshot":          func(q *approval.GrantQuery) { q.ModeSnapshotID += "-other" },
		"mode_revision":          func(q *approval.GrantQuery) { q.ModeRevision++ },
		"interaction_snapshot":   func(q *approval.GrantQuery) { q.InteractionSnapshotID += "-other" },
		"interaction_revision":   func(q *approval.GrantQuery) { q.InteractionRevision++ },
		"profile_snapshot":       func(q *approval.GrantQuery) { q.ExecutionProfileSnapshotID += "-other" },
		"profile_revision":       func(q *approval.GrantQuery) { q.ExecutionProfileRevision++ },
		"permission_snapshot":    func(q *approval.GrantQuery) { q.PermissionSnapshotID += "-other" },
		"permission_revision":    func(q *approval.GrantQuery) { q.PermissionRevision++ },
		"permission_mode":        func(q *approval.GrantQuery) { q.PermissionMode = "full" },
		"native_workspace_scope": func(q *approval.GrantQuery) { q.WorkspaceRootFingerprint = strings.Repeat("0", 64) },
		"capability_generation":  func(q *approval.GrantQuery) { q.CapabilityGeneration = strings.Repeat("0", 64) },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			changed := query
			mutate(&changed)
			if _, found, err := f.st.FindActiveSessionGrant(t.Context(), changed); err != nil || found {
				t.Fatalf("exact scope lookup ignored %s: found=%t err=%v", name, found, err)
			}
		})
	}
	stored, err := f.st.GetSessionGrant(t.Context(), original.ID)
	if err != nil || stored.Version != original.Version || stored.Status != original.Status || stored.UsesRemaining != original.UsesRemaining ||
		!stored.CreatedAt.Equal(original.CreatedAt) || !stored.ExpiresAt.Equal(*original.ExpiresAt) || stored.MaxUses != original.MaxUses {
		t.Fatalf("replay, rejected limits or failed scope lookups mutated the saved grant: %+v %v", stored, err)
	}
	if found, ok, err := f.st.FindActiveSessionGrant(t.Context(), query); err != nil || !ok || found.ID != original.ID {
		t.Fatalf("negative checks lost the original valid scope: %+v %t %v", found, ok, err)
	}
	f.assertNoMarker(t, "count.txt")
}
