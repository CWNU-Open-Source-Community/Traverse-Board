package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"cyberagent-workbench/internal/sandbox"
)

func openLocalSandbox(ctx context.Context, enabled bool) (
	sandbox.LocalBackend, sandbox.LocalReadiness, error,
) {
	backend, err := sandbox.NewPlatformLocalBackend()
	if err != nil {
		return nil, sandbox.LocalReadiness{}, err
	}
	readiness, err := backend.Readiness(ctx,
		sandbox.LocalRuntimeCapabilities{Enabled: enabled})
	if err == nil {
		err = readiness.Validate()
	}
	if err != nil {
		return nil, sandbox.LocalReadiness{}, errors.Join(err, backend.Close())
	}
	return backend, readiness, nil
}

func writeLocalSandboxReadiness(out interface{ Write([]byte) (int, error) },
	readiness sandbox.LocalReadiness, jsonOutput bool,
) error {
	if err := readiness.Validate(); err != nil {
		return err
	}
	if jsonOutput {
		encoder := json.NewEncoder(out)
		encoder.SetEscapeHTML(true)
		return encoder.Encode(readiness)
	}
	_, err := fmt.Fprintf(out, "protocol: %s\npolicy: %s\nbackend: %s\nstatus: %s\nreason: %s\nremediation: %s\nchecked_at: %s\nexpires_at: %s\nevidence_fingerprint: %s\nruntime_generation: %s\nfeature_enabled: %t\nwindows_host: %t\nx64_host: %t\npersistent_acls: %t\nappcontainer_profile: %t\nappcontainer_token: %t\nrestricted_token: %t\nlow_integrity_token: %t\nzero_network_capabilities: %t\nwfp_default_deny: %t\ncreation_time_job_binding: %t\nkill_on_job_close: %t\nbounded_handle_inheritance: %t\nclean_environment: %t\nephemeral_profile: %t\nfilesystem_proven: %t\nprocess_proven: %t\nnetwork_proven: %t\ncredential_proven: %t\nready: %t\ncapability_grant: false\n",
		readiness.ProtocolVersion, readiness.PolicyVersion, readiness.Backend,
		readiness.Status, readiness.ReasonCode, readiness.RemediationCode,
		readiness.CheckedAt.Format("2006-01-02T15:04:05.999999999Z07:00"),
		readiness.ExpiresAt.Format("2006-01-02T15:04:05.999999999Z07:00"),
		readiness.EvidenceFingerprint, readiness.RuntimeGeneration,
		readiness.FeatureEnabled, readiness.WindowsHost, readiness.X64Host,
		readiness.PersistentACLs, readiness.AppContainerProfile,
		readiness.AppContainerToken, readiness.RestrictedToken,
		readiness.LowIntegrityToken, readiness.ZeroNetworkCapabilities,
		readiness.WFPDefaultDeny, readiness.CreationTimeJobBinding,
		readiness.KillOnJobClose, readiness.BoundedHandleInheritance,
		readiness.CleanEnvironment, readiness.EphemeralProfile,
		readiness.FilesystemProven, readiness.ProcessProven,
		readiness.NetworkProven, readiness.CredentialProven, readiness.Ready)
	return err
}
