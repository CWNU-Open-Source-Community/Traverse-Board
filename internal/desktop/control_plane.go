package desktop

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/browserruntime"
	"cyberagent-workbench/internal/codeintel"
	"cyberagent-workbench/internal/commandruntimeadapter"
	"cyberagent-workbench/internal/credential"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/executionauth"
	"cyberagent-workbench/internal/hooks"
	"cyberagent-workbench/internal/httpapi"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/mcp"
	"cyberagent-workbench/internal/modelregistry"
	"cyberagent-workbench/internal/plugins"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/repository"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/sandbox"
	"cyberagent-workbench/internal/scheduler"
	"cyberagent-workbench/internal/skills"
	"cyberagent-workbench/internal/store"
	terminalruntime "cyberagent-workbench/internal/terminal"
	"cyberagent-workbench/internal/toolgateway"
	"cyberagent-workbench/internal/webevidence"
	"cyberagent-workbench/internal/workspace"
)

// ControlPlane owns the Desktop process' SQLite connection and in-process API.
// It does not listen on a socket and it adds no renderer authority beyond the
// tokens explicitly supplied in ControlPlaneConfig.
type ControlPlane struct {
	stateStore                     *store.SQLiteStore
	workspaceManager               *workspace.Manager
	workspaceImportMu              sync.Mutex
	handler                        http.Handler
	closeOnce                      sync.Once
	closeErr                       error
	skillInstaller                 *application.SkillPackageRegistryService
	dockerSandbox                  *application.DockerSandboxService
	userTerminal                   *desktopUserTerminalService
	debugAgentInput                application.DebugTerminalAgentInputController
	commandRuntime                 application.CommandRuntimeRuntime
	standardCodeDrydocks           *application.DrydockService
	standardCodePreset             *application.StandardCodePresetService
	policyChecker                  policy.Checker
	uiEvidence                     *application.UIEvidenceService
	fullCDPSessions                *application.FullCDPProductionService
	commandRuntimeManager          *runner.CommandRuntimeManager
	commandRuntimeManagers         []*runner.CommandRuntimeManager
	commandRuntimeAdapterInstalled bool
	commandRuntimeAdapterReady     bool
	standardCodePresetEnabled      bool
	codeIntelManager               *codeintel.Manager
	terminalManager                *terminalruntime.Manager
	boundaryMonitor                *terminalruntime.HostBoundaryMonitor
	terminalWorkerMu               sync.Mutex
	terminalCancel                 context.CancelFunc
	terminalDone                   chan struct{}
	wakeWorker                     *application.RunWakeWorker
	scheduledJobWorker             *scheduler.Worker
	workerMu                       sync.Mutex
	workerCancel                   context.CancelFunc
	workerDone                     chan struct{}
	scheduledWorkerCancel          context.CancelFunc
	scheduledWorkerDone            chan struct{}
	webFetchReconcileCancel        context.CancelFunc
	webFetchReconcileDone          chan struct{}
	closed                         bool
}

type webFetchAuthorizationReconciler interface {
	ReconcileWebFetchAuthorizations(context.Context, string, int) (int, error)
}

