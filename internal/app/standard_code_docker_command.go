package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/repository"
	"cyberagent-workbench/internal/sandbox"
	"cyberagent-workbench/internal/standardcode"
)

const standardCodeDockerImageEnvironment = "CYBERAGENT_STANDARD_CODE_DOCKER_IMAGE_DIGEST"

func (a *App) runStandardCode(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: cyberagent run standard-code docker-readiness|docker-prepare|docker-execute|docker-cancel|docker-recover")
	}
	switch args[0] {
	case "docker-readiness", "docker-prepare", "docker-execute":
		return a.runStandardCodeBoundCommand(ctx, args[0], args[1:])
	case "docker-cancel":
		return a.runStandardCodeCancel(ctx, args[1:])
	case "docker-recover":
		return a.runStandardCodeRecover(ctx, args[1:])
	default:
		return fmt.Errorf("unknown Standard Code subcommand %q", args[0])
	}
}

func (a *App) runStandardCodeBoundCommand(ctx context.Context, action string,
	args []string,
) error {
	fs := newFlagSet("run standard-code "+action, a.errOut)
	generation := fs.Int64("generation", 0, "exact current Drydock generation")
	checkpoint := fs.String("checkpoint", "", "exact current Drydock Checkpoint identity")
	toolchain := fs.String("toolchain", "", "fixed offline toolchain: go, node, python, or rust")
	workingDirectory := fs.String("cwd", ".", "normalized path relative to the Drydock root")
	timeout := fs.Duration("timeout", 10*time.Minute, "bounded command timeout")
	purpose := fs.String("purpose", "", "operator-visible command purpose")
	operationKey := fs.String("operation-key", "", "stable operation key")
	requestedBy := fs.String("operator", "cli_operator", "operator identity")
	preparationID := fs.String("preparation", "", "exact prepared Standard Code intent")
	approvalID := fs.String("approval", "", "approved exact per-call approval")
	dockerEnabled := fs.Bool("enable-docker-execution", false,
		"enable the process-local fixed Docker execution capability")
	permissionControl := fs.Bool("enable-permission-control", false,
		"enable operator approval control")
	workspaceSandbox := fs.Bool("enable-workspace-sandbox", false,
		"enable the process-local Workspace sandbox capability")
	var arguments multiStringFlag
	fs.Var(&arguments, "arg", "command argument (repeatable; never interpreted by a shell)")
	shape := map[string]bool{"generation": true, "checkpoint": true,
		"toolchain": true, "cwd": true, "timeout": true, "purpose": true,
		"operation-key": true, "operator": true, "preparation": true,
		"approval": true, "arg": true, "enable-docker-execution": false,
		"enable-permission-control": false, "enable-workspace-sandbox": false}
	if err := fs.Parse(reorderFlags(args, shape)); err != nil {
		return err
	}
	if fs.NArg() != 1 || *generation < 1 || strings.TrimSpace(*checkpoint) == "" ||
		strings.TrimSpace(*toolchain) == "" || strings.TrimSpace(*purpose) == "" ||
		*timeout < time.Second || *timeout%time.Second != 0 ||
		!*permissionControl || !*workspaceSandbox ||
		(action == "docker-execute" && !*dockerEnabled) {
		return errors.New("usage: cyberagent run standard-code " + action +
			" <run-id> --generation <n> --checkpoint <id> --toolchain go|node|python|rust --purpose <text> [--arg <value> ...] [--cwd <relative-path>] [--timeout <duration>] --enable-permission-control --enable-workspace-sandbox [--enable-docker-execution]")
	}
	command := standardcode.Command{ProtocolVersion: standardcode.CommandProtocolVersion,
		Toolchain: strings.TrimSpace(*toolchain), Arguments: arguments.values,
		WorkingDirectory: strings.TrimSpace(*workingDirectory),
		TimeoutSeconds:   int(timeout.Seconds()), Purpose: strings.TrimSpace(*purpose)}
	service, err := a.newStandardCodeDockerService(*dockerEnabled,
		*permissionControl, *workspaceSandbox)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(a.out)
	encoder.SetIndent("", "  ")
	switch action {
	case "docker-readiness":
		value, err := service.Readiness(ctx, application.StandardCodeDockerReadinessRequest{
			RunID: fs.Arg(0), ExpectedGeneration: *generation,
			ExpectedCheckpoint: *checkpoint, Command: command})
		if err != nil {
			return err
		}
		return encoder.Encode(value)
	case "docker-prepare":
		if strings.TrimSpace(*operationKey) == "" {
			return errors.New("docker-prepare requires --operation-key")
		}
		value, err := service.Prepare(ctx, application.StandardCodeDockerPrepareRequest{
			RunID: fs.Arg(0), ExpectedGeneration: *generation,
			ExpectedCheckpoint: *checkpoint, OperationKey: *operationKey,
			RequestedBy: *requestedBy, Command: command})
		if err != nil {
			return err
		}
		return encoder.Encode(value)
	case "docker-execute":
		if strings.TrimSpace(*operationKey) == "" ||
			strings.TrimSpace(*preparationID) == "" || strings.TrimSpace(*approvalID) == "" {
			return errors.New("docker-execute requires --preparation, --approval, and --operation-key")
		}
		if _, err := service.RecoverStartup(ctx); err != nil {
			return err
		}
		value, err := service.Execute(ctx, application.StandardCodeDockerExecuteRequest{
			RunID: fs.Arg(0), ExpectedGeneration: *generation,
			ExpectedCheckpoint: *checkpoint, PreparationID: *preparationID,
			ApprovalID: *approvalID, OperationKey: *operationKey,
			RequestedBy: *requestedBy, Command: command})
		if err != nil {
			return err
		}
		return encoder.Encode(value)
	default:
		return errors.New("unsupported Standard Code action")
	}
}

