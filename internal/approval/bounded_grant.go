package approval

import (
	"errors"
	"time"
)

func boundedGrantPermission(tool, action, mode string) bool {
	if mode == "workspace_access" {
		return true
	} // Preserve existing historical grant validation.
	return tool == "command_runtime" && action == "command_process" && (mode == "ask" || mode == "auto" || mode == "full")
}

// MatchesBoundedGrantScope compares host-resolved ledger bindings. Declared
// scope equality never substitutes for review of a new exact native command.
func MatchesBoundedGrantScope(g SessionGrant, q GrantQuery) bool {
	return g.Bounded() && g.RunID == q.RunID && g.SessionID == q.SessionID && g.WorkspaceID == q.WorkspaceID &&
		g.ToolName == q.ToolName && g.ActionClass == q.ActionClass && g.ScopeFingerprint == q.ScopeFingerprint &&
		g.ModeSnapshotID == q.ModeSnapshotID && g.ModeRevision == q.ModeRevision &&
		g.InteractionSnapshotID == q.InteractionSnapshotID && g.InteractionRevision == q.InteractionRevision &&
		g.ExecutionProfileSnapshotID == q.ExecutionProfileSnapshotID && g.ExecutionProfileRevision == q.ExecutionProfileRevision &&
		g.PermissionSnapshotID == q.PermissionSnapshotID && g.PermissionRevision == q.PermissionRevision && g.PermissionMode == q.PermissionMode &&
		g.WorkspaceRootFingerprint == q.WorkspaceRootFingerprint && g.CapabilityGeneration == q.CapabilityGeneration
}

// CheckBoundedConsumption validates an already consumed exact decision. The
// final legal use exhausts the grant; an explicit revoke operation still vetoes
// it. Exhaustion never authorizes another call or another consumption.
func CheckBoundedConsumption(g SessionGrant, c GrantConsumption, record Record, q GrantQuery, explicitlyRevoked bool, now time.Time) error {
	if g.Validate() != nil || c.Validate() != nil || record.Validate() != nil ||
		!MatchesBoundedGrantScope(g, q) || explicitlyRevoked || g.ExpiresAt == nil || !now.Before(*g.ExpiresAt) ||
		record.Status != StatusApproved || record.GrantID != g.ID || c.GrantID != g.ID ||
		c.ProposalID != record.ProposalID || c.ApprovalID != record.ID || c.RunID != g.RunID ||
		c.ScopeFingerprint != g.ScopeFingerprint || c.GrantGeneration != g.Generation ||
		c.UseOrdinal > g.MaxUses || c.UseOrdinal > g.MaxUses-g.UsesRemaining ||
		c.CreatedAt.Before(g.CreatedAt) || !c.CreatedAt.Before(*g.ExpiresAt) {
		return errors.New("bounded command authorization expired, was revoked, or changed scope")
	}
	if g.Status != GrantActive && (g.Status != GrantRevoked || g.UsesRemaining != 0 ||
		g.RevokedBy != "approval_store" || g.RevocationReason != "bounded grant uses exhausted") {
		return errors.New("bounded command grant is revoked")
	}
	return nil
}
