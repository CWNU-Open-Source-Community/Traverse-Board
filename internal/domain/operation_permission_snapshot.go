package domain

// New snapshots preserve the existing immutable record/operation identities,
// but no longer select one of five adapter bundles. Each sink must resolve its
// own availability and exact operation authority using the common authorizer.
// These neutral legacy columns are a versioned non-authorizing projection.
func init() {
	for _, mode := range []RunExecutionPermissionMode{RunExecutionPermissionAsk, RunExecutionPermissionAuto, RunExecutionPermissionFull} {
		full := mode == RunExecutionPermissionFull
		risk := ExecutionRiskMinimal
		if full {
			risk = ExecutionRiskHigh
		}
		outOfScope := ExecutionPermissionOutOfScopeExactOnce
		if full {
			outOfScope = ExecutionPermissionOutOfScopeNotNeeded
		}
		runExecutionPermissionDefinitions[mode] = runExecutionPermissionDefinition{
			ApprovalPolicy: "per_operation", CommandScope: "per_operation", FilesystemScope: "per_operation", NetworkScope: "per_operation",
			RiskTier: risk, RequiredGate: "operation_authority", OperatorConfirmed: full,
			CapabilityMatrix: ExecutionPermissionCapabilityMatrix{
				WorkspaceRead: true, WorkspaceWrite: true, SandboxedCommandRuntime: true,
				UnsandboxedHostProcess: true, NetworkAccess: true, CredentialAccess: true, UserHomeAccess: true,
				PersistentUserTerminal: true, PersistentAgentTerminal: true, FullCDP: full, OutOfScopePolicy: outOfScope,
			},
		}
	}
}

// PermissionPolicyTransition allows an explicit one-way transition of the
// existing immutable ledger. Neither a reader nor startup upgrades old rows.
func PermissionPolicyTransition(previousProtocol, previousPolicy, nextProtocol, nextPolicy string, thread bool) bool {
	legacy, current := RunExecutionPermissionProtocolVersion, RunApprovalPermissionProtocolVersion
	if thread {
		legacy, current = ThreadExecutionPermissionProtocolVersion, ThreadApprovalPermissionProtocolVersion
	}
	return (previousProtocol == nextProtocol && previousPolicy == nextPolicy) ||
		(previousProtocol == legacy && previousPolicy == RunExecutionPermissionPolicyVersion &&
			nextProtocol == current && nextPolicy == OperationPermissionPolicyVersion)
}

// PermissionTransitionRevokes also covers Auto -> Ask: newly automatic public
// network work loses its authority immediately, independent of adapter gates.
func PermissionTransitionRevokes(previous, next RunExecutionPermissionMode) bool {
	if previous == RunExecutionPermissionAuto && next == RunExecutionPermissionAsk {
		return true
	}
	return previous == RunExecutionPermissionDebug && next != RunExecutionPermissionDebug ||
		previous.IsFullPreference() && !next.IncludesFullAccess()
}
