package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/sandbox"
)

type dockerSandboxCapabilityFlags struct {
	execution         *bool
	permissionControl *bool
	dangerFullAccess  *bool
	debugMaximum      *bool
}

func addDockerSandboxCapabilityFlags(fs *flag.FlagSet) dockerSandboxCapabilityFlags {
	return dockerSandboxCapabilityFlags{
		execution: fs.Bool("enable-docker-execution", false,
			"enable the process-local Docker Sandbox execution capability"),
		permissionControl: fs.Bool("enable-permission-control", false,
			"enable operator-approved execution permissions in this process"),
		dangerFullAccess: fs.Bool("enable-danger-full-access", false,
			"enable the danger-full-access process capability"),
		debugMaximum: fs.Bool("enable-debug-maximum-access", false,
			"enable the maximum Debug process capability"),
	}
}

func (value dockerSandboxCapabilityFlags) capabilities(requireExecution bool) (
	bool, domain.ExecutionPermissionRuntimeCapabilities, error,
) {
	if value.execution == nil || value.permissionControl == nil ||
		value.dangerFullAccess == nil || value.debugMaximum == nil {
		return false, domain.ExecutionPermissionRuntimeCapabilities{},
			errors.New("Docker Sandbox capability flags are unavailable")
	}
	if requireExecution && !*value.execution {
		return false, domain.ExecutionPermissionRuntimeCapabilities{}, apperror.New(
			apperror.CodeInvalidArgument,
			"Docker Sandbox admission and start require --enable-docker-execution")
	}
	if *value.execution && !*value.permissionControl {
		return false, domain.ExecutionPermissionRuntimeCapabilities{}, apperror.New(
			apperror.CodeInvalidArgument,
			"--enable-docker-execution requires --enable-permission-control")
	}
	capabilities := domain.ExecutionPermissionRuntimeCapabilities{
		OperatorApprovalEnabled:   *value.permissionControl,
		DangerFullAccessEnabled:   *value.dangerFullAccess,
		DebugMaximumAccessEnabled: *value.debugMaximum,
	}
	if err := capabilities.Validate(); err != nil {
		return false, domain.ExecutionPermissionRuntimeCapabilities{},
			apperror.Wrap(apperror.CodeInvalidArgument, err.Error(), err)
	}
	return *value.execution, capabilities, nil
}

func dockerSandboxCapabilityFlagShape() map[string]bool {
	return map[string]bool{
		"enable-docker-execution":     false,
		"enable-permission-control":   false,
		"enable-danger-full-access":   false,
		"enable-debug-maximum-access": false,
	}
}

func (a *App) runDockerSandboxProduct(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("Docker Sandbox product subcommand is required")
	}
	switch args[0] {
	case "docker-readiness":
		return a.runDockerSandboxReadiness(ctx, args[1:])
	case "docker-admit":
		return a.runDockerSandboxAdmit(ctx, args[1:])
	case "docker-start":
		return a.runDockerSandboxStart(ctx, args[1:])
	case "docker-cancel":
		return a.runDockerSandboxCancel(ctx, args[1:])
	case "docker-status":
		return a.runDockerSandboxStatus(ctx, args[1:])
	default:
		return fmt.Errorf("unknown Docker Sandbox product subcommand %q", args[0])
	}
}

func (a *App) runDockerSandboxReadiness(ctx context.Context, args []string) error {
	fs := newFlagSet("run sandbox docker-readiness", a.errOut)
	manifestPath := fs.String("manifest-file", "", "sandbox manifest JSON file")
	capabilityFlags := addDockerSandboxCapabilityFlags(fs)
	shape := dockerSandboxCapabilityFlagShape()
	shape["manifest-file"] = true
	if err := fs.Parse(reorderFlags(args, shape)); err != nil {
		return err
	}
	if fs.NArg() != 1 || strings.TrimSpace(*manifestPath) == "" {
		return errors.New("usage: cyberagent run sandbox docker-readiness <plan-id> --manifest-file <manifest.json> [process capability flags]")
	}
	manifest, err := readSandboxManifest(*manifestPath)
	if err != nil {
		return err
	}
	enabled, permissionCapabilities, err := capabilityFlags.capabilities(false)
	if err != nil {
		return err
	}
	service, err := a.newDockerSandboxService(enabled, permissionCapabilities)
	if err != nil {
		return err
	}
	readiness, err := service.Readiness(ctx, application.DockerSandboxReadinessRequest{
		PlanID: fs.Arg(0), Manifest: manifest,
	})
	if err != nil {
		return err
	}
	printDockerSandboxReadiness(a, readiness)
	return nil
}