func runWebFetchAuthorizationReconciler(ctx context.Context,
	reconciler webFetchAuthorizationReconciler, interval time.Duration,
) {
	if reconciler == nil {
		return
	}
	if interval <= 0 {
		interval = 2 * time.Second
	}
	for {
		_, _ = reconciler.ReconcileWebFetchAuthorizations(
			ctx, "", application.MaxApprovalQueueItems)
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func webFetchAuthorizationReconcilerEnabled(config ControlPlaneConfig) bool {
	return config.ControlToken != "" && config.RunExecutionEnabled &&
		config.ApprovalControlEnabled &&
		config.ExecutionPermissionCapabilities.OperatorApprovalEnabled
}

type ControlPlaneConfig struct {
	DatabasePath                            string
	HomePath                                string
	ReadToken                               string
	ControlToken                            string
	RunControlEnabled                       bool
	ExecutionPermissionControlEnabled       bool
	ExecutionPermissionCapabilities         domain.ExecutionPermissionRuntimeCapabilities
	LocalSandboxReadiness                   *sandbox.LocalReadiness
	LocalSandboxBackend                     sandbox.LocalBackend
	StandardCodeDockerImageDigest           string
	WebSearchEndpoint                       string
	BrowserCDPPermissionControlEnabled      bool
	BrowserCDPPermissionCapabilities        domain.BrowserCDPPermissionRuntimeCapabilities
	RunCreationEnabled                      bool
	SessionMessageEnabled                   bool
	SessionSteeringControlEnabled           bool
	RunLifecycleEnabled                     bool
	RunExecutionEnabled                     bool
	PlanDeliveryControlEnabled              bool
	ApprovalControlEnabled                  bool
	ControlledCommandProposalControlEnabled bool
	HostCommandProposalControlEnabled       bool
	ModelControlEnabled                     bool
	ProviderCredentialEnabled               bool
	FileEditReviewEnabled                   bool
	FileEditProposalEnabled                 bool
	RunWakeControlEnabled                   bool
	FileEditApplyEnabled                    bool
	RunWakeExecutionEnabled                 bool
	RunWakeWorkerEnabled                    bool
	ScheduledJobControlEnabled              bool
	ScheduledJobWorkerEnabled               bool
	SkillInstallationEnabled                bool
	EvidenceAttachmentEnabled               bool
	VerificationEvidenceEnabled             bool
	EmbeddedAnalyzerExecutionEnabled        bool
	BatchDeliveryControlEnabled             bool
	BatchDeliveryHostValidationEnabled      bool
	UIEvidenceControlEnabled                bool
	BrowserRuntimeCapabilities              browserruntime.ProductionRuntimeCapabilities
	FullCDPSessionControlEnabled            bool
	FullCDPRuntimeCapabilities              browserruntime.FullCDPRuntimeCapabilities
	UserTerminalEnabled                     bool
	DockerExecutionEnabled                  bool
	CodeIntelConfigPath                     string
	GitAdvancedControlEnabled               bool
	GitHubReviewControlEnabled              bool
	GitManagedWorktreeRoot                  string
	AppVersion                              string
	UIHandler                               http.Handler
	CredentialStore                         credential.Store
	OnWakeWorkerError                       func(error)
	OnScheduledJobWorkerError               func(error)
}

func OpenControlPlane(config ControlPlaneConfig) (*ControlPlane, error) {
	if strings.TrimSpace(config.DatabasePath) == "" {
		return nil, apperror.New(apperror.CodeInvalidArgument,
			"desktop database path is required")
	}
	if config.LocalSandboxReadiness != nil {
		if err := config.LocalSandboxReadiness.Validate(); err != nil ||
			config.LocalSandboxReadiness.Ready !=
				config.ExecutionPermissionCapabilities.WorkspaceSandboxEnabled {
			return nil, apperror.New(apperror.CodeInvalidArgument,
				"desktop Local Sandbox readiness does not match its startup gate")
		}
	} else if config.ExecutionPermissionCapabilities.WorkspaceSandboxEnabled {
		return nil, apperror.New(apperror.CodeInvalidArgument,
			"desktop Workspace Sandbox startup gate requires validated readiness")
	}
	if config.LocalSandboxBackend != nil &&
		(config.LocalSandboxReadiness == nil ||
			config.LocalSandboxBackend.Generation() !=
				config.LocalSandboxReadiness.RuntimeGeneration) {
		return nil, apperror.New(apperror.CodeInvalidArgument,
			"desktop Local Sandbox backend does not match its readiness proof")
	}
	if config.DockerExecutionEnabled &&
		(!config.ExecutionPermissionControlEnabled ||
			!config.ExecutionPermissionCapabilities.OperatorApprovalEnabled) {
		return nil, apperror.New(apperror.CodeInvalidArgument,
			"desktop Docker execution requires operator approval permission control")
	}
	if config.GitAdvancedControlEnabled &&
		(!config.ExecutionPermissionControlEnabled ||
			!config.ExecutionPermissionCapabilities.OperatorApprovalEnabled ||
			config.ControlToken == "") {
		return nil, apperror.New(apperror.CodeInvalidArgument,
			"desktop Git advanced control requires operator approval permission control")
	}
	if config.GitHubReviewControlEnabled &&
		(!config.ExecutionPermissionControlEnabled ||
			!config.ExecutionPermissionCapabilities.OperatorApprovalEnabled ||
			config.ControlToken == "") {
		return nil, apperror.New(apperror.CodeInvalidArgument,
			"desktop GitHub review control requires operator approval permission control")
	}
	if strings.TrimSpace(config.GitManagedWorktreeRoot) != "" &&
		!config.GitAdvancedControlEnabled && !config.GitHubReviewControlEnabled {
		return nil, apperror.New(apperror.CodeInvalidArgument,
			"desktop managed Git worktree root requires Git advanced or GitHub review control")
	}
	if config.BatchDeliveryHostValidationEnabled &&
		(!config.ExecutionPermissionControlEnabled ||
			!config.ExecutionPermissionCapabilities.OperatorApprovalEnabled ||
			!config.ExecutionPermissionCapabilities.DangerFullAccessEnabled ||
			!config.BatchDeliveryControlEnabled || config.ControlToken == "") {
		return nil, apperror.New(apperror.CodeInvalidArgument,
			"desktop batch validation requires control, permission control, and danger-full-access")
	}
	if config.UIEvidenceControlEnabled {
		capabilities := config.BrowserRuntimeCapabilities
		if !config.RunExecutionEnabled ||
			!config.ExecutionPermissionCapabilities.Allows(
				domain.RunExecutionPermissionFullAccess) ||
			!config.BrowserCDPPermissionControlEnabled ||
			!config.BrowserCDPPermissionCapabilities.ControlEnabled ||
			capabilities.Validate() != nil || !capabilities.SafeWebStartEnabled ||
			!capabilities.DisposableProfileEnabled ||
			!capabilities.NetworkContainmentEnabled || !capabilities.RestrictedCDPEnabled {
			return nil, apperror.New(apperror.CodeInvalidArgument,
				"desktop UI evidence requires full-access command runtime and restricted Safe Web")
		}
	}
	if config.FullCDPSessionControlEnabled {
		capabilities := config.FullCDPRuntimeCapabilities
		if config.ControlToken == "" ||
			!config.ExecutionPermissionControlEnabled ||
			!config.BrowserCDPPermissionControlEnabled ||
			!config.BrowserCDPPermissionCapabilities.ControlEnabled ||
			!config.BrowserCDPPermissionCapabilities.FullDebugEnabled ||
			!config.ExecutionPermissionCapabilities.DangerFullAccessEnabled ||
			config.ExecutionPermissionCapabilities.RuntimeAuthority == nil ||
			capabilities.Validate() != nil || !capabilities.StartEnabled ||
			!capabilities.DisposableProfileEnabled || !capabilities.TransportEnabled {
			return nil, apperror.New(apperror.CodeInvalidArgument,
				"desktop Full CDP sessions require explicit control, execution authority, and all dedicated runtime gates")
		}
	}
	webClient := webevidence.NewSafeHTTPClient()
	providerSearchClient := webevidence.NewProviderSearchHTTPClient()
	var webSearchProvider webevidence.SearchProvider
	if endpoint := strings.TrimSpace(config.WebSearchEndpoint); endpoint != "" {
		configured, providerErr := webevidence.NewSearXNGProvider(webClient, endpoint)
		if providerErr != nil {
			return nil, apperror.Wrap(apperror.CodeInvalidArgument,
				"desktop Web search endpoint is invalid", providerErr)
		}
		webSearchProvider = configured
	}
	// Consume caller-provided startup evidence before opening SQLite or
	// constructing services. Schema migration and cold dependency startup must
	// not make a proof that was current at this boundary expire mid-construction.
	capabilityReadinessRuntime := application.CapabilityReadinessRuntime{
		RunControlEnabled:   config.ControlToken != "" && config.RunControlEnabled,
		RunExecutionEnabled: config.ControlToken != "" && config.RunExecutionEnabled,
		ExecutionPermissionControlEnabled: config.ControlToken != "" &&
			config.ExecutionPermissionControlEnabled,
		BrowserCDPPermissionControlEnabled: config.ControlToken != "" &&
			config.BrowserCDPPermissionControlEnabled,
		ExecutionPermissionCapabilities:  config.ExecutionPermissionCapabilities,
		BrowserCDPPermissionCapabilities: config.BrowserCDPPermissionCapabilities,
		LocalSandboxInstalled: config.ExecutionPermissionCapabilities.
			WorkspaceSandboxEnabled,
		DockerStartupGateEnabled: config.DockerExecutionEnabled,
		DockerAvailable:          config.DockerExecutionEnabled,
	}
	if config.LocalSandboxReadiness != nil {
		projected, projectionErr := capabilityReadinessRuntime.
			WithLocalSandboxReadiness(*config.LocalSandboxReadiness)
		if projectionErr != nil || projected.ExecutionPermissionCapabilities !=
			config.ExecutionPermissionCapabilities {
			return nil, apperror.New(apperror.CodeInvalidArgument,
				"desktop Local Sandbox readiness projection is invalid")
		}
		capabilityReadinessRuntime = projected
	}
	stateStore, err := store.Open(config.DatabasePath)
	if err != nil {
		return nil, err
	}
	home := strings.TrimSpace(config.HomePath)
	if home == "" {
		home = filepath.Dir(config.DatabasePath)
	}
	workspaceManager := workspace.NewManager(home, stateStore)
	registeredWorkspaces, err := stateStore.ListWorkspaces(context.Background())
	if err != nil {
		_ = stateStore.Close()
		return nil, err
	}
	var codeIntelManager *codeintel.Manager
	codeIntelTransferred := false
	defer func() {
		if codeIntelManager != nil && !codeIntelTransferred {
			shutdownCtx, cancel := context.WithTimeout(context.Background(),
				codeintel.MaximumShutdownGracePeriod)
			_ = codeIntelManager.Close(shutdownCtx)
			cancel()
		}
	}()
	if strings.TrimSpace(config.CodeIntelConfigPath) != "" {
		codeIntelManager, _, err = codeintel.NewManagerFromConfig(
			filepath.Clean(config.CodeIntelConfigPath))
		if err != nil {
			_ = stateStore.Close()
			return nil, apperror.Wrap(apperror.CodeFailedPrecondition,
				"load Desktop code-intel configuration", err)
		}
	}
	if len(registeredWorkspaces) == 0 {
		if _, err := workspaceManager.Ensure(
			context.Background(), "default"); err != nil {
			_ = stateStore.Close()
			return nil, err
		}
	}
	credentialStore := config.CredentialStore
	if credentialStore == nil {
		credentialStore = credential.NewSystemStore()
	}
	models := modelregistry.NewFromEnvironment()
	if credentialStore.Available() {
		models, err = modelregistry.NewFromEnvironmentWithCredentials(credentialStore)
		if err != nil {
			_ = stateStore.Close()
			return nil, err
		}
	}
	if err := models.LoadRouteSettings(context.Background(), stateStore); err != nil {
		_ = stateStore.Close()
		return nil, err
	}
	mcpClient, err := mcp.NewClientManager(stateStore, credentialStore, mcp.ManagerOptions{})
	if err != nil {
		_ = stateStore.Close()
		return nil, err
	}
	if _, err := mcpClient.ReconcileStartup(context.Background()); err != nil {
		_ = stateStore.Close()
		return nil, apperror.Wrap(apperror.CodeUnavailable,
			"desktop MCP Client startup reconciliation failed", err)
	}
	pluginService, err := plugins.NewService(stateStore)
	if err != nil {
		_ = stateStore.Close()
		return nil, err
	}
	hookEngine := hooks.NewEngine(stateStore).WithLoader(pluginService.ActiveHooks)
	extensionControl, err := application.NewExtensionControlService(stateStore,
		mcpClient, pluginService)
	if err != nil {
		_ = stateStore.Close()
		return nil, err
	}
	checker := policy.NewDefaultChecker()
	webEvidence := webevidence.NewService(stateStore, webSearchProvider,
		webevidence.NewFetcher(webClient))
	providerSearchResolver, err := application.NewProviderSearchResolver(models,
		stateStore, credentialStore, webSearchProvider, providerSearchClient)
	if err != nil {
		_ = stateStore.Close()
		return nil, apperror.Wrap(apperror.CodeFailedPrecondition,
			"desktop Provider search resolver is unavailable", err)
	}
	webEvidence.WithSearchProviderResolver(providerSearchResolver)
	workspaceCheckpoints, err := application.NewWorkspaceCheckpointService(stateStore,
		config.ExecutionPermissionCapabilities)
	if err != nil {
		_ = stateStore.Close()
		return nil, err
	}
	workspaceCheckpoints.WithLifecycleHooks(hookEngine)
	var commandRuntimeDrydocks *application.DrydockService
	if config.RunExecutionEnabled &&
		config.ExecutionPermissionCapabilities.WorkspaceSandboxEnabled {
		drydockExecutor, executorErr := repository.NewDrydockExecutor(
			filepath.Join(home, "drydocks"))
		if executorErr != nil {
			_ = stateStore.Close()
			return nil, executorErr
		}
		commandRuntimeDrydocks, err = application.NewDrydockService(stateStore,
			drydockExecutor)
		if err != nil {
			_ = stateStore.Close()
			return nil, err
		}
		commandRuntimeDrydocks.WithCheckpointService(workspaceCheckpoints)
	}
	if commandRuntimeDrydocks != nil {
		_, err = commandRuntimeDrydocks.ReconcileWorkspaceCheckpoints(context.Background())
	} else {
		_, err = workspaceCheckpoints.Reconcile(context.Background())
	}
	if err != nil {
		_ = stateStore.Close()
		return nil, apperror.Wrap(apperror.CodeUnavailable,
			"desktop Workspace checkpoint startup reconciliation failed", err)
	}
	var gitAdvanced *application.GitAdvancedService
	var githubReview *application.GitHubReviewService
	if config.GitAdvancedControlEnabled || config.GitHubReviewControlEnabled {
		managedRoot := strings.TrimSpace(config.GitManagedWorktreeRoot)
		if managedRoot == "" {
			managedRoot = filepath.Join(home, "worktrees")
		}
		gitExecutor, gitErr := repository.NewAdvancedExecutor(managedRoot, true)
		if gitErr != nil {
			_ = stateStore.Close()
			return nil, gitErr
		}
		if config.GitAdvancedControlEnabled {
			gitAdvanced, gitErr = application.NewGitAdvancedService(stateStore, gitExecutor,
				config.ExecutionPermissionCapabilities, workspaceCheckpoints)
			if gitErr != nil {
				_ = stateStore.Close()
				return nil, gitErr
			}
			if _, gitErr = gitAdvanced.ReconcileStartup(context.Background(), 500); gitErr != nil {
				_ = stateStore.Close()
				return nil, apperror.Wrap(apperror.CodeUnavailable,
					"desktop Git advanced startup reconciliation failed", gitErr)
			}
		}
		if config.GitHubReviewControlEnabled {
			githubReview, gitErr = application.NewGitHubReviewService(stateStore,
				credentialStore, gitExecutor, config.ExecutionPermissionCapabilities)
			if gitErr != nil {
				_ = stateStore.Close()
				return nil, gitErr
			}
			if _, gitErr = githubReview.ReconcileStartup(context.Background(), 500); gitErr != nil {
				_ = stateStore.Close()
				return nil, apperror.Wrap(apperror.CodeUnavailable,
					"desktop GitHub review startup reconciliation failed", gitErr)
			}
		}
	}
	batchDelivery := application.NewBatchDeliveryService(stateStore).
		WithHostValidationExecution(config.BatchDeliveryHostValidationEnabled,
			config.ExecutionPermissionCapabilities)
	if _, err := batchDelivery.ReconcileStartup(context.Background(), 256); err != nil {
		_ = stateStore.Close()
		return nil, apperror.Wrap(apperror.CodeUnavailable,
			"desktop batch delivery startup reconciliation failed", err)
	}
	var standardCodeDelivery *application.StandardCodeDeliveryService
	if commandRuntimeDrydocks != nil {
		standardCodeDelivery, err = application.NewStandardCodeDeliveryService(
			stateStore, commandRuntimeDrydocks)
		if err != nil {
			_ = stateStore.Close()
			return nil, err
		}
		if githubReview != nil {
			githubReview.WithStandardCodeDelivery(standardCodeDelivery)
		}
	}
	dockerSandbox, standardCodeRuntime, standardCodeDockerReadiness, err :=
		newDesktopDockerSandboxService(
			context.Background(),
			stateStore, home, config.DockerExecutionEnabled,
			config.ExecutionPermissionCapabilities, commandRuntimeDrydocks,
			strings.TrimSpace(config.StandardCodeDockerImageDigest))
	if err != nil {
		_ = stateStore.Close()
		return nil, apperror.Wrap(apperror.CodeFailedPrecondition,
			"desktop Docker Sandbox startup recovery failed", err)
	}
	lifecycleControl := application.NewRunLifecycleControlService(stateStore).
		WithLifecycleHooks(hookEngine)
	webFetchAuthorizationSchedulerEnabled :=
		webFetchAuthorizationReconcilerEnabled(config)
	executionControl := application.NewRunExecutionHandoffService(stateStore,
		models.Router(), checker).WithActiveCalls(
		application.NewActiveCallRegistry()).WithMCPClient(mcpClient).
		WithWebEvidence(webEvidence).
		WithWebFetchAuthorizationScheduler(webFetchAuthorizationSchedulerEnabled).
		WithExecutionPermissionCapabilities(config.ExecutionPermissionCapabilities).
		WithLifecycleHooks(hookEngine)
	if standardCodeDelivery != nil {
		executionControl.WithStandardCodeDelivery(standardCodeDelivery)
	}
	if codeIntelManager != nil {
		executionControl.WithCodeIntel(codeIntelManager)
	}
	commandManager, err := runner.NewPlatformCommandRuntimeManager(stateStore,
		idgen.New("command-runtime-owner"))
	if err != nil {
		_ = stateStore.Close()
		return nil, err
	}
	if _, err := commandManager.ReconcileStartup(context.Background()); err != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = commandManager.Shutdown(shutdownCtx)
		cancel()
		_ = stateStore.Close()
		return nil, apperror.Wrap(apperror.CodeUnavailable,
			"command runtime startup reconciliation failed", err)
	}
	commandManagers := []*runner.CommandRuntimeManager{commandManager}
	commandManagersTransferred := false
	defer func() {
		if !commandManagersTransferred {
			shutdownDesktopCommandRuntimeManagers(commandManagers, 2*time.Second)
		}
	}()
	commandAdapters := make([]*application.CommandRuntimeService, 0, 3)
	if config.RunExecutionEnabled && config.ExecutionPermissionCapabilities.Allows(
		domain.RunExecutionPermissionFullAccess) {
		hostRuntime, serviceErr := application.NewCommandRuntimeService(stateStore,
			commandManager, config.ExecutionPermissionCapabilities)
		if serviceErr != nil {
			shutdownDesktopCommandRuntimeManagers(commandManagers, 2*time.Second)
			_ = stateStore.Close()
			return nil, serviceErr
		}
		commandAdapters = append(commandAdapters, hostRuntime)
	}
	if config.RunExecutionEnabled && config.LocalSandboxBackend != nil &&
		config.LocalSandboxReadiness != nil && commandRuntimeDrydocks != nil {
		localExecutor, executorErr := application.NewLocalSandboxCommandRuntimeExecutor(
			stateStore, config.LocalSandboxBackend, *config.LocalSandboxReadiness)
		if executorErr != nil {
			shutdownDesktopCommandRuntimeManagers(commandManagers, 2*time.Second)
			_ = stateStore.Close()
			return nil, executorErr
		}
		localManager, managerErr := runner.NewSandboxCommandRuntimeManager(stateStore,
			localExecutor, idgen.New("command-runtime-local-owner"))
		if managerErr != nil {
			shutdownDesktopCommandRuntimeManagers(commandManagers, 2*time.Second)
			_ = stateStore.Close()
			return nil, managerErr
		}
		commandManagers = append(commandManagers, localManager)
		if _, managerErr = localManager.ReconcileStartup(context.Background()); managerErr != nil {
			shutdownDesktopCommandRuntimeManagers(commandManagers, 2*time.Second)
			_ = stateStore.Close()
			return nil, apperror.Wrap(apperror.CodeUnavailable,
				"Local Command Runtime startup reconciliation failed", managerErr)
		}
		localRuntime, serviceErr := application.NewSandboxedCommandRuntimeService(
			stateStore, localManager, localExecutor,
			config.ExecutionPermissionCapabilities, commandRuntimeDrydocks)
		if serviceErr != nil {
			shutdownDesktopCommandRuntimeManagers(commandManagers, 2*time.Second)
			_ = stateStore.Close()
			return nil, serviceErr
		}
		commandAdapters = append(commandAdapters, localRuntime)
	}
	if config.RunExecutionEnabled && standardCodeRuntime != nil &&
		commandRuntimeDrydocks != nil {
		dockerExecutor, executorErr :=
			application.NewDockerSandboxCommandRuntimeExecutor(standardCodeRuntime)
		if executorErr != nil {
			shutdownDesktopCommandRuntimeManagers(commandManagers, 2*time.Second)
			_ = stateStore.Close()
			return nil, executorErr
		}
		dockerManager, managerErr := runner.NewSandboxCommandRuntimeManager(stateStore,
			dockerExecutor, idgen.New("command-runtime-docker-owner"))
		if managerErr != nil {
			shutdownDesktopCommandRuntimeManagers(commandManagers, 2*time.Second)
			_ = stateStore.Close()
			return nil, managerErr
		}
		commandManagers = append(commandManagers, dockerManager)
		if _, managerErr = dockerManager.ReconcileStartup(context.Background()); managerErr != nil {
			shutdownDesktopCommandRuntimeManagers(commandManagers, 2*time.Second)
			_ = stateStore.Close()
			return nil, apperror.Wrap(apperror.CodeUnavailable,
				"Docker Command Runtime startup reconciliation failed", managerErr)
		}
		dockerRuntime, serviceErr := application.NewSandboxedCommandRuntimeService(
			stateStore, dockerManager, dockerExecutor,
			config.ExecutionPermissionCapabilities, commandRuntimeDrydocks)
		if serviceErr != nil {
			shutdownDesktopCommandRuntimeManagers(commandManagers, 2*time.Second)
			_ = stateStore.Close()
			return nil, serviceErr
		}
		commandAdapters = append(commandAdapters, dockerRuntime)
	}
	var commandRuntime application.CommandRuntimeRuntime
	if len(commandAdapters) == 1 {
		commandRuntime = commandAdapters[0]
	} else if len(commandAdapters) > 1 {
		commandRuntime, err = application.NewCommandRuntimeMultiplexer(commandAdapters...)
		if err != nil {
			shutdownDesktopCommandRuntimeManagers(commandManagers, 2*time.Second)
			_ = stateStore.Close()
			return nil, err
		}
	}
	if commandRuntime != nil {
		executionControl.WithCommandRuntime(commandRuntime)
	}
	uiEvidence, err := application.NewUIEvidenceReadService(stateStore)
	if err != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = commandManager.Shutdown(shutdownCtx)
		cancel()
		_ = stateStore.Close()
		return nil, err
	}
	if _, err := uiEvidence.Reconcile(context.Background()); err != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = commandManager.Shutdown(shutdownCtx)
		cancel()
		_ = stateStore.Close()
		return nil, apperror.Wrap(apperror.CodeUnavailable,
			"desktop UI evidence startup reconciliation failed", err)
	}
	var browserController *browserruntime.BrowserProcessController
	if config.UIEvidenceControlEnabled || config.FullCDPSessionControlEnabled {
		var controllerErr error
		browserController, controllerErr = browserruntime.NewPlatformBrowserProcessController()
		if controllerErr != nil {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = commandManager.Shutdown(shutdownCtx)
			cancel()
			_ = stateStore.Close()
			return nil, controllerErr
		}
	}
	if config.UIEvidenceControlEnabled {
		browserService := application.NewBrowserRuntimeService(stateStore,
			browserController, config.BrowserRuntimeCapabilities,
			config.BrowserCDPPermissionCapabilities)
		browserProvider, providerErr :=
			application.NewSafeWebUIEvidenceBrowserProvider(browserService)
		if providerErr != nil {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = commandManager.Shutdown(shutdownCtx)
			cancel()
			_ = stateStore.Close()
			return nil, providerErr
		}
		uiEvidence, err = application.NewUIEvidenceService(stateStore, commandRuntime,
			browserProvider, filepath.Join(home, "runtime", "ui-evidence-profiles"))
		if err != nil {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = commandManager.Shutdown(shutdownCtx)
			cancel()
			_ = stateStore.Close()
			return nil, err
		}
	}
	var fullCDPSessions *application.FullCDPProductionService
	fullCDPSessionControlEnabled := false
	if config.FullCDPSessionControlEnabled && browserController != nil &&
		browserController.FullCDPAvailable() {
		profileRoot, profileRootErr := browserruntime.PrepareFullCDPProfileRuntimeRoot(home)
		if profileRootErr != nil {
			shutdownDesktopCommandRuntimeManagers(commandManagers, 2*time.Second)
			_ = stateStore.Close()
			return nil, apperror.Wrap(apperror.CodeUnavailable,
				"desktop Full CDP Profile runtime could not be prepared", profileRootErr)
		}
		fullCDPSessions, err = application.NewFullCDPProductionService(stateStore,
			browserController, config.FullCDPRuntimeCapabilities,
			config.BrowserCDPPermissionCapabilities,
			config.ExecutionPermissionCapabilities, profileRoot)
		if err != nil {
			shutdownDesktopCommandRuntimeManagers(commandManagers, 2*time.Second)
			_ = stateStore.Close()
			return nil, err
		}
		fullCDPSessionControlEnabled = true
	}
	if fullCDPSessions != nil {
		// The Supervisor may act only through an operator-opened, ready session.
		// WithBrowserActions advertises no Open/Close tool and revalidates the
		// execution permission, browser permission, and runtime fence per action.
		executionControl.WithBrowserActions(fullCDPSessions)
	}
	dockerProposalExecutor, err := application.NewDockerSandboxProposalExecutor(
		dockerSandbox)
	if err != nil {
		_ = stateStore.Close()
		return nil, err
	}
	executionControl.WithDockerSandboxProposalExecutor(dockerProposalExecutor)
	planDeliveryControl := application.NewPlanDeliveryControlService(stateStore)
	approvalGateway := toolgateway.New(stateStore, checker).
		WithDockerSandboxProposalExecutor(dockerProposalExecutor).
		WithLifecycleHooks(hookEngine)
	mcpExecutor, err := application.NewMCPClientToolExecutor(mcpClient, stateStore,
		config.ExecutionPermissionCapabilities)
	if err != nil {
		_ = stateStore.Close()
		return nil, err
	}
	approvalGateway.WithMCPExecutor(mcpExecutor)
	approvalControl := application.NewApprovalControlService(stateStore,
		approvalGateway, checker)
	modelControl := application.NewModelControlService(models, stateStore)
	threadModelRoutes := application.NewThreadModelRouteService(stateStore, models)
	providerSearchReadiness := application.NewProviderSearchReadinessService(
		stateStore, providerSearchResolver)
	providerDefinitionControl, err := application.NewProviderDefinitionService(stateStore, models)
	if err != nil {
		_ = stateStore.Close()
		return nil, err
	}
	providerCredentialControl := application.NewProviderCredentialService(credentialStore).
		WithRegistryReload(models, stateStore)
	fileEditReview := application.NewFileEditReviewService(stateStore)
	fileEditProposal := application.NewFileEditProposalService(stateStore, checker)
	fileEditApply := application.NewFileEditApplyService(stateStore, checker,
		workspaceCheckpoints)
	runWakeControl := application.NewRunWakeControlService(stateStore)
	runWakeExecution := application.NewForegroundRunWakeConsumer(stateStore,
		executionControl)
	var wakeWorker *application.RunWakeWorker
	if config.RunWakeWorkerEnabled {
		wakeWorker, err = application.NewRunWakeWorker(
			application.NewRunWakeCoordinator(stateStore), runWakeExecution,
			application.RunWakeWorkerConfig{OnError: config.OnWakeWorkerError})
		if err != nil {
			_ = stateStore.Close()
			return nil, err
		}
	}
	var workerHealth httpapi.RunWakeWorkerHealthSource
	if wakeWorker != nil {
		workerHealth = wakeWorker
	}
	scheduledJobs := application.NewScheduledJobService(stateStore)
	var scheduledJobWorker *scheduler.Worker
	if config.ScheduledJobWorkerEnabled {
		scheduledJobWorker, err = scheduler.NewWorker(scheduledJobs, scheduler.WorkerConfig{
			OnError: config.OnScheduledJobWorkerError,
		})
		if err != nil {
			_ = stateStore.Close()
			return nil, err
		}
	}
	var scheduledWorkerHealth httpapi.ScheduledJobWorkerHealthSource
	if scheduledJobWorker != nil {
		scheduledWorkerHealth = scheduledJobWorker
	}
	var skillInstaller *application.SkillPackageRegistryService
	if config.SkillInstallationEnabled {
		objects, objectErr := skills.NewLocalPackageObjectStore(home)
		if objectErr != nil {
			_ = stateStore.Close()
			return nil, objectErr
		}
		registry, registryErr := skills.BuiltinRegistry()
		if registryErr != nil {
			_ = stateStore.Close()
			return nil, registryErr
		}
		skillInstaller = application.NewSkillPackageRegistryService(stateStore,
			objects, registry)
	}
	var terminalManager *terminalruntime.Manager
	var userTerminal *desktopUserTerminalService
	var debugAgentInput application.DebugTerminalAgentInputController
	var boundaryMonitor *terminalruntime.HostBoundaryMonitor
	if config.UserTerminalEnabled {
		terminalBroker := executionauth.NewTerminalInputBroker()
		terminalManager, err = terminalruntime.NewPlatformManager(terminalBroker)
		if err != nil {
			_ = stateStore.Close()
			return nil, err
		}
		userTerminal, err = newDesktopUserTerminalService(stateStore,
			terminalManager, config.ExecutionPermissionCapabilities)
		if err != nil {
			_ = terminalManager.Shutdown()
			_ = stateStore.Close()
			return nil, err
		}
		agentInputBridge, bridgeErr := terminalruntime.NewAgentInputBridge(
			terminalManager, terminalBroker)
		if bridgeErr != nil {
			_ = terminalManager.Shutdown()
			_ = stateStore.Close()
			return nil, bridgeErr
		}
		debugAgentInput, err = application.NewDebugTerminalAgentInputService(
			stateStore, agentInputBridge, checker,
			config.ExecutionPermissionCapabilities, true)
		if err != nil {
			_ = terminalManager.Shutdown()
			_ = stateStore.Close()
			return nil, err
		}
		boundaryMonitor, err = terminalruntime.NewPlatformHostBoundaryMonitor(
			terminalManager)
		if err != nil {
			_ = terminalManager.Shutdown()
			_ = stateStore.Close()
			return nil, err
		}
	}
	if debugAgentInput != nil &&
		config.ExecutionPermissionCapabilities.Allows(
			domain.RunExecutionPermissionDebug) {
		executionControl.WithDebugTerminalAgentInput(debugAgentInput)
	}
	controlledCommandExecutor, err := runner.NewPlatformControlledExecutor()
	if err != nil {
		if terminalManager != nil {
			_ = terminalManager.Shutdown()
		}
		_ = stateStore.Close()
		return nil, err
	}
	controlledCommandProposals :=
		application.NewControlledCommandProposalReviewService(
			stateStore, controlledCommandExecutor,
			config.ExecutionPermissionCapabilities)
	hostCommandExecutor, err := runner.NewPlatformHostExecutor()
	if err != nil {
		if terminalManager != nil {
			_ = terminalManager.Shutdown()
		}
		_ = stateStore.Close()
		return nil, err
	}
	hostCommandProposals := application.NewHostCommandProposalReviewService(
		stateStore, hostCommandExecutor, config.ExecutionPermissionCapabilities)
	embeddedAnalyzerExecution := application.NewEmbeddedAnalyzerExecutionService(stateStore)
	if standardCodeDockerReadiness != nil {
		capabilityReadinessRuntime.DockerReadiness = standardCodeDockerReadiness
		capabilityReadinessRuntime.DockerAvailable =
			standardCodeDockerReadiness.DaemonReachable
		capabilityReadinessRuntime.DockerBackendReady =
			standardCodeDockerReadiness.Ready
	}
	installedCommandRuntimeAdapters := []commandruntimeadapter.Identity{}
	if commandRuntime != nil {
		installedCommandRuntimeAdapters = commandRuntime.InstalledCommandRuntimeAdapters()
	}
	capabilityReadinessRuntime.CommandRuntimeAdapters = installedCommandRuntimeAdapters
	var standardCodePreset *application.StandardCodePresetService
	if config.ControlToken != "" && config.RunControlEnabled &&
		config.ExecutionPermissionControlEnabled &&
		commandRuntimeDrydocks != nil {
		capabilityReadinessRuntime.StandardCodePresetEnabled = true
		standardCodePreset, err = application.NewStandardCodePresetService(stateStore,
			commandRuntimeDrydocks, capabilityReadinessRuntime)
		if err != nil {
			if terminalManager != nil {
				_ = terminalManager.Shutdown()
			}
			_ = stateStore.Close()
			return nil, err
		}
	}
	commandRuntimeAdapterReady := false
	for _, adapter := range installedCommandRuntimeAdapters {
		ready := true
		switch adapter.Backend {
		case application.CommandRuntimeLocalSandboxBackend:
			ready = capabilityReadinessRuntime.LocalBackendReady
		case application.CommandRuntimeDockerSandboxBackend:
			ready = capabilityReadinessRuntime.DockerBackendReady
		}
		commandRuntimeAdapterReady = commandRuntimeAdapterReady || ready
	}
	threadTurnControl := application.NewThreadTurnServiceWithExecutionCapabilities(
		stateStore, lifecycleControl, executionControl,
		config.ExecutionPermissionCapabilities).WithModelRouteRegistry(models).
		WithLifecycleHooks(hookEngine)
	api, err := httpapi.New(stateStore, httpapi.Config{
		AccessToken: config.ReadToken, ControlToken: config.ControlToken,
		RunControlEnabled:                       config.RunControlEnabled,
		ExecutionPermissionControlEnabled:       config.ExecutionPermissionControlEnabled,
		ExecutionPermissionCapabilities:         config.ExecutionPermissionCapabilities,
		BrowserCDPPermissionControlEnabled:      config.BrowserCDPPermissionControlEnabled,
		BrowserCDPPermissionCapabilities:        config.BrowserCDPPermissionCapabilities,
		CapabilityReadinessRuntime:              &capabilityReadinessRuntime,
		CommandRuntimeAdapters:                  installedCommandRuntimeAdapters,
		CommandRuntimeAdvertiser:                commandRuntime,
		RunCreationEnabled:                      config.RunCreationEnabled,
		StandardCodePresetEnabled:               standardCodePreset != nil,
		SessionMessageEnabled:                   config.SessionMessageEnabled,
		SessionSteeringControlEnabled:           config.SessionSteeringControlEnabled,
		RunLifecycleEnabled:                     config.RunLifecycleEnabled,
		RunExecutionEnabled:                     config.RunExecutionEnabled,
		PlanDeliveryControlEnabled:              config.PlanDeliveryControlEnabled,
		ApprovalControlEnabled:                  config.ApprovalControlEnabled,
		WebFetchAuthorizationSchedulerEnabled:   webFetchAuthorizationSchedulerEnabled,
		ControlledCommandProposalControlEnabled: config.ControlledCommandProposalControlEnabled,
		HostCommandProposalControlEnabled:       config.HostCommandProposalControlEnabled,
		ModelControlEnabled:                     config.ModelControlEnabled,
		ProviderDefinitionEnabled:               config.ModelControlEnabled,
		ProviderCredentialEnabled:               config.ProviderCredentialEnabled,
		FileEditReviewEnabled:                   config.FileEditReviewEnabled,
		FileEditProposalEnabled:                 config.FileEditProposalEnabled,
		RunWakeControlEnabled:                   config.RunWakeControlEnabled,
		FileEditApplyEnabled:                    config.FileEditApplyEnabled,
		RunWakeExecutionEnabled:                 config.RunWakeExecutionEnabled,
		RunWakeWorkerEnabled:                    config.RunWakeWorkerEnabled,
		ScheduledJobControlEnabled:              config.ScheduledJobControlEnabled,
		ScheduledJobWorkerEnabled:               config.ScheduledJobWorkerEnabled,
		SkillInstallationEnabled:                config.SkillInstallationEnabled,
		EvidenceAttachmentEnabled:               config.EvidenceAttachmentEnabled,
		VerificationEvidenceEnabled:             config.VerificationEvidenceEnabled,
		EmbeddedAnalyzerExecutionEnabled:        config.EmbeddedAnalyzerExecutionEnabled,
		WorkspaceCheckpointControlEnabled:       config.ControlToken != "",
		GitAdvancedControlEnabled:               config.GitAdvancedControlEnabled,
		GitHubReviewControlEnabled:              config.GitHubReviewControlEnabled,
		BatchDeliveryControlEnabled:             config.BatchDeliveryControlEnabled,
		BatchDeliveryHostValidationEnabled:      config.BatchDeliveryHostValidationEnabled,
		ExtensionControlEnabled:                 config.ControlToken != "",
		LifecycleHooks:                          hookEngine,
		UIEvidenceControlEnabled:                config.UIEvidenceControlEnabled,
		FullCDPSessionControlEnabled:            fullCDPSessionControlEnabled,
		RunLifecycleController:                  lifecycleControl,
		ThreadTurnController:                    threadTurnControl,
		StandardCodePresetController:            standardCodePreset,
		StandardCodeDeliveryController:          standardCodeDelivery,
		RunExecutionController:                  executionControl,
		PublicModelStreamSource:                 executionControl,
		PlanDeliveryController:                  planDeliveryControl,
		ApprovalController:                      approvalControl,
		ControlledCommandProposalController:     controlledCommandProposals,
		HostCommandProposalController:           hostCommandProposals,
		ModelControlController:                  modelControl,
		ThreadModelRouteController:              threadModelRoutes,
		ProviderSearchReadinessController:       providerSearchReadiness,
		ProviderDefinitionController:            providerDefinitionControl,
		PriceSnapshotController:                 stateStore,
		FanoutExecutionController: application.NewReadOnlyFanoutExecutionService(
			stateStore, models.Router(), checker),
		ChildTaskControlController:          application.NewChildTaskControlService(stateStore),
		ProviderCredentialController:        providerCredentialControl,
		FileEditReviewController:            fileEditReview,
		FileEditProposalController:          fileEditProposal,
		RunWakeController:                   runWakeControl,
		FileEditApplyController:             fileEditApply,
		RunWakeExecutionController:          runWakeExecution,
		RunWakeWorkerHealthSource:           workerHealth,
		ScheduledJobController:              scheduledJobs,
		ScheduledJobWorkerHealthSource:      scheduledWorkerHealth,
		SkillInstallationController:         skillInstaller,
		EmbeddedAnalyzerExecutionController: embeddedAnalyzerExecution,
		WorkspaceCheckpointController:       workspaceCheckpoints,
		GitAdvancedController:               gitAdvanced,
		GitHubReviewController:              githubReview,
		BatchDeliveryController:             batchDelivery,
		ExtensionController:                 extensionControl,
		CodeIntelSource:                     codeIntelManager,
		UIEvidenceController:                uiEvidence,
		FullCDPSessionController:            fullCDPSessions,
		DockerSandboxController:             dockerSandbox,
		ModelRegistry:                       models,
		AppVersion:                          config.AppVersion, UIHandler: config.UIHandler,
	})
	if err != nil {
		if terminalManager != nil {
			_ = terminalManager.Shutdown()
		}
		_ = stateStore.Close()
		return nil, err
	}
	// A review decision and its provider/tool continuation cannot share one
	// SQLite transaction. A process-owned worker reconciles that durable queue
	// for the whole desktop lifetime. It is deliberately not request-scoped: a
	// disconnected renderer must not cancel an approved exact continuation.
	var cancelWebFetchReconcile context.CancelFunc
	var webFetchReconcileDone chan struct{}
	if webFetchAuthorizationSchedulerEnabled {
		webFetchReconcileCtx, cancel := context.WithCancel(context.Background())
		cancelWebFetchReconcile = cancel
		webFetchReconcileDone = make(chan struct{})
		go func() {
			defer close(webFetchReconcileDone)
			runWebFetchAuthorizationReconciler(webFetchReconcileCtx, executionControl,
				2*time.Second)
		}()
	}
	codeIntelTransferred = true
	commandManagersTransferred = true
	return &ControlPlane{stateStore: stateStore, workspaceManager: workspaceManager,
		handler:        api.Handler(),
		skillInstaller: skillInstaller, dockerSandbox: dockerSandbox,
		userTerminal:                   userTerminal,
		debugAgentInput:                debugAgentInput,
		commandRuntime:                 commandRuntime,
		standardCodeDrydocks:           commandRuntimeDrydocks,
		standardCodePreset:             standardCodePreset,
		policyChecker:                  checker,
		uiEvidence:                     uiEvidence,
		fullCDPSessions:                fullCDPSessions,
		commandRuntimeManager:          commandManager,
		commandRuntimeManagers:         commandManagers,
		commandRuntimeAdapterInstalled: len(installedCommandRuntimeAdapters) > 0,
		commandRuntimeAdapterReady:     commandRuntimeAdapterReady,
		standardCodePresetEnabled:      standardCodePreset != nil,
		webFetchReconcileCancel:        cancelWebFetchReconcile,
		webFetchReconcileDone:          webFetchReconcileDone,
		codeIntelManager:               codeIntelManager,
		terminalManager:                terminalManager, boundaryMonitor: boundaryMonitor,
		wakeWorker: wakeWorker, scheduledJobWorker: scheduledJobWorker}, nil
}

