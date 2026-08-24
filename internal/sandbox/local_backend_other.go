//go:build !windows

package sandbox

import (
	"context"
	"io"
	"runtime"
	"time"
)

type unavailableLocalBackend struct {
	generation string
}

func newPlatformLocalBackend(localBackendConfig) (LocalBackend, error) {
	generation, err := newLocalRuntimeGeneration(
		"local-runtime-generation.v1", runtime.GOOS, runtime.GOARCH,
		LocalBackendPolicyVersion)
	if err != nil {
		return nil, err
	}
	return &unavailableLocalBackend{generation: generation}, nil
}

func (b *unavailableLocalBackend) Generation() string { return b.generation }

func (b *unavailableLocalBackend) Readiness(_ context.Context,
	capabilities LocalRuntimeCapabilities,
) (LocalReadiness, error) {
	now := time.Now().UTC()
	value := LocalReadiness{ProtocolVersion: LocalReadinessProtocolVersion,
		PolicyVersion: LocalBackendPolicyVersion, Backend: LocalBackendName,
		CheckedAt: now, ExpiresAt: now.Add(LocalReadinessTTL),
		RuntimeGeneration: b.generation, FeatureEnabled: capabilities.Enabled,
		Status: LocalReadinessUnavailable, ReasonCode: LocalReasonPlatformUnsupported,
		RemediationCode: LocalRemediationUseSupportedHost}
	if !capabilities.Enabled {
		value.Status = LocalReadinessDisabled
		value.ReasonCode = LocalReasonFeatureDisabled
		value.RemediationCode = LocalRemediationEnableFeature
	}
	value.EvidenceFingerprint = localReadinessFingerprint(value)
	return value, value.Validate()
}

func (*unavailableLocalBackend) Run(context.Context,
	LocalRunRequest,
) (LocalExecutionResult, error) {
	return LocalExecutionResult{}, ErrLocalSandboxUnavailable
}

func (*unavailableLocalBackend) RunWithStdin(_ context.Context,
	_ LocalRunRequest, stdin io.ReadCloser,
) (LocalExecutionResult, error) {
	if stdin != nil {
		_ = stdin.Close()
	}
	return LocalExecutionResult{}, ErrLocalSandboxUnavailable
}

func (*unavailableLocalBackend) Close() error { return nil }
