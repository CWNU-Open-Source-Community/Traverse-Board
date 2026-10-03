package toolgateway

import (
	"testing"

	"cyberagent-workbench/internal/domain"
)

func TestWebEvidenceAskFetchRequiresInlineApprovalScheduler(t *testing.T) {
	scope := testWebEvidenceCapabilityContext()
	scope.PermissionMode = domain.RunExecutionPermissionAsk
	scope.NetworkMode, scope.AllowedTargets = "disabled", nil
	scope.InlineWebFetchApprovalAvailable = false
	withoutScheduler := WebEvidenceCapabilitySnapshot(scope)
	if withoutScheduler.Available || withoutScheduler.FetchAvailable || withoutScheduler.SearchAvailable {
		t.Fatalf("Ask without a scheduler advertised network tools: %#v", withoutScheduler)
	}
	if _, err := NewWebEvidenceCallAuthority(scope); err == nil {
		t.Fatal("Ask without a scheduler produced call authority")
	}

	scope.InlineWebFetchApprovalAvailable = true
	withScheduler := WebEvidenceCapabilitySnapshot(scope)
	if !withScheduler.Available || !withScheduler.FetchAvailable || withScheduler.SearchAvailable ||
		withScheduler.SourceSearchAvailable || withScheduler.Generation == withoutScheduler.Generation {
		t.Fatalf("Ask did not advertise only the exact-review fetch route: %#v", withScheduler)
	}
	authority, err := NewWebEvidenceCallAuthority(scope)
	if err != nil || authority.PermissionMode != domain.RunExecutionPermissionAsk ||
		authority.NetworkMode != "disabled" || len(authority.AllowedTargets) != 0 {
		t.Fatalf("advertisement changed Run network authority: %#v err=%v", authority, err)
	}
}