// CommandRuntimeProcessStatus exposes non-authorizing process facts to the
// native Desktop bootstrap. Current-Run grant remains in the per-Run readiness
// projection and is never inferred by the renderer.
func (c *ControlPlane) CommandRuntimeProcessStatus() (installed, ready bool) {
	if c == nil {
		return false, false
	}
	return c.commandRuntimeAdapterInstalled, c.commandRuntimeAdapterReady
}

func (c *ControlPlane) StandardCodePresetEnabled() bool {
	return c != nil && c.standardCodePresetEnabled
}

// FullCDPSessionControlEnabled reports whether this process installed the
// production Full CDP session owner. It projects availability only and grants
// no Run permission or browser authority.
func (c *ControlPlane) FullCDPSessionControlEnabled() bool {
	return c != nil && c.fullCDPSessions != nil
}

// RegisterWorkspaceDirectory keeps the selected host path entirely within Go
// and returns only a bounded metadata projection to the native bridge.
func (c *ControlPlane) RegisterWorkspaceDirectory(ctx context.Context,
	selectedPath string) (WorkspaceImportSummary, error) {
	if c == nil || c.workspaceManager == nil {
		return WorkspaceImportSummary{}, apperror.New(apperror.CodeFailedPrecondition,
			"desktop workspace registrar is unavailable")
	}
	c.workspaceImportMu.Lock()
	defer c.workspaceImportMu.Unlock()
	record, err := c.workspaceManager.Import(ctx, selectedPath)
	if errors.Is(err, workspace.ErrInvalidImportDirectory) {
		return WorkspaceImportSummary{}, apperror.New(apperror.CodeInvalidArgument,
			"selected workspace directory is invalid")
	}
	if err != nil {
		return WorkspaceImportSummary{}, apperror.New(apperror.CodeUnavailable,
			"workspace directory registration failed")
	}
	return WorkspaceImportSummary{ID: record.ID, Name: record.Name,
		CreatedAt: record.CreatedAt}, nil
}

