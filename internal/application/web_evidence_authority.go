package application

import (
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/webevidence"
)

// bindWebEvidenceRuntime uses the same Full contract as native operations.
// Current Full always needs a live grant; retained Full Access startup grants
// can legitimately have generation zero. Ask/Auto keep their exact Run scope.
func bindWebEvidenceRuntime(capabilities domain.ExecutionPermissionRuntimeCapabilities,
	permission domain.RunExecutionPermissionSnapshot,
) (snapshotID string, generation uint64, epoch string, live bool) {
	if !permission.Mode.IsFullPreference() {
		return "", 0, "", true
	}
	generation, live = capabilities.FullAccessGeneration(permission)
	if !live {
		return "", 0, "", false
	}
	if generation == 0 {
		return "", 0, "", true
	}
	epoch = capabilities.RuntimeAuthority.RuntimeEpoch()
	return permission.ID, generation, epoch, epoch != ""
}

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
