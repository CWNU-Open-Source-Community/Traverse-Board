package desktop

import (
	"testing"

	"cyberagent-workbench/internal/apperror"
)

func TestSandboxRestartPreservesDebugChoiceAndRequiresFixedProtocol(t *testing.T) {
	for _, debug := range []bool{false, true} {
		restarter := &testRiskProfileRestarter{}
		bridge := newRiskRestartTestBridge(t, restarter)
		bridge.bootstrap.UserTerminalEnabled = debug
		if _, err := bridge.RestartWithSandboxSettings(DesktopSandboxRestartRequest{}); apperror.CodeOf(err) != apperror.CodeInvalidArgument || restarter.calls != 0 {
			t.Fatalf("invalid restart reached native shell: %v calls=%d", err, restarter.calls)
		}
		request := DesktopSandboxRestartRequest{ProtocolVersion: DesktopSandboxRestartProtocolVersion}
		result, err := bridge.RestartWithSandboxSettings(request)
		want := DesktopRiskProfileSandbox
		if debug {
			want = DesktopRiskProfileSandboxDebug
		}
		if err != nil || result.Status != DesktopRiskRestartCancelled || restarter.profile != want ||
			result.PersistentRuntimeGrant || result.ArbitraryArgumentsAccepted {
			t.Fatalf("restart=%+v native=%s err=%v", result, restarter.profile, err)
		}
		restarter.restarting = true
		if _, err := bridge.RestartWithSandboxSettings(request); err != nil {
			t.Fatal(err)
		}
		if _, err := bridge.RestartWithSandboxSettings(request); apperror.CodeOf(err) != apperror.CodeResourceExhausted {
			t.Fatalf("duplicate restart accepted: %v", err)
		}
	}
}