func (c *ControlPlane) Handler() http.Handler {
	if c == nil {
		return nil
	}
	return c.handler
}

func (c *ControlPlane) SkillInstaller() SkillPackageInstaller {
	if c == nil {
		return nil
	}
	return c.skillInstaller
}

func (c *ControlPlane) UserTerminalController() UserTerminalController {
	if c == nil {
		return nil
	}
	return c.userTerminal
}

// DebugTerminalAgentInputController exposes only grant/query/revoke to the
// trusted Desktop bridge. The bearer stays inside Go; HTTP, repository
// content, Skills, and models cannot mint or retrieve it.
func (c *ControlPlane) DebugTerminalAgentInputController() application.DebugTerminalAgentInputController {
	if c == nil {
		return nil
	}
	return c.debugAgentInput
}

// CodeIntelEnabled reports whether this process loaded an explicit reviewed
// language-server configuration. It grants no renderer process authority.
func (c *ControlPlane) CodeIntelEnabled() bool {
	return c != nil && c.codeIntelManager != nil
}

// ResolveWorkspace keeps the registered root inside the Go control plane. The
// renderer selects only an opaque Workspace ID and never receives RootPath.
func (c *ControlPlane) ResolveWorkspace(ctx context.Context,
	workspaceID string) (WorkspaceOpenTarget, error) {
	if c == nil || c.stateStore == nil {
		return WorkspaceOpenTarget{}, apperror.New(apperror.CodeFailedPrecondition,
			"desktop workspace resolver is unavailable")
	}
	if ctx == nil || !validWorkspaceIdentity(workspaceID) {
		return WorkspaceOpenTarget{}, apperror.New(apperror.CodeInvalidArgument,
			"desktop workspace identifier is invalid")
	}
	record, err := c.stateStore.GetWorkspaceByID(ctx, workspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return WorkspaceOpenTarget{}, apperror.New(apperror.CodeNotFound,
			"desktop workspace was not found")
	}
	if err != nil {
		return WorkspaceOpenTarget{}, apperror.New(apperror.CodeUnavailable,
			"desktop workspace lookup failed")
	}
	return WorkspaceOpenTarget{
		ID: record.ID, Name: record.Name, RootPath: filepath.Clean(record.RootPath),
	}, nil
}