func (a *App) runStandardCodeCancel(ctx context.Context, args []string) error {
	fs := newFlagSet("run standard-code docker-cancel", a.errOut)
	operationKey := fs.String("operation-key", "", "stable cancellation operation key")
	requestedBy := fs.String("operator", "cli_operator", "operator identity")
	if err := fs.Parse(reorderFlags(args, map[string]bool{
		"operation-key": true, "operator": true})); err != nil {
		return err
	}
	if fs.NArg() != 1 || strings.TrimSpace(*operationKey) == "" {
		return errors.New("usage: cyberagent run standard-code docker-cancel <admission-id> --operation-key <key> [--operator <id>]")
	}
	service, err := a.newStandardCodeDockerService(false, false, false)
	if err != nil {
		return err
	}
	value, err := service.Cancel(ctx, application.StandardCodeDockerCancelRequest{
		AdmissionID: fs.Arg(0), OperationKey: *operationKey, RequestedBy: *requestedBy})
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(a.out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func (a *App) runStandardCodeRecover(ctx context.Context, args []string) error {
	fs := newFlagSet("run standard-code docker-recover", a.errOut)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: cyberagent run standard-code docker-recover")
	}
	service, err := a.newStandardCodeDockerService(false, false, false)
	if err != nil {
		return err
	}
	values, err := service.RecoverStartup(ctx)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(a.out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(values)
}

func (a *App) newStandardCodeDockerService(dockerEnabled, permissionControl,
	workspaceSandbox bool,
) (*application.StandardCodeDockerService, error) {
	imageDigest := strings.TrimSpace(os.Getenv(standardCodeDockerImageEnvironment))
	if !sandbox.ValidOCIImageDigest(imageDigest) {
		return nil, fmt.Errorf("%s must name one pre-existing exact OCI sha256 image digest",
			standardCodeDockerImageEnvironment)
	}
	executor, err := repository.NewDrydockExecutor(filepath.Join(a.home, "drydocks"))
	if err != nil {
		return nil, err
	}
	drydocks, err := application.NewDrydockService(a.store, executor)
	if err != nil {
		return nil, err
	}
	manifests := application.NewSandboxManifestService(a.store, a.checker)
	if a.dockerObserver != nil {
		manifests.WithDockerProductionObserver(a.dockerObserver)
	}
	readiness := a.dockerReadinessProbe
	if readiness == nil {
		local, probeErr := sandbox.NewLocalDockerReadinessProbe()
		if probeErr != nil {
			return nil, probeErr
		}
		readiness = local
	}
	lifecycle := a.dockerLifecycle
	if lifecycle == nil {
		lifecycle = sandbox.NewLocalDockerContainerLifecycleTransport()
	}
	ioTransport := a.dockerIO
	if ioTransport == nil {
		ioTransport = sandbox.NewLocalDockerContainerIOTransport()
	}
	stagingRoot := filepath.Join(a.home, dockerSandboxStagingDirectory)
	if err := os.MkdirAll(stagingRoot, 0o700); err != nil {
		return nil, err
	}
	permissionCapabilities := domain.ExecutionPermissionRuntimeCapabilities{
		WorkspaceSandboxEnabled: workspaceSandbox,
		OperatorApprovalEnabled: permissionControl}
	docker, err := application.NewDockerSandboxService(a.store, readiness, a.checker,
		sandbox.DockerRuntimeCapabilities{Enabled: dockerEnabled}, permissionCapabilities,
		application.WithDockerSandboxExecution(lifecycle, ioTransport, stagingRoot,
			sandbox.DefaultDockerContainerLifecycleLeaseTTL),
		application.WithDockerStandardCode(drydocks, imageDigest))
	if err != nil {
		return nil, err
	}
	return application.NewStandardCodeDockerService(a.store, drydocks, manifests,
		docker, imageDigest)
}