func (a *App) runDockerSandboxAdmit(ctx context.Context, args []string) error {
	fs := newFlagSet("run sandbox docker-admit", a.errOut)
	manifestPath := fs.String("manifest-file", "", "sandbox manifest JSON file")
	operationKey := fs.String("operation-key", "", "stable admission operation key")
	operator := fs.String("operator", "cli_operator", "requesting operator identity")
	capabilityFlags := addDockerSandboxCapabilityFlags(fs)
	shape := dockerSandboxCapabilityFlagShape()
	shape["manifest-file"], shape["operation-key"], shape["operator"] = true, true, true
	if err := fs.Parse(reorderFlags(args, shape)); err != nil {
		return err
	}
	if fs.NArg() != 1 || strings.TrimSpace(*manifestPath) == "" ||
		strings.TrimSpace(*operationKey) == "" {
		return errors.New("usage: cyberagent run sandbox docker-admit <plan-id> --manifest-file <manifest.json> --operation-key <key> --enable-docker-execution --enable-permission-control [permission capability flags] [--operator <id>]")
	}
	manifest, err := readSandboxManifest(*manifestPath)
	if err != nil {
		return err
	}
	enabled, permissionCapabilities, err := capabilityFlags.capabilities(true)
	if err != nil {
		return err
	}
	service, err := a.newDockerSandboxService(enabled, permissionCapabilities)
	if err != nil {
		return err
	}
	result, err := service.Admit(ctx, application.DockerSandboxAdmissionRequest{
		PlanID: fs.Arg(0), Manifest: manifest, OperationKey: *operationKey,
		RequestedBy: *operator,
	})
	if err != nil {
		return err
	}
	printDockerSandboxAdmission(a, result)
	if !result.Allowed {
		return apperror.New(apperror.CodePolicyDenied,
			"Docker Sandbox admission denied: "+result.ReasonCode)
	}
	return nil
}

func (a *App) runDockerSandboxStart(ctx context.Context, args []string) error {
	fs := newFlagSet("run sandbox docker-start", a.errOut)
	manifestPath := fs.String("manifest-file", "", "sandbox manifest JSON file")
	admissionKey := fs.String("admission-operation-key", "", "stable admission operation key")
	operationKey := fs.String("operation-key", "", "stable start operation key")
	operator := fs.String("operator", "cli_operator", "requesting operator identity")
	capabilityFlags := addDockerSandboxCapabilityFlags(fs)
	shape := dockerSandboxCapabilityFlagShape()
	shape["manifest-file"], shape["admission-operation-key"] = true, true
	shape["operation-key"], shape["operator"] = true, true
	if err := fs.Parse(reorderFlags(args, shape)); err != nil {
		return err
	}
	if fs.NArg() != 1 || strings.TrimSpace(*manifestPath) == "" ||
		strings.TrimSpace(*admissionKey) == "" || strings.TrimSpace(*operationKey) == "" {
		return errors.New("usage: cyberagent run sandbox docker-start <plan-id> --manifest-file <manifest.json> --admission-operation-key <key> --operation-key <key> --enable-docker-execution --enable-permission-control [permission capability flags] [--operator <id>]")
	}
	manifest, err := readSandboxManifest(*manifestPath)
	if err != nil {
		return err
	}
	enabled, permissionCapabilities, err := capabilityFlags.capabilities(true)
	if err != nil {
		return err
	}
	service, err := a.newDockerSandboxService(enabled, permissionCapabilities)
	if err != nil {
		return err
	}
	admission, err := service.Admit(ctx, application.DockerSandboxAdmissionRequest{
		PlanID: fs.Arg(0), Manifest: manifest, OperationKey: *admissionKey,
		RequestedBy: *operator,
	})
	if err != nil {
		return err
	}
	printDockerSandboxAdmission(a, admission)
	if !admission.Allowed || admission.Admission == nil {
		return apperror.New(apperror.CodePolicyDenied,
			"Docker Sandbox start admission denied: "+admission.ReasonCode)
	}
	result, err := service.Start(ctx, application.DockerSandboxStartRequest{
		AdmissionID: admission.Admission.ID, OperationKey: *operationKey,
		RequestedBy: *operator,
	})
	if result.Record.Admission.ID != "" {
		printDockerSandboxRecord(a, result.Record, result.Replayed)
	}
	return err
}