func (c *ControlPlane) StartWakeWorker(parent context.Context) error {
	if c == nil {
		return errors.New("desktop control plane is unavailable")
	}
	if parent == nil {
		return errors.New("desktop wake worker context is required")
	}
	c.workerMu.Lock()
	defer c.workerMu.Unlock()
	if c.closed {
		return errors.New("desktop control plane is closed")
	}
	if c.wakeWorker == nil && c.scheduledJobWorker == nil {
		return nil
	}
	if c.workerDone != nil || c.scheduledWorkerDone != nil {
		return errors.New("desktop background worker is already started")
	}
	if c.wakeWorker != nil {
		ctx, cancel := context.WithCancel(parent)
		c.workerCancel = cancel
		c.workerDone = make(chan struct{})
		go func(done chan struct{}) {
			defer close(done)
			_ = c.wakeWorker.Run(ctx)
		}(c.workerDone)
	}
	if c.scheduledJobWorker != nil {
		ctx, cancel := context.WithCancel(parent)
		c.scheduledWorkerCancel = cancel
		c.scheduledWorkerDone = make(chan struct{})
		go func(done chan struct{}) {
			defer close(done)
			_ = c.scheduledJobWorker.Run(ctx)
		}(c.scheduledWorkerDone)
	}
	return nil
}

