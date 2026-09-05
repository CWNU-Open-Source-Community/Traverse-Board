package application

import (
	"testing"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/webevidence"
)

func TestEffectiveWebEvidenceAuthorityUsesPublicHTTPSForFullAndDebug(t *testing.T) {
	t.Parallel()
	for _, permission := range []domain.RunExecutionPermissionMode{
		domain.RunExecutionPermissionFullAccess,
		domain.RunExecutionPermissionDebug,
	} {
		authority := effectiveWebEvidenceAuthority(domain.Scope{NetworkMode: "disabled"}, permission)
		if authority.Mode != "allowlist" || len(authority.AllowedTargets) != 1 ||
			authority.AllowedTargets[0] != webevidence.PublicHTTPSTarget {
			t.Fatalf("permission %s authority = %#v", permission, authority)
		}
		if _, err := authority.Authorize("https://docs.example.org/reference"); err != nil {
			t.Fatalf("permission %s did not authorize public HTTPS: %v", permission, err)
		}
		for _, target := range []string{
			"http://docs.example.org/", "https://localhost/", "https://127.0.0.1/",
			"https://169.254.169.254/latest/meta-data/",
		} {
			if _, err := authority.Authorize(target); err == nil {
				t.Fatalf("permission %s authorized unsafe target %q", permission, target)
			}
		}
	}
}

func TestEffectiveWebEvidenceAuthorityPreservesExactScopeForNarrowModes(t *testing.T) {
	t.Parallel()
	scope := domain.Scope{NetworkMode: "allowlist",
		AllowedTargets: []string{"docs.example.org"}}
	for _, permission := range []domain.RunExecutionPermissionMode{
		domain.RunExecutionPermissionConservative,
		domain.RunExecutionPermissionWorkspaceAccess,
		domain.RunExecutionPermissionApproval,
	} {
		authority := effectiveWebEvidenceAuthority(scope, permission)
		if len(authority.AllowedTargets) != 1 || authority.AllowedTargets[0] != "docs.example.org" {
			t.Fatalf("permission %s authority = %#v", permission, authority)
		}
		if _, err := authority.Authorize("https://other.example.org/"); err == nil {
			t.Fatalf("permission %s widened exact authority", permission)
		}
	}
	// The projection must not share the persisted slice with callers.
	authority := effectiveWebEvidenceAuthority(scope, domain.RunExecutionPermissionConservative)
	authority.AllowedTargets[0] = "changed.example.org"
	if scope.AllowedTargets[0] != "docs.example.org" {
		t.Fatal("effective authority mutated the durable Run scope")
	}
}

func TestEffectiveWebEvidenceRobotsPolicyComesFromPermission(t *testing.T) {
	t.Parallel()
	for _, permission := range []domain.RunExecutionPermissionMode{
		domain.RunExecutionPermissionFullAccess,
		domain.RunExecutionPermissionDebug,
	} {
		if policy := effectiveWebEvidenceRobotsPolicy(permission); policy != webevidence.RobotsPolicyAuditOnly {
			t.Fatalf("permission %s robots policy=%q", permission, policy)
		}
	}
	for _, permission := range []domain.RunExecutionPermissionMode{
		domain.RunExecutionPermissionConservative,
		domain.RunExecutionPermissionWorkspaceAccess,
		domain.RunExecutionPermissionApproval,
	} {
		if policy := effectiveWebEvidenceRobotsPolicy(permission); policy != webevidence.RobotsPolicyEnforce {
			t.Fatalf("permission %s robots policy=%q", permission, policy)
		}
	}
}
