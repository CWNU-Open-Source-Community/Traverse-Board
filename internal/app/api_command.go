package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/httpapi"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/skills"
	"cyberagent-workbench/internal/webui"
)

const apiTokenEnvironment = "CYBERAGENT_API_TOKEN"

const apiControlTokenEnvironment = "CYBERAGENT_API_CONTROL_TOKEN"

func (a *App) apiCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("API subcommand is required")
	}
	switch args[0] {
	case "serve":
		return a.apiServeCommand(ctx, args[1:])
	case "openapi":
		return a.apiOpenAPICommand(args[1:])
	default:
		return fmt.Errorf("unknown API subcommand %q", args[0])
	}
}

func (a *App) apiServeCommand(ctx context.Context, args []string) error {
	fs := newFlagSet("api serve", a.errOut)
	listenAddress := fs.String("listen", httpapi.DefaultListenAddress, "loopback listen address")
	uiDirectory := fs.String("ui-dir", "", "optional built Web UI directory")
	fileEditProposals := fs.Bool("enable-file-edit-proposals", false,
		"enable Go-issued interactive FileEdit proposal sources")
	providerCredentials := fs.Bool("enable-provider-credentials", false,
		"enable OS-owned Provider credential changes")
	wakeWorker := fs.Bool("enable-wake-worker", false,
		"enable the bounded single-owner Run wake worker")
	permissionControl := fs.Bool("enable-permission-control", false,
		"enable operator-selected Run execution permissions")
	hostCommandProposals := fs.Bool("enable-host-command-proposals", false,
		"enable exact process or canonical PowerShell/Git Bash proposals with independent operator review")
	dangerFullAccess := fs.Bool("enable-danger-full-access", false,
		"enable danger-full-access permission selection")
	debugMaximumAccess := fs.Bool("enable-debug-maximum-access", false,
		"enable maximum Debug permission selection")
	browserCDPControl := fs.Bool("enable-browser-cdp-control", false,
		"enable operator-selected browser CDP permissions")
	fullCDPDebug := fs.Bool("enable-full-cdp-debug", false,
		"enable highly sensitive complete CDP debugging selection")
	dockerExecution := fs.Bool("enable-docker-execution", false,
		"enable the process-local Docker Sandbox execution capability")
	batchValidationExecution := fs.Bool("enable-batch-validation-execution", false,
		"enable fixed offline go/npm checks for confirmed batch deliveries")
	if err := fs.Parse(reorderFlags(args, map[string]bool{"listen": true, "ui-dir": true,
		"enable-file-edit-proposals": false, "enable-provider-credentials": false,
		"enable-wake-worker": false, "enable-permission-control": false,
		"enable-host-command-proposals": false,
		"enable-danger-full-access":     false, "enable-debug-maximum-access": false,
		"enable-browser-cdp-control": false, "enable-full-cdp-debug": false,
		"enable-docker-execution": false, "enable-batch-validation-execution": false})); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: cyberagent api serve [--listen <loopback-host:port>] [--ui-dir <built-web-directory>] [explicit capability flags]")
	}

	accessToken := os.Getenv(apiTokenEnvironment)
	controlToken := os.Getenv(apiControlTokenEnvironment)
	permissionCapabilities := domain.ExecutionPermissionRuntimeCapabilities{
		OperatorApprovalEnabled:   *permissionControl,
		DangerFullAccessEnabled:   *dangerFullAccess,
		DebugMaximumAccessEnabled: *debugMaximumAccess,
	}
	if err := permissionCapabilities.Validate(); err != nil {
		return apperror.Wrap(apperror.CodeInvalidArgument,
			err.Error(), err)
	}
	browserCDPCapabilities := domain.BrowserCDPPermissionRuntimeCapabilities{
		ControlEnabled: *browserCDPControl, FullDebugEnabled: *fullCDPDebug,
	}
	if err := browserCDPCapabilities.Validate(); err != nil {
		return apperror.Wrap(apperror.CodeInvalidArgument, err.Error(), err)
	}
	if browserCDPCapabilities.FullDebugEnabled &&
		!permissionCapabilities.DebugMaximumAccessEnabled {
		return apperror.New(apperror.CodeInvalidArgument,
			"full CDP debug requires maximum Debug execution capability")
	}
	if *hostCommandProposals && !permissionCapabilities.OperatorApprovalEnabled {
		return apperror.New(apperror.CodeInvalidArgument,
			"host command proposals require --enable-permission-control")
	}
	if *dockerExecution && !permissionCapabilities.OperatorApprovalEnabled {
		return apperror.New(apperror.CodeInvalidArgument,
			"--enable-docker-execution requires --enable-permission-control")
	}
	if *batchValidationExecution &&
		(!permissionCapabilities.OperatorApprovalEnabled ||
			!permissionCapabilities.DangerFullAccessEnabled) {
		return apperror.New(apperror.CodeInvalidArgument,
			"--enable-batch-validation-execution requires permission control and danger-full-access")
	}
	if (*fileEditProposals || *providerCredentials || *wakeWorker ||
		*permissionControl || *hostCommandProposals || *browserCDPControl ||
		*dockerExecution || *batchValidationExecution) && controlToken == "" {
		return apperror.New(apperror.CodeInvalidArgument,
			"interactive proposals, Provider credentials, the wake worker, and execution permission control require CYBERAGENT_API_CONTROL_TOKEN")
	}
	generated := accessToken == ""
	if generated {
		var err error
		accessToken, err = httpapi.GenerateAccessToken()
		if err != nil {
			return err
		}
	}
	var uiBundle *webui.Bundle
	if strings.TrimSpace(*uiDirectory) != "" {
		var err error
		uiBundle, err = webui.LoadDirectory(*uiDirectory)
		if err != nil {
			return apperror.Wrap(apperror.CodeInvalidArgument, "invalid Web UI directory", err)
		}
	}
	if err := a.ensureStore(); err != nil {
		return err
	}
	workspaceCheckpoints, err := application.NewWorkspaceCheckpointService(a.store,
		permissionCapabilities)
	if err != nil {
		return err
	}
	if _, err := workspaceCheckpoints.Reconcile(ctx); err != nil {
		return apperror.Wrap(apperror.CodeUnavailable,
			"Workspace checkpoint startup reconciliation failed", err)
	}
	batchDelivery := application.NewBatchDeliveryService(a.store).
		WithHostValidationExecution(*batchValidationExecution, permissionCapabilities)
	if _, err := batchDelivery.ReconcileStartup(ctx, 256); err != nil {
		return apperror.Wrap(apperror.CodeUnavailable,
			"batch delivery startup reconciliation failed", err)
	}
	dockerSandbox, err := a.newDockerSandboxService(*dockerExecution,
		permissionCapabilities)
	if err != nil {
		return err
	}
	if _, err := dockerSandbox.RecoverStartup(ctx); err != nil {
		return apperror.Wrap(apperror.CodeUnavailable,
			"Docker Sandbox startup recovery failed", err)
	}
	lifecycleControl := application.NewRunLifecycleControlService(a.store)
	executionControl := application.NewRunExecutionHandoffService(a.store, a.router,
		a.checker).WithActiveCalls(a.calls)
	commandManager, err := runner.NewPlatformCommandRuntimeManager(a.store,
		idgen.New("command-runtime-owner"))
	if err != nil {
		return err
	}
	if _, err := commandManager.ReconcileStartup(ctx); err != nil {
		return apperror.Wrap(apperror.CodeUnavailable,
			"command runtime startup reconciliation failed", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
		_ = commandManager.Shutdown(shutdownCtx)
		cancel()
	}()
	var commandRuntime *application.CommandRuntimeService
	if controlToken != "" && permissionCapabilities.Allows(
		domain.RunExecutionPermissionFullAccess) {
		commandRuntime, err = application.NewCommandRuntimeService(a.store,
			commandManager, permissionCapabilities)
		if err != nil {
			return err
		}
		executionControl.WithCommandRuntime(commandRuntime)
	}
	if executor := a.newDockerSandboxProposalExecutor(); executor != nil {
		executionControl.WithDockerSandboxProposalExecutor(executor)
	}
	planDeliveryControl := application.NewPlanDeliveryControlService(a.store)
	approvalControl := application.NewApprovalControlService(a.store,
		a.newToolGateway(), a.checker)
	modelControl := application.NewModelControlService(a.models, a.store)
	providerCredentialControl := application.NewProviderCredentialService(a.credentials).
		WithRegistryReload(a.models, a.store)
	fileEditReview := application.NewFileEditReviewService(a.store)
	fileEditProposal := application.NewFileEditProposalService(a.store, a.checker)
	fileEditApply := application.NewFileEditApplyService(a.store, a.checker,
		workspaceCheckpoints)
	runWakeControl := application.NewRunWakeControlService(a.store)
	runWakeExecution := application.NewForegroundRunWakeConsumer(a.store,
		executionControl)
	var worker *application.RunWakeWorker
	if *wakeWorker {
		createdWorker, workerErr := application.NewRunWakeWorker(
			application.NewRunWakeCoordinator(a.store), runWakeExecution,
			application.RunWakeWorkerConfig{OnError: func(runErr error) {
				fmt.Fprintln(a.errOut, "wake-worker:", runErr)
			}})
		if workerErr != nil {
			return workerErr
		}
		worker = createdWorker
	}
	var workerHealth httpapi.RunWakeWorkerHealthSource
	if worker != nil {
		workerHealth = worker
	}
	builtinSkills, err := skills.BuiltinRegistry()
	if err != nil {
		return err
	}
	skillObjects, err := skills.NewLocalPackageObjectStore(a.home)
	if err != nil {
		return err
	}
	skillInstallation := application.NewSkillPackageRegistryService(a.store,
		skillObjects, builtinSkills)
	controlledCommandExecutor, err := a.controlledCommandExecutor()
	if err != nil {
		return err
	}
	controlledCommandProposals :=
		application.NewControlledCommandProposalReviewService(
			a.store, controlledCommandExecutor, permissionCapabilities)
	hostCommandExecutor, err := a.hostCommandExecutor()
	if err != nil {
		return err
	}
	hostCommandProposalControl :=
		application.NewHostCommandProposalReviewService(
			a.store, hostCommandExecutor, permissionCapabilities)
	embeddedAnalyzerExecution := application.NewEmbeddedAnalyzerExecutionService(a.store)
	api, err := httpapi.New(a.store, httpapi.Config{
		AccessToken: accessToken, ControlToken: controlToken,
		RunControlEnabled: controlToken != "", RunCreationEnabled: controlToken != "",
		SessionMessageEnabled:                   controlToken != "",
		SessionSteeringControlEnabled:           controlToken != "",
		RunLifecycleEnabled:                     controlToken != "",
		RunExecutionEnabled:                     controlToken != "",
		PlanDeliveryControlEnabled:              controlToken != "",
		ApprovalControlEnabled:                  controlToken != "",
		ControlledCommandProposalControlEnabled: controlToken != "",
		HostCommandProposalControlEnabled:       *hostCommandProposals,
		ModelControlEnabled:                     controlToken != "",
		ProviderCredentialEnabled:               *providerCredentials,
		FileEditReviewEnabled:                   controlToken != "",
		FileEditProposalEnabled:                 *fileEditProposals,
		RunWakeControlEnabled:                   controlToken != "",
		FileEditApplyEnabled:                    controlToken != "",
		RunWakeExecutionEnabled:                 controlToken != "",
		RunWakeWorkerEnabled:                    *wakeWorker,
		ExecutionPermissionControlEnabled:       *permissionControl,
		ExecutionPermissionCapabilities:         permissionCapabilities,
		BrowserCDPPermissionControlEnabled:      *browserCDPControl,
		BrowserCDPPermissionCapabilities:        browserCDPCapabilities,
		SkillInstallationEnabled:                controlToken != "",
		EvidenceAttachmentEnabled:               controlToken != "",
		VerificationEvidenceEnabled:             controlToken != "",
		EmbeddedAnalyzerExecutionEnabled:        controlToken != "",
		WorkspaceCheckpointControlEnabled:       controlToken != "",
		BatchDeliveryControlEnabled:             controlToken != "",
		BatchDeliveryHostValidationEnabled:      *batchValidationExecution,
		RunLifecycleController:                  lifecycleControl,
		RunExecutionController:                  executionControl,
		PublicModelStreamSource:                 executionControl,
		PlanDeliveryController:                  planDeliveryControl,
		ApprovalController:                      approvalControl,
		ControlledCommandProposalController:     controlledCommandProposals,
		HostCommandProposalController:           hostCommandProposalControl,
		ModelControlController:                  modelControl,
		PriceSnapshotController:                 a.store,
		FanoutExecutionController: application.NewReadOnlyFanoutExecutionService(
			a.store, a.router, a.checker),
		ChildTaskControlController:          application.NewChildTaskControlService(a.store),
		ProviderCredentialController:        providerCredentialControl,
		FileEditReviewController:            fileEditReview,
		FileEditProposalController:          fileEditProposal,
		RunWakeController:                   runWakeControl,
		FileEditApplyController:             fileEditApply,
		RunWakeExecutionController:          runWakeExecution,
		RunWakeWorkerHealthSource:           workerHealth,
		SkillInstallationController:         skillInstallation,
		EmbeddedAnalyzerExecutionController: embeddedAnalyzerExecution,
		WorkspaceCheckpointController:       workspaceCheckpoints,
		BatchDeliveryController:             batchDelivery,
		DockerSandboxController:             dockerSandbox,
		ModelRegistry:                       a.models,
		AppVersion:                          Version,
		UIHandler:                           uiBundle,
	})
	if err != nil {
		return err
	}
	listener, err := httpapi.ListenLoopback(ctx, *listenAddress)
	if err != nil {
		return err
	}
	server, err := httpapi.NewServer(api, log.New(a.errOut, "api: ", log.LstdFlags))
	if err != nil {
		_ = listener.Close()
		return err
	}
	reconcileCtx, reconcileCancel := context.WithCancel(ctx)
	defer reconcileCancel()
	if commandRuntime != nil {
		go func() {
			if reconcileErr := commandRuntime.RunReconciler(reconcileCtx,
				500*time.Millisecond); reconcileErr != nil && reconcileCtx.Err() == nil {
				fmt.Fprintln(a.errOut, "command-runtime-reconciler:", reconcileErr)
			}
		}()
	} else {
		go runCommandRuntimeStartupReconciler(reconcileCtx, commandManager, a.errOut)
	}
	origin := "http://" + listener.Addr().String()
	baseURL := origin + "/api/v1"
	fmt.Fprintf(a.out, "api_url: %s\napi_version: %s\napi_token_generated: %t\napi_control_enabled: %t\n",
		baseURL, httpapi.Version, generated, controlToken != "")
	if uiBundle != nil {
		fmt.Fprintf(a.out, "ui_url: %s/\nui_source: %s\nui_assets: %d\nui_digest: %s\n",
			origin, uiBundle.Source(), uiBundle.AssetCount(), uiBundle.Digest())
	}
	if generated {
		fmt.Fprintf(a.out, "api_token: %s\n", accessToken)
	} else {
		fmt.Fprintf(a.out, "api_token_source: %s\n", apiTokenEnvironment)
	}
	if controlToken != "" {
		fmt.Fprintf(a.out, "api_control_token_source: %s\n", apiControlTokenEnvironment)
	}
	var workerCancel context.CancelFunc
	var workerDone chan struct{}
	if worker != nil {
		workerCtx, cancel := context.WithCancel(ctx)
		workerCancel = cancel
		workerDone = make(chan struct{})
		go func() {
			defer close(workerDone)
			_ = worker.Run(workerCtx)
		}()
	}
	if workerCancel != nil {
		defer func() {
			workerCancel()
			<-workerDone
		}()
	}
	fmt.Fprintf(a.out, "file_edit_proposals_enabled: %t\nprovider_credentials_enabled: %t\nwake_worker_enabled: %t\nwake_worker_concurrency: %d\nwake_worker_max_steps: %d\n",
		*fileEditProposals, *providerCredentials, *wakeWorker,
		application.RunWakeWorkerConcurrency, application.RunWakeWorkerMaxSteps)
	fmt.Fprintf(a.out, "execution_permission_control_enabled: %t\noperator_approval_enabled: %t\nhost_command_proposal_control_enabled: %t\ndanger_full_access_enabled: %t\ndebug_maximum_access_enabled: %t\ncommand_runtime_enabled: %t\n",
		*permissionControl, permissionCapabilities.OperatorApprovalEnabled,
		*hostCommandProposals,
		permissionCapabilities.DangerFullAccessEnabled,
		permissionCapabilities.DebugMaximumAccessEnabled, commandRuntime != nil)
	fmt.Fprintf(a.out, "browser_cdp_permission_control_enabled: %t\nfull_cdp_debug_enabled: %t\n",
		*browserCDPControl, browserCDPCapabilities.FullDebugEnabled)
	fmt.Fprintf(a.out, "docker_execution_enabled: %t\n", *dockerExecution)
	fmt.Fprintf(a.out, "batch_delivery_host_validation_enabled: %t\n",
		*batchValidationExecution)
	fmt.Fprintln(a.out, "note: the API is loopback-only; control is separately authorized and tokens are not persisted")
	return server.Serve(ctx, listener)
}