func (a *App) runDockerSandboxCancel(ctx context.Context, args []string) error {
	fs := newFlagSet("run sandbox docker-cancel", a.errOut)
	operationKey := fs.String("operation-key", "", "stable cancellation operation key")
	operator := fs.String("operator", "cli_operator", "requesting operator identity")
	if err := fs.Parse(reorderFlags(args,
		map[string]bool{"operation-key": true, "operator": true})); err != nil {
		return err
	}
	if fs.NArg() != 1 || strings.TrimSpace(*operationKey) == "" {
		return errors.New("usage: cyberagent run sandbox docker-cancel <admission-id> --operation-key <key> [--operator <id>]")
	}
	service, err := a.newDockerSandboxService(false,
		domain.ExecutionPermissionRuntimeCapabilities{})
	if err != nil {
		return err
	}
	result, err := service.Cancel(ctx, application.DockerSandboxCancelRequest{
		AdmissionID: fs.Arg(0), OperationKey: *operationKey, RequestedBy: *operator,
	})
	if result.Cancellation.ID != "" {
		fmt.Fprintf(a.out, "cancellation_id: %s\ncancellation_reason: %s\nreplayed: %t\n",
			result.Cancellation.ID, result.Cancellation.ReasonCode, result.Replayed)
	}
	if result.Record.Admission.ID != "" {
		printDockerSandboxRecord(a, result.Record, result.Replayed)
	}
	return err
}

func (a *App) runDockerSandboxStatus(ctx context.Context, args []string) error {
	fs := newFlagSet("run sandbox docker-status", a.errOut)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: cyberagent run sandbox docker-status <admission-id>")
	}
	service, err := a.newDockerSandboxService(false,
		domain.ExecutionPermissionRuntimeCapabilities{})
	if err != nil {
		return err
	}
	record, err := service.Get(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	printDockerSandboxRecord(a, record, record.Replayed)
	return nil
}

func printDockerSandboxReadiness(a *App, value sandbox.DockerReadiness) {
	fmt.Fprintf(a.out, "protocol: %s\nstatus: %s\nready: %t\nfeature_enabled: %t\nreason_code: %s\nremediation_code: %s\nchecked_at: %s\nexpires_at: %s\nreadiness_fingerprint: %s\n",
		value.ProtocolVersion, value.Status, value.Ready, value.FeatureEnabled,
		value.ReasonCode, value.RemediationCode,
		value.CheckedAt.Format(timeFormatRFC3339Nano),
		value.ExpiresAt.Format(timeFormatRFC3339Nano), value.ReadinessFingerprint)
}

func printDockerSandboxAdmission(a *App,
	value application.DockerSandboxAdmissionResult,
) {
	printDockerSandboxReadiness(a, value.Readiness)
	fmt.Fprintf(a.out, "admission_allowed: %t\nadmission_reason_code: %s\nadmission_remediation_code: %s\nreplayed: %t\n",
		value.Allowed, value.ReasonCode, value.RemediationCode, value.Replayed)
	if value.Admission != nil {
		fmt.Fprintf(a.out, "admission_id: %s\nrun_id: %s\nplan_id: %s\nadmission_fingerprint: %s\n",
			value.Admission.ID, value.Admission.RunID, value.Admission.PlanID,
			value.Admission.AdmissionFingerprint)
	}
}

func printDockerSandboxRecord(a *App, value domain.DockerSandboxRecord,
	replayed bool,
) {
	state := "admitted"
	if value.Launch != nil {
		state = "launched"
	}
	if value.Receipt != nil {
		state = "terminal"
	}
	fmt.Fprintf(a.out, "protocol: %s\nadmission_id: %s\nrun_id: %s\nplan_id: %s\nstate: %s\ndecision: %s\nreason_code: %s\nremediation_code: %s\nreplayed: %t\n",
		value.Admission.ProtocolVersion, value.Admission.ID, value.Admission.RunID,
		value.Admission.PlanID, state, value.Admission.Decision,
		value.Admission.ReasonCode, value.Admission.RemediationCode, replayed)
	if value.Launch != nil {
		fmt.Fprintf(a.out, "attempt_id: %s\nlifecycle_intent_id: %s\n",
			value.Launch.AttemptID, value.Launch.LifecycleIntentID)
	}
	if value.Receipt != nil {
		fmt.Fprintf(a.out, "outcome: %s\nreceipt_reason_code: %s\ncleanup_complete: %t\nartifact_count: %d\ncompleted_at: %s\n",
			value.Receipt.Outcome, value.Receipt.ReasonCode,
			value.Receipt.CleanupComplete, value.Receipt.ArtifactCount,
			value.Receipt.CompletedAt.Format(timeFormatRFC3339Nano))
		if value.Receipt.ExitCode != nil {
			fmt.Fprintf(a.out, "exit_code: %d\n", *value.Receipt.ExitCode)
		}
	}
}
