package desktop

import "cyberagent-workbench/internal/apperror"

const DesktopSandboxRestartProtocolVersion = "desktop_sandbox_restart.v1"

// The native shell reloads its own persisted environment settings. The web
// renderer provides no executable, arguments, image or runtime permission.
type DesktopSandboxRestartRequest struct {
	ProtocolVersion string `json:"protocol_version"`
}

type DesktopSandboxRestartResult struct {
	ProtocolVersion            string                   `json:"protocol_version"`
	Status                     DesktopRiskRestartStatus `json:"status"`
	RestartRequired            bool                     `json:"restart_required"`
	ArbitraryArgumentsAccepted bool                     `json:"arbitrary_arguments_accepted"`
	PersistentRuntimeGrant     bool                     `json:"persistent_runtime_grant"`
}

func (b *DesktopBridge) RestartWithSandboxSettings(request DesktopSandboxRestartRequest) (DesktopSandboxRestartResult, error) {
	if b == nil || !b.bootstrap.RiskProfileRestartEnabled || b.riskProfileRestarter == nil {
		return DesktopSandboxRestartResult{}, apperror.New(apperror.CodeNotFound, "desktop sandbox settings restart is unavailable")
	}
	if request.ProtocolVersion != DesktopSandboxRestartProtocolVersion {
		return DesktopSandboxRestartResult{}, apperror.New(apperror.CodeInvalidArgument, "desktop sandbox restart protocol is invalid")
	}
	if !b.riskRestartActive.CompareAndSwap(false, true) {
		return DesktopSandboxRestartResult{}, apperror.New(apperror.CodeResourceExhausted, "desktop restart is already active")
	}
	ctx, err := b.lifecycleContext()
	if err != nil {
		b.riskRestartActive.Store(false)
		return DesktopSandboxRestartResult{}, err
	}
	profile := DesktopRiskProfileSandbox
	if b.bootstrap.UserTerminalEnabled {
		profile = DesktopRiskProfileSandboxDebug
	}
	restarting, err := b.riskProfileRestarter.ConfirmAndRestart(ctx, profile)
	if err != nil {
		b.riskRestartActive.Store(false)
		return DesktopSandboxRestartResult{}, apperror.Wrap(apperror.CodeUnavailable, "desktop sandbox settings restart could not be prepared", err)
	}
	status := DesktopRiskRestartRestarting
	if !restarting {
		b.riskRestartActive.Store(false)
		status = DesktopRiskRestartCancelled
	}
	return DesktopSandboxRestartResult{ProtocolVersion: DesktopSandboxRestartProtocolVersion,
		Status: status, RestartRequired: true}, nil
}
