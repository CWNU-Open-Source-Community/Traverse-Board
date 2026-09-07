package application

import (
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/webevidence"
)

// effectiveWebEvidenceAuthority keeps Provider transport authority separate
// from direct Web evidence authority. Full Access and Debug already grant host
// network access, so their safe Web projection permits arbitrary public HTTPS
// while the fetcher continues to enforce DNS pinning, SSRF, redirect, method,
// response-size, and timeout limits. Narrower permission modes retain the
// exact Run scope and can only widen it through an explicit operator action.
func effectiveWebEvidenceAuthority(scope domain.Scope,
	permission domain.RunExecutionPermissionMode,
) webevidence.NetworkAuthority {
	if permission.IncludesFullAccess() {
		return webevidence.NetworkAuthority{Mode: "allowlist",
			AllowedTargets: []string{webevidence.PublicHTTPSTarget}}
	}
	return webevidence.NetworkAuthority{Mode: scope.NetworkMode,
		AllowedTargets: append([]string(nil), scope.AllowedTargets...)}
}

// effectiveWebEvidenceRobotsPolicy projects the persisted Run permission into
// direct-fetch behavior explicitly. Public-HTTPS authority alone does not make
// robots advisory: only operator-confirmed Full Access and Debug do so.
func effectiveWebEvidenceRobotsPolicy(
	permission domain.RunExecutionPermissionMode,
) webevidence.RobotsPolicy {
	if permission.IncludesFullAccess() {
		return webevidence.RobotsPolicyAuditOnly
	}
	return webevidence.RobotsPolicyEnforce
}
