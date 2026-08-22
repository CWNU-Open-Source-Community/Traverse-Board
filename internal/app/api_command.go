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
	"cyberagent-workbench/internal/plugins"
	"cyberagent-workbench/internal/repository"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/scheduler"
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
	scheduledJobWorker := fs.Bool("enable-scheduled-job-worker", false,
		"enable the process-local single-concurrency scheduled job worker")
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
	codeIntelConfig := fs.String("code-intel-config", "",
		"absolute operator-reviewed code-intel config")
	gitAdvancedEnabled := fs.Bool("enable-git-advanced", false,
		"enable approval-gated git-advanced.v1 mutations")
	githubReviewEnabled := fs.Bool("enable-github-review", false,
		"enable the approval-gated GitHub Review Provider")
	gitWorktreeRoot := fs.String("git-worktree-root", "",
		"product-managed worktree root; defaults below CYBERAGENT_HOME")
	if err := fs.Parse(reorderFlags(args, map[string]bool{"listen": true, "ui-dir": true,
		"code-intel-config":          true,
		"enable-file-edit-proposals": false, "enable-provider-credentials": false,
		"enable-wake-worker": false, "enable-scheduled-job-worker": false,
		"enable-permission-control":     false,
		"enable-host-command-proposals": false,
		"enable-danger-full-access":     false, "enable-debug-maximum-access": false,
		"enable-browser-cdp-control": false, "enable-full-cdp-debug": false,
		"enable-docker-execution": false, "enable-batch-validation-execution": false,
		"enable-git-advanced": false, "enable-github-review": false,
		"git-worktree-root": true})); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: cyberagent api serve [--listen <loopback-host:port>] [--ui-dir <built-web-directory>] [explicit capability flags]")
	}
	if strings.TrimSpace(*codeIntelConfig) != "" {
		if a.codeIntelConfigLoaded || a.codeIntel != nil {
			return apperror.New(apperror.CodeFailedPrecondition,
				"code-intel config was already initialized")
		}
		absolute, err := filepath.Abs(strings.TrimSpace(*codeIntelConfig))
		if err != nil {
			return err
		}
		a.codeIntelConfigPath = filepath.Clean(absolute)
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
	if *gitAdvancedEnabled && !permissionCapabilities.OperatorApprovalEnabled {
		return apperror.New(apperror.CodeInvalidArgument,
			"--enable-git-advanced requires --enable-permission-control")
	}
	if *githubReviewEnabled && !permissionCapabilities.OperatorApprovalEnabled {
		return apperror.New(apperror.CodeInvalidArgument,
			"--enable-github-review requires --enable-permission-control")
	}
	if strings.TrimSpace(*gitWorktreeRoot) != "" &&
		!*gitAdvancedEnabled && !*githubReviewEnabled {
		return apperror.New(apperror.CodeInvalidArgument,
			"--git-worktree-root requires --enable-git-advanced or --enable-github-review")
	}
	if *batchValidationExecution &&
		(!permissionCapabilities.OperatorApprovalEnabled ||
			!permissionCapabilities.DangerFullAccessEnabled) {
		return apperror.New(apperror.CodeInvalidArgument,
			"--enable-batch-validation-execution requires permission control and danger-full-access")
	}
	if (*fileEditProposals || *providerCredentials || *wakeWorker || *scheduledJobWorker ||
		*permissionControl || *hostCommandProposals || *browserCDPControl ||
		*dockerExecution || *batchValidationExecution || *gitAdvancedEnabled ||
		*githubReviewEnabled) && controlToken == "" {
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
	mcpClient := a.newMCPClientManager()
	if mcpClient == nil {
		return apperror.New(apperror.CodeInternal,
			"initialize MCP Client control service")
	}
	if _, err := mcpClient.ReconcileStartup(ctx); err != nil {
		return apperror.Wrap(apperror.CodeUnavailable,
			"MCP Client startup reconciliation failed", err)
	}
	pluginService, err := plugins.NewService(a.store)
	if err != nil {
		return err
	}
	extensionControl, err := application.NewExtensionControlService(a.store,
		mcpClient, pluginService)
	if err != nil {
		return err
	}
	hookEngine := a.newLifecycleHookEngine()
	workspaceCheckpoints, err := application.NewWorkspaceCheckpointService(a.store,
		permissionCapabilities)
	if err != nil {
		return err
	}
	workspaceCheckpoints.WithLifecycleHooks(hookEngine)
	if _, err := workspaceCheckpoints.Reconcile(ctx); err != nil {
		return apperror.Wrap(apperror.CodeUnavailable,
			"Workspace checkpoint startup reconciliation failed", err)
	}
	var gitAdvancedService *application.GitAdvancedService
	if *gitAdvancedEnabled {
		managedRoot := strings.TrimSpace(*gitWorktreeRoot)
		if managedRoot == "" {
			managedRoot = filepath.Join(a.home, "worktrees")
		}
		gitExecutor, executorErr := repository.NewAdvancedExecutor(managedRoot, true)
		if executorErr != nil {
			return executorErr
		}
		gitAdvancedService, err = application.NewGitAdvancedService(a.store,
			gitExecutor, permissionCapabilities, workspaceCheckpoints)
		if err != nil {
			return err
		}
		if _, err := gitAdvancedService.ReconcileStartup(ctx, 500); err != nil {
			return apperror.Wrap(apperror.CodeUnavailable,
				"Git advanced startup reconciliation failed", err)
		}
	}
	var githubReviewService *application.GitHubReviewService
	if *githubReviewEnabled {
		managedRoot := strings.TrimSpace(*gitWorktreeRoot)
		if managedRoot == "" {
			managedRoot = filepath.Join(a.home, "worktrees")
		}
		gitExecutor, executorErr := repository.NewAdvancedExecutor(managedRoot, true)
		if executorErr != nil {
			return executorErr
		}
		githubReviewService, err = application.NewGitHubReviewService(a.store,
			a.credentials, gitExecutor, permissionCapabilities)
		if err != nil {
			return err
		}
		if _, err := githubReviewService.ReconcileStartup(ctx, 500); err != nil {
			return apperror.Wrap(apperror.CodeUnavailable,
				"GitHub review startup reconciliation failed", err)
		}
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
	lifecycleControl := application.NewRunLifecycleControlService(a.store).
		WithLifecycleHooks(hookEngine)
	executionControl := application.NewRunExecutionHandoffService(a.store, a.router,
		a.checker).WithActiveCalls(a.calls).WithMCPClient(mcpClient).
		WithLifecycleHooks(hookEngine)
	if a.codeIntel != nil {
		executionControl.WithCodeIntel(a.codeIntel)
	}
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
	scheduledJobs := application.NewScheduledJobService(a.store)
	var scheduleWorker *scheduler.Worker
	if *scheduledJobWorker {
		scheduleWorker, err = scheduler.NewWorker(scheduledJobs, scheduler.WorkerConfig{
			OnError: func(runErr error) {
				fmt.Fprintln(a.errOut, "scheduled-job-worker:", runErr)
			},
		})
		if err != nil {
			return err
		}
	}
	var scheduleWorkerHealth httpapi.ScheduledJobWorkerHealthSource
	if scheduleWorker != nil {
		scheduleWorkerHealth = scheduleWorker
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
		ScheduledJobControlEnabled:              controlToken != "",
		ScheduledJobWorkerEnabled:               *scheduledJobWorker,
		ExecutionPermissionControlEnabled:       *permissionControl,
		ExecutionPermissionCapabilities:         permissionCapabilities,
		BrowserCDPPermissionControlEnabled:      *browserCDPControl,
		BrowserCDPPermissionCapabilities:        browserCDPCapabilities,
		SkillInstallationEnabled:                controlToken != "",
		EvidenceAttachmentEnabled:               controlToken != "",
		VerificationEvidenceEnabled:             controlToken != "",
		EmbeddedAnalyzerExecutionEnabled:        controlToken != "",
		WorkspaceCheckpointControlEnabled:       controlToken != "",
		GitAdvancedControlEnabled:               *gitAdvancedEnabled,
		GitHubReviewControlEnabled:              *githubReviewEnabled,
		BatchDeliveryControlEnabled:             controlToken != "",
		BatchDeliveryHostValidationEnabled:      *batchValidationExecution,
		ExtensionControlEnabled:                 controlToken != "",
		LifecycleHooks:                          hookEngine,
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
		ScheduledJobController:              scheduledJobs,
		ScheduledJobWorkerHealthSource:      scheduleWorkerHealth,
		SkillInstallationController:         skillInstallation,
		EmbeddedAnalyzerExecutionController: embeddedAnalyzerExecution,
		WorkspaceCheckpointController:       workspaceCheckpoints,
		GitAdvancedController:               gitAdvancedService,
		GitHubReviewController:              githubReviewService,
		BatchDeliveryController:             batchDelivery,
		ExtensionController:                 extensionControl,
		CodeIntelSource:                     a.codeIntel,
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
	var scheduleWorkerCancel context.CancelFunc
	var scheduleWorkerDone chan struct{}
	if scheduleWorker != nil {
		workerCtx, cancel := context.WithCancel(ctx)
		scheduleWorkerCancel = cancel
		scheduleWorkerDone = make(chan struct{})
		go func() {
			defer close(scheduleWorkerDone)
			_ = scheduleWorker.Run(workerCtx)
		}()
	}
	if scheduleWorkerCancel != nil {
		defer func() {
			scheduleWorkerCancel()
			<-scheduleWorkerDone
		}()
	}
	fmt.Fprintf(a.out, "file_edit_proposals_enabled: %t\nprovider_credentials_enabled: %t\nwake_worker_enabled: %t\nwake_worker_concurrency: %d\nwake_worker_max_steps: %d\n",
		*fileEditProposals, *providerCredentials, *wakeWorker,
		application.RunWakeWorkerConcurrency, application.RunWakeWorkerMaxSteps)
	fmt.Fprintf(a.out, "scheduled_job_control_enabled: %t\nscheduled_job_worker_enabled: %t\nscheduled_job_worker_concurrency: %d\nscheduled_job_persistent_service: false\n",
		controlToken != "", *scheduledJobWorker, scheduler.WorkerConcurrency)
	fmt.Fprintf(a.out, "execution_permission_control_enabled: %t\nworkspace_sandbox_enabled: %t\noperator_approval_enabled: %t\nhost_command_proposal_control_enabled: %t\ndanger_full_access_enabled: %t\ndebug_maximum_access_enabled: %t\ncommand_runtime_enabled: %t\n",
		*permissionControl, permissionCapabilities.WorkspaceSandboxEnabled,
		permissionCapabilities.OperatorApprovalEnabled,
		*hostCommandProposals,
		permissionCapabilities.DangerFullAccessEnabled,
		permissionCapabilities.DebugMaximumAccessEnabled, commandRuntime != nil)
	fmt.Fprintf(a.out, "browser_cdp_permission_control_enabled: %t\nfull_cdp_debug_enabled: %t\n",
		*browserCDPControl, browserCDPCapabilities.FullDebugEnabled)
	fmt.Fprintf(a.out, "docker_execution_enabled: %t\n", *dockerExecution)
	fmt.Fprintf(a.out, "batch_delivery_host_validation_enabled: %t\n",
		*batchValidationExecution)
	fmt.Fprintf(a.out, "code_intel_enabled: %t\n", a.codeIntel != nil)
	fmt.Fprintf(a.out, "git_advanced_control_enabled: %t\n", *gitAdvancedEnabled)
	fmt.Fprintf(a.out, "github_review_control_enabled: %t\n", *githubReviewEnabled)
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