func (c *ControlPlane) StartTerminalBoundaryMonitor(parent context.Context) error {
	if c == nil {
		return errors.New("desktop control plane is unavailable")
	}
	if c.boundaryMonitor == nil && c.commandRuntimeManager == nil {
		return nil
	}
	if parent == nil || parent.Err() != nil {
		return errors.New("desktop terminal boundary context is required")
	}
	c.terminalWorkerMu.Lock()
	defer c.terminalWorkerMu.Unlock()
	c.workerMu.Lock()
	closed := c.closed
	c.workerMu.Unlock()
	if closed || c.terminalDone != nil {
		return errors.New("desktop terminal boundary monitor is already started")
	}
	if c.boundaryMonitor != nil {
		if err := c.boundaryMonitor.Start(parent); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithCancel(parent)
	c.terminalCancel = cancel
	c.terminalDone = make(chan struct{})
	go c.runTerminalBindingWorker(ctx, c.terminalDone)
	return nil
}

func (c *ControlPlane) runTerminalBindingWorker(ctx context.Context,
	done chan struct{},
) {
	defer close(done)
	const interval = 500 * time.Millisecond
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	if c.userTerminal != nil {
		c.userTerminal.reconcileBindings(ctx)
	}
	if c.debugAgentInput != nil {
		c.debugAgentInput.Reconcile(ctx)
	}
	if c.commandRuntimeManager != nil {
		if err := c.reconcileCommandRuntime(ctx); err != nil {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
			_ = c.commandRuntimeManager.Shutdown(shutdownCtx)
			cancel()
			return
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if c.userTerminal != nil {
				c.userTerminal.reconcileBindings(ctx)
			}
			if c.debugAgentInput != nil {
				c.debugAgentInput.Reconcile(ctx)
			}
			if c.commandRuntimeManager != nil {
				if err := c.reconcileCommandRuntime(ctx); err != nil {
					shutdownCtx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
					_ = c.commandRuntimeManager.Shutdown(shutdownCtx)
					cancel()
					return
				}
			}
		}
	}
}

func (c *ControlPlane) reconcileCommandRuntime(ctx context.Context) error {
	if c.commandRuntime != nil {
		_, err := c.commandRuntime.Reconcile(ctx)
		return err
	}
	if c.commandRuntimeManager != nil {
		_, err := c.commandRuntimeManager.ReconcileStartup(ctx)
		return err
	}
	return nil
}

func shutdownDesktopCommandRuntimeManagers(managers []*runner.CommandRuntimeManager,
	timeout time.Duration,
) {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	for _, manager := range managers {
		if manager != nil {
			_ = manager.Shutdown(ctx)
		}
	}
}

func (c *ControlPlane) Close() error {
	if c == nil {
		return nil
	}
	c.closeOnce.Do(func() {
		c.workerMu.Lock()
		c.closed = true
		cancel, done := c.workerCancel, c.workerDone
		scheduledCancel, scheduledDone := c.scheduledWorkerCancel, c.scheduledWorkerDone
		webFetchCancel, webFetchDone := c.webFetchReconcileCancel, c.webFetchReconcileDone
		c.workerCancel = nil
		c.workerDone = nil
		c.scheduledWorkerCancel = nil
		c.scheduledWorkerDone = nil
		c.webFetchReconcileCancel = nil
		c.webFetchReconcileDone = nil
		c.workerMu.Unlock()
		var fullCDPShutdown <-chan error
		var fullCDPShutdownCancel context.CancelFunc
		if c.fullCDPSessions != nil {
			shutdownContext, shutdownCancel := context.WithTimeout(
				context.Background(), 35*time.Second)
			shutdownDone := make(chan error, 1)
			fullCDPShutdown = shutdownDone
			fullCDPShutdownCancel = shutdownCancel
			service := c.fullCDPSessions
			go func() {
				shutdownDone <- service.Close(shutdownContext)
			}()
		}
		if cancel != nil {
			cancel()
		}
		if done != nil {
			<-done
		}
		if scheduledCancel != nil {
			scheduledCancel()
		}
		if scheduledDone != nil {
			<-scheduledDone
		}
		if webFetchCancel != nil {
			webFetchCancel()
		}
		if webFetchDone != nil {
			<-webFetchDone
		}
		c.terminalWorkerMu.Lock()
		terminalCancel, terminalDone := c.terminalCancel, c.terminalDone
		c.terminalCancel = nil
		c.terminalDone = nil
		c.terminalWorkerMu.Unlock()
		if terminalCancel != nil {
			terminalCancel()
		}
		if terminalDone != nil {
			<-terminalDone
		}
		if c.debugAgentInput != nil {
			shutdownContext, shutdownCancel := context.WithTimeout(
				context.Background(), 2*time.Second)
			c.debugAgentInput.Shutdown(shutdownContext)
			shutdownCancel()
		}
		if c.uiEvidence != nil {
			shutdownContext, shutdownCancel := context.WithTimeout(
				context.Background(), 35*time.Second)
			c.closeErr = errors.Join(c.closeErr, c.uiEvidence.Close(shutdownContext))
			shutdownCancel()
		}
		if fullCDPShutdown != nil {
			// FullCDPProductionService.Close does not return from its timeout path
			// until any in-flight close audit has drained and future close audits
			// are detached. SQLite can therefore be closed later in this method
			// without a cleanup goroutine writing through a stale store owner.
			c.closeErr = errors.Join(c.closeErr, <-fullCDPShutdown)
			fullCDPShutdownCancel()
		}
		if len(c.commandRuntimeManagers) > 0 {
			shutdownContext, shutdownCancel := context.WithTimeout(
				context.Background(), 7*time.Second)
			for _, manager := range c.commandRuntimeManagers {
				if manager != nil {
					c.closeErr = errors.Join(c.closeErr,
						manager.Shutdown(shutdownContext))
				}
			}
			shutdownCancel()
		} else if c.commandRuntimeManager != nil {
			shutdownContext, shutdownCancel := context.WithTimeout(
				context.Background(), 7*time.Second)
			c.closeErr = errors.Join(c.closeErr,
				c.commandRuntimeManager.Shutdown(shutdownContext))
			shutdownCancel()
		}
		if c.codeIntelManager != nil {
			shutdownContext, shutdownCancel := context.WithTimeout(
				context.Background(), codeintel.MaximumShutdownGracePeriod)
			c.closeErr = errors.Join(c.closeErr, c.codeIntelManager.Close(shutdownContext))
			shutdownCancel()
		}
		if c.stateStore == nil {
			c.closeErr = errors.New("desktop control plane store is unavailable")
			return
		}
		if c.boundaryMonitor != nil {
			c.closeErr = errors.Join(c.closeErr, c.boundaryMonitor.Stop())
		}
		if c.terminalManager != nil {
			c.closeErr = errors.Join(c.closeErr,
				c.terminalManager.Shutdown())
		}
		c.closeErr = errors.Join(c.closeErr, c.stateStore.Close())
	})
	return c.closeErr
}
