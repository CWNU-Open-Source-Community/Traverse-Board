package domain

import "errors"

// ExecutionApprovalMode is the new approval preference, separate from runtime
// adapter availability. Legacy RunExecutionPermissionMode remains read-only
// compatibility input; no existing new-Run writer is switched by this contract.
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
