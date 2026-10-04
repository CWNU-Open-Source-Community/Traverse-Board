package application

import (
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/webevidence"
)

func TestWebEvidenceRuntimeBindingPreservesCurrentAndHistoricalFullContracts(t *testing.T) {
	at := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	ask, err := domain.NewInitialRunExecutionPermissionSnapshot("web-ask", domain.Run{
		ID: "web-run", MissionID: "web-mission"}, domain.Mission{ID: "web-mission"}, "operator", at)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []domain.RunExecutionPermissionMode{domain.RunExecutionPermissionAsk, domain.RunExecutionPermissionAuto} {
		permission := ask
		if mode != permission.Mode {
			permission, err = ask.Next("web-auto", mode, false, "operator", "exact Run scope", at)
			if err != nil {
				t.Fatal(err)
			}
		}
		if id, generation, epoch, live := bindWebEvidenceRuntime(domain.ExecutionPermissionRuntimeCapabilities{}, permission); !live || id != "" || generation != 0 || epoch != "" {
			t.Fatalf("%s acquired or required a Full binding", mode)
		}
	}
	for _, mode := range []domain.RunExecutionPermissionMode{domain.RunExecutionPermissionFull, domain.RunExecutionPermissionFullAccess} {
		permission, err := ask.Next("web-full", mode, true, "operator", "confirmed Full preference", at)
		if err != nil {
			t.Fatal(err)
		}
		for _, dynamic := range []bool{false, true} {
			authority := domain.NewExecutionPermissionRuntimeAuthority()
			caps := domain.ExecutionPermissionRuntimeCapabilities{OperatorApprovalEnabled: true,
				DangerFullAccessEnabled: true, FullAccessRequiresRuntimeGrant: dynamic, RuntimeAuthority: authority}
			_, _, _, coldAllowed := bindWebEvidenceRuntime(caps, permission)
			staticHistorical := mode == domain.RunExecutionPermissionFullAccess && !dynamic
			if coldAllowed != staticHistorical {
				t.Fatalf("mode=%s dynamic=%t cold=%t", mode, dynamic, coldAllowed)
			}
			grant, err := authority.ActivateRunFullAccess(permission)
			if err != nil {
				t.Fatal(err)
			}
			id, generation, epoch, live := bindWebEvidenceRuntime(caps, permission)
			if !live {
				t.Fatalf("live %s dynamic=%t denied", mode, dynamic)
			}
			if staticHistorical {
				if id != "" || generation != 0 || epoch != "" {
					t.Fatal("historical static grant changed its persisted binding")
				}
			} else if id != permission.ID || generation != grant.Generation || epoch != authority.RuntimeEpoch() {
				t.Fatal("live binding lost exact snapshot, generation or epoch")
			}
			authority.RevokeRun(permission.RunID)
			if _, _, _, live := bindWebEvidenceRuntime(caps, permission); live != staticHistorical {
				t.Fatalf("mode=%s dynamic=%t revoked=%t", mode, dynamic, live)
			}
			caps.DangerFullAccessEnabled = false
			if _, _, _, live := bindWebEvidenceRuntime(caps, permission); live {
				t.Fatal("Full exceeded the process capability ceiling")
			}
		}
	}
}

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
