package desktop

import (
	"context"
	"errors"
	"testing"

	"cyberagent-workbench/internal/apperror"
)

type testRiskProfileRestarter struct {
	restarting bool
	err        error
	calls      int
	profile    DesktopRiskProfile
}

func (r *testRiskProfileRestarter) ConfirmAndRestart(
	_ context.Context,
	profile DesktopRiskProfile,
) (bool, error) {
	r.calls++
	r.profile = profile
	return r.restarting, r.err
}

func newRiskRestartTestBridge(t *testing.T,
	restarter DesktopRiskProfileRestarter,
) *DesktopBridge {
	t.Helper()
	selector, preview := NewSkillPackagePreviewBoundary()
	bridge, err := NewDesktopBridge(DesktopBridgeConfig{
		ContextProvider:                   func() context.Context { return context.Background() },
		FilePicker:                        &testSkillPackagePicker{},
		ReadToken:                         testDesktopReadToken,
		ControlToken:                      testDesktopControlToken,
		ExecutionPermissionControlEnabled: true,
		OperatorApprovalEnabled:           true,
		RiskProfileRestartEnabled:         true,
		RiskProfileRestarter:              restarter,
		APIVersion:                        "api.v1", AppVersion: "test", UIDigest: testDesktopUIDigest,
		Selector: selector, PreviewBridge: preview,
	})
	if err != nil {
		t.Fatal(err)
	}
	return bridge
}

func TestDesktopRiskRestartRequiresExactProtocolAndProfile(t *testing.T) {
	restarter := &testRiskProfileRestarter{}
	bridge := newRiskRestartTestBridge(t, restarter)
	for _, request := range []DesktopRiskRestartRequest{
		{ProtocolVersion: "desktop_risk_restart." + "v2", Profile: DesktopRiskProfileFullAccess},
		{ProtocolVersion: DesktopRiskRestartProtocolVersion, Profile: DesktopRiskProfileFullAccess},
		{ProtocolVersion: DesktopRiskRestartProtocolVersion, Profile: "shell"},
		{ProtocolVersion: DesktopRiskRestartProtocolVersion},
	} {
		if _, err := bridge.RestartWithRiskProfile(request); apperror.CodeOf(err) != apperror.CodeInvalidArgument {
			t.Fatalf("request %+v error = %v", request, err)
		}
	}
	if restarter.calls != 0 {
		t.Fatalf("native restarter calls = %d, want 0", restarter.calls)
	}
}

func TestDesktopRiskRestartCancellationIsExplicitAndRetryable(t *testing.T) {
	restarter := &testRiskProfileRestarter{restarting: false}
	bridge := newRiskRestartTestBridge(t, restarter)
	request := DesktopRiskRestartRequest{
		ProtocolVersion: DesktopRiskRestartProtocolVersion,
		Profile:         DesktopRiskProfileDebug,
	}
	for attempt := 0; attempt < 2; attempt++ {
		result, err := bridge.RestartWithRiskProfile(request)
		if err != nil {
			t.Fatal(err)
		}
		if result.ProtocolVersion != DesktopRiskRestartProtocolVersion ||
			result.Profile != DesktopRiskProfileDebug ||
			result.Status != DesktopRiskRestartCancelled || !result.RestartRequired ||
			result.ArbitraryArgumentsAccepted || result.PersistentRuntimeGrant {
			t.Fatalf("unexpected cancellation result: %+v", result)
		}
	}
	if restarter.calls != 2 || restarter.profile != DesktopRiskProfileDebug {
		t.Fatalf("native cancellation calls = %d profile=%q", restarter.calls, restarter.profile)
	}
}

func TestDesktopRiskRestartSuccessClosesConcurrentRestartWindow(t *testing.T) {
	restarter := &testRiskProfileRestarter{restarting: true}
	bridge := newRiskRestartTestBridge(t, restarter)
	request := DesktopRiskRestartRequest{
		ProtocolVersion: DesktopRiskRestartProtocolVersion,
		Profile:         DesktopRiskProfileDebug,
	}
	result, err := bridge.RestartWithRiskProfile(request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != DesktopRiskRestartRestarting ||
		result.Profile != DesktopRiskProfileDebug || result.PersistentRuntimeGrant ||
		result.ArbitraryArgumentsAccepted {
		t.Fatalf("unexpected restart result: %+v", result)
	}
	if _, err := bridge.RestartWithRiskProfile(request); apperror.CodeOf(err) != apperror.CodeResourceExhausted {
		t.Fatalf("concurrent restart error = %v", err)
	}
	if restarter.calls != 1 {
		t.Fatalf("native restart calls = %d, want 1", restarter.calls)
	}
}

func TestDesktopRiskRestartNativeFailureIsBoundedAndRetryable(t *testing.T) {
	restarter := &testRiskProfileRestarter{err: errors.New(`C:\PRIVATE\TraverseBoard.exe`)}
	bridge := newRiskRestartTestBridge(t, restarter)
	request := DesktopRiskRestartRequest{
		ProtocolVersion: DesktopRiskRestartProtocolVersion,
		Profile:         DesktopRiskProfileDebug,
	}
	for attempt := 0; attempt < 2; attempt++ {
		_, err := bridge.RestartWithRiskProfile(request)
		if apperror.CodeOf(err) != apperror.CodeUnavailable ||
			err == nil || err.Error() != "desktop risk-profile restart could not be prepared" {
			t.Fatalf("native failure = %v", err)
		}
	}
	if restarter.calls != 2 {
		t.Fatalf("native failure calls = %d, want 2", restarter.calls)
	}
}

func TestDesktopRiskRestartConfigRequiresItsNativeBoundary(t *testing.T) {
	selector, preview := NewSkillPackagePreviewBoundary()
	base := DesktopBridgeConfig{
		ContextProvider:                   func() context.Context { return context.Background() },
		FilePicker:                        &testSkillPackagePicker{},
		ReadToken:                         testDesktopReadToken,
		ControlToken:                      testDesktopControlToken,
		ExecutionPermissionControlEnabled: true,
		OperatorApprovalEnabled:           true,
		APIVersion:                        "api.v1", AppVersion: "test", UIDigest: testDesktopUIDigest,
		Selector: selector, PreviewBridge: preview,
	}
	for _, mutate := range []func(*DesktopBridgeConfig){
		func(config *DesktopBridgeConfig) { config.RiskProfileRestartEnabled = true },
		func(config *DesktopBridgeConfig) {
			config.RiskProfileRestarter = &testRiskProfileRestarter{}
		},
		func(config *DesktopBridgeConfig) {
			config.RiskProfileRestartEnabled = true
			config.RiskProfileRestarter = &testRiskProfileRestarter{}
			config.ExecutionPermissionControlEnabled = false
			config.OperatorApprovalEnabled = false
		},
	} {
		config := base
		mutate(&config)
		if _, err := NewDesktopBridge(config); apperror.CodeOf(err) != apperror.CodeInvalidArgument {
			t.Fatalf("invalid restart config error = %v", err)
		}
	}
}