func runCommandRuntimeStartupReconciler(ctx context.Context,
	manager *runner.CommandRuntimeManager, errOut interface{ Write([]byte) (int, error) },
) {
	if manager == nil {
		return
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := manager.ReconcileStartup(ctx); err != nil && ctx.Err() == nil {
				fmt.Fprintln(errOut, "command-runtime-reconciler:", err)
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
				_ = manager.Shutdown(shutdownCtx)
				cancel()
				return
			}
		}
	}
}

func (a *App) apiOpenAPICommand(args []string) error {
	fs := newFlagSet("api openapi", a.errOut)
	output := fs.String("output", "", "optional output file")
	if err := fs.Parse(reorderFlags(args, map[string]bool{"output": true})); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: cyberagent api openapi [--output <path>]")
	}
	document, err := httpapi.GenerateOpenAPI()
	if err != nil {
		return err
	}
	if strings.TrimSpace(*output) == "" {
		_, err = a.out.Write(document)
		return err
	}
	path := filepath.Clean(strings.TrimSpace(*output))
	if err := os.WriteFile(path, document, 0o644); err != nil {
		return fmt.Errorf("write OpenAPI document: %w", err)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		absolute = path
	}
	fmt.Fprintf(a.out, "openapi_written: %s\n", absolute)
	return nil
}
