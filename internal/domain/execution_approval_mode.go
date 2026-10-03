package domain

import "errors"

// ExecutionApprovalMode is the new approval preference, separate from runtime
// adapter availability. Versioned snapshots retain legacy five-mode readers.
type ExecutionApprovalMode string

const (
	ExecutionApprovalAsk  ExecutionApprovalMode = "ask"
	ExecutionApprovalAuto ExecutionApprovalMode = "auto"
	ExecutionApprovalFull ExecutionApprovalMode = "full"
)

func ParseExecutionApprovalMode(value string) (ExecutionApprovalMode, error) {
	switch mode := ExecutionApprovalMode(value); mode {
	case ExecutionApprovalAsk, ExecutionApprovalAuto, ExecutionApprovalFull:
		return mode, nil
	default:
		return "", errors.New("approval mode must be ask, auto, or full")
	}
}

// ApprovalPreference is a read projection only; it must never be used to
// translate a legacy value accepted from a new write request.
func (m RunExecutionPermissionMode) ApprovalPreference() ExecutionApprovalMode {
	switch m {
	case RunExecutionPermissionAsk, RunExecutionPermissionConservative, RunExecutionPermissionWorkspaceAccess, RunExecutionPermissionApproval:
		return ExecutionApprovalAsk
	case RunExecutionPermissionAuto:
		return ExecutionApprovalAuto
	case RunExecutionPermissionFull, RunExecutionPermissionFullAccess, RunExecutionPermissionDebug:
		return ExecutionApprovalFull
	default:
		return ""
	}
}

type LegacyPermissionProjection struct {
	Mode               ExecutionApprovalMode
	RequiresActivation bool
}

// ProjectLegacyExecutionPermission changes only a read projection. It neither
// rewrites stored snapshots/approvals nor creates a process-local grant. A Full
// or Debug history can display Full while remaining unactivated after restart.
func ProjectLegacyExecutionPermission(snapshot RunExecutionPermissionSnapshot) (LegacyPermissionProjection, error) {
	if err := snapshot.Validate(); err != nil {
		return LegacyPermissionProjection{}, err
	}
	switch snapshot.Mode {
	case RunExecutionPermissionConservative, RunExecutionPermissionWorkspaceAccess, RunExecutionPermissionApproval:
		return LegacyPermissionProjection{Mode: ExecutionApprovalAsk}, nil
	case RunExecutionPermissionFullAccess, RunExecutionPermissionDebug:
		return LegacyPermissionProjection{Mode: ExecutionApprovalFull, RequiresActivation: true}, nil
	default:
		return LegacyPermissionProjection{}, errors.New("unsupported legacy permission snapshot")
	}
}

// ExecutionPermissionApproval projects both current and historical snapshots.
// Projection alone never activates a saved Full preference.
func ExecutionPermissionApproval(snapshot RunExecutionPermissionSnapshot) (LegacyPermissionProjection, error) {
	if err := snapshot.Validate(); err != nil {
		return LegacyPermissionProjection{}, err
	}
	if mode, err := ParseExecutionApprovalMode(string(snapshot.Mode)); err == nil {
		return LegacyPermissionProjection{Mode: mode, RequiresActivation: mode == ExecutionApprovalFull}, nil
	}
	return ProjectLegacyExecutionPermission(snapshot)
}
