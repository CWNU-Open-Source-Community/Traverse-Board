package httpapi

import "cyberagent-workbench/internal/domain"

func approvalFullActivation(mode domain.RunExecutionPermissionMode, active bool, runtime domain.ExecutionPermissionRuntimeCapabilities) (string, string) {
	if !runtime.Allows(domain.RunExecutionPermissionFull) {
		return "unavailable", "Full runtime activation is unavailable in this process."
	}
	// A historic Full/Debug preference can be displayed as Full, but its old
	// startup flags are never presented as a new three-mode activation.
	if mode == domain.RunExecutionPermissionFull && active {
		return "active", ""
	}
	return "inactive", ""
}
