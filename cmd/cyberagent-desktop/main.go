//go:build desktop

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"cyberagent-workbench/internal/app"
	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/browserruntime"
	"cyberagent-workbench/internal/desktop"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/httpapi"
	"cyberagent-workbench/internal/sandbox"
	"cyberagent-workbench/internal/webui"
	webassets "cyberagent-workbench/web"

	"github.com/wailsapp/wails/v2"
	wailsassetserver "github.com/wailsapp/wails/v2/pkg/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	desktopSingleInstanceID = "e3305a58-3d1e-4e2f-b4ca-d1032a737b96"
	defaultDesktopWidth     = 1440
	defaultDesktopHeight    = 900
)

type desktopOptions struct {
	operatorPreview        bool
	profileControl         bool
	permissionControl      bool
	workspaceSandbox       bool
	dangerFullAccess       bool
	debugMaximumAccess     bool
	browserCDPControl      bool
	fullCDPDebug           bool
	runCreation            bool
	sessionMessages        bool
	sessionSteeringControl bool
	runLifecycle           bool
	runExecution           bool
	planDeliveryControl    bool
	approvalControl        bool
	commandProposalControl bool
	hostCommandProposals   bool
	modelControl           bool
	providerCredentials    bool
	fileEditReview         bool
	fileEditProposals      bool
	runWakeControl         bool
	fileEditApply          bool
	runWakeExecution       bool
	runWakeWorker          bool
	scheduledJobControl    bool
	scheduledJobWorker     bool
	skillInstallation      bool
	evidenceAttachment     bool
	verificationEvidence   bool
	embeddedAnalyzer       bool
	batchDeliveryControl   bool
	batchValidation        bool
	uiEvidence             bool
	userTerminal           bool
	dockerExecution        bool
	codeIntelConfig        string
	gitAdvanced            bool
	githubReview           bool
	gitWorktreeRoot        string
	version                bool
}

type nativeSkillPackagePicker struct{}

type nativeWorkspaceDirectoryPicker struct{}

type wailsWindowRestorer struct{}

func (wailsWindowRestorer) Unminimise(ctx context.Context) { runtime.WindowUnminimise(ctx) }
func (wailsWindowRestorer) Show(ctx context.Context)       { runtime.WindowShow(ctx) }

func secondInstanceHandler(lifecycle *desktop.Lifecycle) func(options.SecondInstanceData) {
	return func(_ options.SecondInstanceData) {
		lifecycle.RequestRestore()
	}
}

func shouldMaximiseDesktopWindow(screens []runtime.Screen) bool {
	var selected *runtime.Screen
	for index := range screens {
		if screens[index].IsCurrent {
			selected = &screens[index]
			break
		}
		if selected == nil && screens[index].IsPrimary {
			selected = &screens[index]
		}
	}
	return selected != nil && (selected.Size.Width < defaultDesktopWidth ||
		selected.Size.Height < defaultDesktopHeight)
}

func (nativeSkillPackagePicker) OpenSkillPackage(ctx context.Context) (string, error) {
	if ctx == nil {
		return "", errors.New("desktop lifecycle is unavailable")
	}
	return runtime.OpenFileDialog(ctx, runtime.OpenDialogOptions{
		Title: "Select Traverse Board Skill package",
		Filters: []runtime.FileFilter{
			{DisplayName: "Traverse Board Skill package (*.zip)", Pattern: "*.zip"},
		},
		ShowHiddenFiles:      false,
		CanCreateDirectories: false,
		ResolvesAliases:      false,
	})
}

func (nativeWorkspaceDirectoryPicker) OpenWorkspaceDirectory(ctx context.Context) (string, error) {
	if ctx == nil {
		return "", errors.New("desktop lifecycle is unavailable")
	}
	return runtime.OpenDirectoryDialog(ctx, runtime.OpenDialogOptions{
		Title:                "Select Traverse Board workspace folder",
		ShowHiddenFiles:      true,
		CanCreateDirectories: false,
		ResolvesAliases:      true,
	})
}

type inProcessAPIHandler struct {
	next http.Handler
}

func (h inProcessAPIHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if h.next == nil || request == nil || request.URL == nil {
		http.Error(writer, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	if !trustedDesktopRendererOrigin(request) {
		http.Error(writer, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return
	}
	trusted := request.Clone(request.Context())
	trusted.Host = "127.0.0.1"
	trusted.RemoteAddr = "127.0.0.1:0"
	trusted.URL.Scheme = ""
	trusted.URL.Host = ""
	if trusted.URL != nil && trusted.URL.Path == "" {
		trusted.URL.Path = "/"
	}
	trusted.RequestURI = trusted.URL.RequestURI()
	if trusted.RequestURI == "" {
		trusted.RequestURI = "/"
	}
	if (trusted.Method == http.MethodGet || trusted.Method == http.MethodHead) &&
		trusted.ContentLength == -1 && len(trusted.TransferEncoding) == 0 && trusted.Body == http.NoBody &&
		trusted.Header.Get("Content-Length") == "" {
		trusted.ContentLength = 0
	}
	h.next.ServeHTTP(writer, trusted)
}

func trustedDesktopRendererOrigin(request *http.Request) bool {
	if request == nil || request.URL == nil || request.URL.User != nil || request.URL.Fragment != "" ||
		request.URL.RawFragment != "" || request.URL.Opaque != "" {
		return false
	}
	// The Wails AssetServer enforces the platform webview host itself before
	// the request reaches this handler; trustedDesktopRendererHost pins the
	// exact per-platform authority (wails.localhost on Windows, wails on
	// macOS where the custom wails:// scheme owns the renderer).
	if !strings.EqualFold(request.Host, trustedDesktopRendererHost()) ||
		!containsUserAgentToken(request.UserAgent(), wailsassetserver.WailsUserAgentValue) {
		return false
	}
	if request.URL.Scheme == "" || request.URL.Host == "" {
		// Wails converts the intercepted WebView request into server form before
		// invoking the custom AssetServer handler, leaving authority in Request.Host.
		return request.URL.Scheme == "" && request.URL.Host == ""
	}
	return strings.EqualFold(request.URL.Scheme, "http") &&
		strings.EqualFold(request.URL.Hostname(), "wails.localhost") && request.URL.Port() == ""
}

func containsUserAgentToken(value, expected string) bool {
	for _, token := range strings.Fields(value) {
		if strings.EqualFold(token, expected) {
			return true
		}
	}
	return false
}

type desktopBindingError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func main() {
	config, err := parseDesktopOptions(os.Args[1:])
	if err != nil {
		reportDesktopStartupFailure(err)
		os.Exit(2)
	}
	if config.version {
		fmt.Fprintf(os.Stdout, "%s desktop %s\n", app.Name, app.Version)
		return
	}
	if err := runDesktop(config); err != nil {
		reportDesktopStartupFailure(err)
		os.Exit(1)
	}
}

func parseDesktopOptions(args []string) (desktopOptions, error) {
	fs := flag.NewFlagSet("cyberagent-desktop", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	operatorPreview := fs.Bool("operator-preview", false,
		"enable the safe operator Desktop capability bundle for local product testing")
	profileControl := fs.Bool("enable-profile-control", false,
		"enable only the non-authorizing Run execution-profile control")
	permissionControl := fs.Bool("enable-permission-control", false,
		"enable operator selection of execution permission modes")
	workspaceSandbox := fs.Bool("enable-workspace-sandbox", false,
		"probe and enable the verified Windows Workspace Sandbox")
	dangerFullAccess := fs.Bool("enable-danger-full-access", false,
		"enable unsandboxed one-shot host execution permission selection")
	debugMaximumAccess := fs.Bool("enable-debug-maximum-access", false,
		"enable persistent maximum-access debug permission selection")
	browserCDPControl := fs.Bool("enable-browser-cdp-control", false,
		"enable browser CDP permission selection")
	fullCDPDebug := fs.Bool("enable-full-cdp-debug", false,
		"enable highly sensitive complete CDP debugging selection")
	runCreation := fs.Bool("enable-run-creation", false,
		"enable idempotent workspace-bound Run creation")
	sessionMessages := fs.Bool("enable-session-messages", false,
		"enable idempotent Run-bound Session message submission")
	sessionSteeringControl := fs.Bool("enable-session-steering-control", false,
		"enable pending-only Run-bound Session steering cancellation")
	runLifecycle := fs.Bool("enable-run-lifecycle", false,
		"enable idempotent Run start, pause, and resume control")
	runExecution := fs.Bool("enable-run-execution", false,
		"enable bounded queued Run execution through the Go Supervisor")
	planDeliveryControl := fs.Bool("enable-plan-delivery", false,
		"enable operator Plan direction selection and explicit Deliver transition")
	approvalControl := fs.Bool("enable-approvals", false,
		"enable bounded approve-once and deny decisions for durable approvals")
	commandProposalControl := fs.Bool("enable-command-proposals", false,
		"enable review and one-shot execution of Agent-proposed fixed Go commands")
	hostCommandProposals := fs.Bool("enable-host-command-proposals", false,
		"enable exact process or canonical PowerShell/Git Bash proposals with independent operator review")
	modelControl := fs.Bool("enable-model-control", false,
		"enable persisted model route selection and explicit connectivity diagnostics")
	providerCredentials := fs.Bool("enable-provider-credentials", false,
		"enable OS-owned Provider credential changes")
	fileEditReview := fs.Bool("enable-file-edit-review", false,
		"enable review-only file edit approval or denial without applying files")
	fileEditProposals := fs.Bool("enable-file-edit-proposals", false,
		"enable Go-issued interactive FileEdit proposal sources")
	runWakeControl := fs.Bool("enable-run-wake", false,
		"enable durable bounded Run wake intent scheduling and cancellation")
	fileEditApply := fs.Bool("enable-file-edit-apply", false,
		"enable independently authorized approved FileEdit application")
	runWakeExecution := fs.Bool("enable-run-wake-execution", false,
		"enable explicitly launched foreground wake execution")
	runWakeWorker := fs.Bool("enable-wake-worker", false,
		"enable the bounded single-owner Run wake worker")
	scheduledJobControl := fs.Bool("enable-scheduled-jobs", false,
		"enable durable scheduled job creation and lifecycle control")
	scheduledJobWorker := fs.Bool("enable-scheduled-job-worker", false,
		"enable the process-local single-concurrency scheduled job worker")
	skillInstallation := fs.Bool("enable-skill-installation", false,
		"enable confirmed inert Skill package installation")
	evidenceAttachment := fs.Bool("enable-evidence-attachments", false,
		"enable idempotent non-authorizing Workspace evidence attachment")
	verificationEvidence := fs.Bool("enable-verification-evidence", false,
		"enable immutable operator verification evidence recording")
	embeddedAnalyzer := fs.Bool("enable-embedded-analyzer", false,
		"enable the fixed bounded embedded Rust/WASI analyzer")
	batchDeliveryControl := fs.Bool("enable-batch-delivery-control", false,
		"enable confirmed batch delivery preparation, review, merge, cancellation, and recovery")
	batchValidation := fs.Bool("enable-batch-validation-execution", false,
		"enable fixed offline go/npm checks for confirmed batch deliveries")
	uiEvidence := fs.Bool("enable-ui-evidence", false,
		"enable source-bound real-browser UI evidence for explicitly authorized Runs")
	userTerminal := fs.Bool("enable-user-terminal", false,
		"enable the user-owned Debug ConPTY terminal")
	dockerExecution := fs.Bool("enable-docker-execution", false,
		"enable product Docker Sandbox admission and execution on the fixed local daemon")
	codeIntelConfig := fs.String("code-intel-config", "",
		"absolute operator-reviewed code-intel config")
	gitAdvanced := fs.Bool("enable-git-advanced", false,
		"enable approval-gated git-advanced.v1 mutations")
	githubReview := fs.Bool("enable-github-review", false,
		"enable GitHub App review evidence and approval-gated write-back")
	gitWorktreeRoot := fs.String("git-worktree-root", "",
		"product-managed Git worktree root; defaults below CYBERAGENT_HOME")
	version := fs.Bool("version", false, "print version and exit")
	if err := fs.Parse(args); err != nil {
		return desktopOptions{}, err
	}
	if fs.NArg() != 0 {
		return desktopOptions{}, errors.New("cyberagent-desktop accepts no positional arguments")
	}
	if *operatorPreview {
		*profileControl = true
		*permissionControl = true
		*workspaceSandbox = true
		*browserCDPControl = true
		*runCreation = true
		*sessionMessages = true
		*sessionSteeringControl = true
		*runLifecycle = true
		*runExecution = true
		*planDeliveryControl = true
		*approvalControl = true
		*commandProposalControl = true
		*hostCommandProposals = true
		*modelControl = true
		*providerCredentials = true
		*fileEditReview = true
		*fileEditProposals = true
		*runWakeControl = true
		*fileEditApply = true
		*runWakeExecution = true
		*scheduledJobControl = true
		*scheduledJobWorker = true
		*skillInstallation = true
		*evidenceAttachment = true
		*verificationEvidence = true
		*embeddedAnalyzer = true
		*batchDeliveryControl = true
		*gitAdvanced = true
		*githubReview = true
	}
	capabilities := domain.ExecutionPermissionRuntimeCapabilities{
		OperatorApprovalEnabled:   *permissionControl,
		DangerFullAccessEnabled:   *dangerFullAccess,
		DebugMaximumAccessEnabled: *debugMaximumAccess,
	}
	if err := capabilities.Validate(); err != nil {
		return desktopOptions{}, err
	}
	if *debugMaximumAccess && !*userTerminal {
		return desktopOptions{}, errors.New(
			"debug maximum access requires --enable-user-terminal")
	}
	if *hostCommandProposals && !*permissionControl {
		return desktopOptions{}, errors.New(
			"host command proposals require --enable-permission-control")
	}
	if *workspaceSandbox && !*permissionControl {
		return desktopOptions{}, errors.New(
			"Workspace Sandbox requires --enable-permission-control")
	}
	if *dockerExecution && !*permissionControl {
		return desktopOptions{}, errors.New(
			"Docker execution requires --enable-permission-control")
	}
	if *gitAdvanced && !*permissionControl {
		return desktopOptions{}, errors.New(
			"Git advanced control requires --enable-permission-control")
	}
	if *githubReview && !*permissionControl {
		return desktopOptions{}, errors.New(
			"GitHub review control requires --enable-permission-control")
	}
	if strings.TrimSpace(*gitWorktreeRoot) != "" && !*gitAdvanced && !*githubReview {
		return desktopOptions{}, errors.New(
			"--git-worktree-root requires --enable-git-advanced or --enable-github-review")
	}
	if *batchValidation && (!*batchDeliveryControl || !*permissionControl || !*dangerFullAccess) {
		return desktopOptions{}, errors.New(
			"batch validation execution requires --enable-batch-delivery-control, --enable-permission-control, and --enable-danger-full-access")
	}
	if *fullCDPDebug && (!*browserCDPControl || !*debugMaximumAccess) {
		return desktopOptions{}, errors.New(
			"full CDP debug requires --enable-browser-cdp-control and --enable-debug-maximum-access")
	}
	if *scheduledJobWorker && !*scheduledJobControl {
		return desktopOptions{}, errors.New(
			"scheduled job worker requires --enable-scheduled-jobs")
	}
	if *uiEvidence && (!*runExecution || !*dangerFullAccess || !*browserCDPControl) {
		return desktopOptions{}, errors.New(
			"UI evidence requires --enable-run-execution, --enable-danger-full-access, and --enable-browser-cdp-control")
	}
	return desktopOptions{operatorPreview: *operatorPreview,
		profileControl: *profileControl, runCreation: *runCreation,
		permissionControl: *permissionControl, dangerFullAccess: *dangerFullAccess,
		workspaceSandbox:       *workspaceSandbox,
		debugMaximumAccess:     *debugMaximumAccess,
		browserCDPControl:      *browserCDPControl,
		fullCDPDebug:           *fullCDPDebug,
		sessionMessages:        *sessionMessages,
		sessionSteeringControl: *sessionSteeringControl,
		runLifecycle:           *runLifecycle,
		runExecution:           *runExecution,
		planDeliveryControl:    *planDeliveryControl,
		approvalControl:        *approvalControl,
		commandProposalControl: *commandProposalControl,
		hostCommandProposals:   *hostCommandProposals,
		modelControl:           *modelControl,
		providerCredentials:    *providerCredentials,
		fileEditReview:         *fileEditReview,
		fileEditProposals:      *fileEditProposals,
		runWakeControl:         *runWakeControl,
		fileEditApply:          *fileEditApply,
		runWakeExecution:       *runWakeExecution,
		runWakeWorker:          *runWakeWorker,
		scheduledJobControl:    *scheduledJobControl,
		scheduledJobWorker:     *scheduledJobWorker,
		skillInstallation:      *skillInstallation,
		evidenceAttachment:     *evidenceAttachment,
		verificationEvidence:   *verificationEvidence,
		embeddedAnalyzer:       *embeddedAnalyzer,
		batchDeliveryControl:   *batchDeliveryControl,
		batchValidation:        *batchValidation,
		uiEvidence:             *uiEvidence,
		userTerminal:           *userTerminal,
		dockerExecution:        *dockerExecution,
		codeIntelConfig:        strings.TrimSpace(*codeIntelConfig),
		gitAdvanced:            *gitAdvanced,
		githubReview:           *githubReview,
		gitWorktreeRoot:        strings.TrimSpace(*gitWorktreeRoot),
		version:                *version}, nil
}

func runDesktop(config desktopOptions) error {
	if err := checkDesktopPrerequisites(); err != nil {
		return err
	}
	var localReadiness *sandbox.LocalReadiness
	if config.workspaceSandbox {
		backend, err := sandbox.NewPlatformLocalBackend()
		if err != nil {
			return err
		}
		defer backend.Close()
		readiness, err := backend.Readiness(context.Background(),
			sandbox.LocalRuntimeCapabilities{Enabled: true})
		if err != nil {
			return err
		}
		localReadiness = &readiness
	}
	bundle, err := webui.LoadEmbeddedFS(webassets.Files, "dist")
	if err != nil {
		return apperror.Wrap(apperror.CodeFailedPrecondition,
			"embedded Desktop UI validation failed", err)
	}
	readToken, err := httpapi.GenerateAccessToken()
	if err != nil {
		return err
	}
	controlToken := ""
	// Docker product control uses the same short-lived Desktop control token;
	// it never receives an independent renderer authority channel.
	if config.profileControl || config.permissionControl || config.browserCDPControl ||
		config.runCreation || config.sessionMessages || config.sessionSteeringControl ||
		config.runLifecycle || config.runExecution || config.planDeliveryControl ||
		config.approvalControl || config.modelControl || config.commandProposalControl ||
		config.hostCommandProposals || config.providerCredentials ||
		config.fileEditReview || config.fileEditProposals || config.runWakeControl ||
		config.fileEditApply || config.runWakeExecution || config.runWakeWorker ||
		config.scheduledJobControl || config.scheduledJobWorker ||
		config.skillInstallation || config.evidenceAttachment ||
		config.verificationEvidence || config.embeddedAnalyzer || config.userTerminal ||
		config.dockerExecution || config.batchDeliveryControl || config.batchValidation ||
		config.uiEvidence || config.gitAdvanced || config.githubReview {
		controlToken, err = httpapi.GenerateAccessToken()
		if err != nil {
			return err
		}
	}

	homePath := app.DefaultHome()
	databasePath := filepath.Join(homePath, "cyberagent.db")
	controlPlane, err := desktop.OpenControlPlane(desktop.ControlPlaneConfig{
		DatabasePath: databasePath, HomePath: homePath, ReadToken: readToken,
		ControlToken:      controlToken,
		RunControlEnabled: config.profileControl, RunCreationEnabled: config.runCreation,
		ExecutionPermissionControlEnabled: config.permissionControl,
		ExecutionPermissionCapabilities: domain.ExecutionPermissionRuntimeCapabilities{
			WorkspaceSandboxEnabled:   localReadiness != nil && localReadiness.Ready,
			OperatorApprovalEnabled:   config.permissionControl,
			DangerFullAccessEnabled:   config.dangerFullAccess,
			DebugMaximumAccessEnabled: config.debugMaximumAccess,
		},
		LocalSandboxReadiness:              localReadiness,
		BrowserCDPPermissionControlEnabled: config.browserCDPControl,
		BrowserCDPPermissionCapabilities: domain.BrowserCDPPermissionRuntimeCapabilities{
			ControlEnabled:   config.browserCDPControl,
			FullDebugEnabled: config.fullCDPDebug,
		},
		SessionMessageEnabled:                   config.sessionMessages,
		SessionSteeringControlEnabled:           config.sessionSteeringControl,
		RunLifecycleEnabled:                     config.runLifecycle,
		RunExecutionEnabled:                     config.runExecution,
		PlanDeliveryControlEnabled:              config.planDeliveryControl,
		ApprovalControlEnabled:                  config.approvalControl,
		ControlledCommandProposalControlEnabled: config.commandProposalControl,
		HostCommandProposalControlEnabled:       config.hostCommandProposals,
		ModelControlEnabled:                     config.modelControl,
		ProviderCredentialEnabled:               config.providerCredentials,
		FileEditReviewEnabled:                   config.fileEditReview,
		FileEditProposalEnabled:                 config.fileEditProposals,
		RunWakeControlEnabled:                   config.runWakeControl,
		FileEditApplyEnabled:                    config.fileEditApply,
		RunWakeExecutionEnabled:                 config.runWakeExecution,
		RunWakeWorkerEnabled:                    config.runWakeWorker,
		ScheduledJobControlEnabled:              config.scheduledJobControl,
		ScheduledJobWorkerEnabled:               config.scheduledJobWorker,
		SkillInstallationEnabled:                config.skillInstallation,
		EvidenceAttachmentEnabled:               config.evidenceAttachment,
		VerificationEvidenceEnabled:             config.verificationEvidence,
		EmbeddedAnalyzerExecutionEnabled:        config.embeddedAnalyzer,
		BatchDeliveryControlEnabled:             config.batchDeliveryControl,
		BatchDeliveryHostValidationEnabled:      config.batchValidation,
		UIEvidenceControlEnabled:                config.uiEvidence,
		BrowserRuntimeCapabilities: browserruntime.ProductionRuntimeCapabilities{
			SafeWebStartEnabled: true, DisposableProfileEnabled: true,
			NetworkContainmentEnabled: true, RestrictedCDPEnabled: true,
		},
		UserTerminalEnabled:        config.userTerminal,
		DockerExecutionEnabled:     config.dockerExecution,
		CodeIntelConfigPath:        config.codeIntelConfig,
		GitAdvancedControlEnabled:  config.gitAdvanced,
		GitHubReviewControlEnabled: config.githubReview,
		GitManagedWorktreeRoot:     config.gitWorktreeRoot,
		AppVersion:                 app.Version, UIHandler: bundle,
		OnWakeWorkerError: func(runErr error) {
			fmt.Fprintln(os.Stderr, "wake-worker:", runErr)
		},
		OnScheduledJobWorkerError: func(runErr error) {
			fmt.Fprintln(os.Stderr, "scheduled-job-worker:", runErr)
		},
	})
	if err != nil {
		return apperror.Wrap(apperror.CodeFailedPrecondition,
			"desktop data store validation failed", err)
	}
	defer controlPlane.Close()
	dockerExecutionEnabled, err := controlPlane.DockerExecutionEnabled()
	if err != nil {
		return apperror.Wrap(apperror.CodeFailedPrecondition,
			"desktop Docker capability projection failed", err)
	}
	if err := controlPlane.StartWakeWorker(context.Background()); err != nil {
		return apperror.Wrap(apperror.CodeFailedPrecondition,
			"desktop wake worker could not start", err)
	}
	if err := controlPlane.StartTerminalBoundaryMonitor(context.Background()); err != nil {
		return apperror.Wrap(apperror.CodeFailedPrecondition,
			"desktop terminal boundary monitor could not start", err)
	}

	lifecycle := desktop.NewLifecycle(wailsWindowRestorer{})
	selector, preview := desktop.NewSkillPackagePreviewBoundary()
	bridge, err := desktop.NewDesktopBridge(desktop.DesktopBridgeConfig{
		ContextProvider: lifecycle.Context, FilePicker: nativeSkillPackagePicker{},
		ReadToken: readToken, ControlToken: controlToken, APIVersion: httpapi.Version,
		RunControlEnabled: config.profileControl, RunCreationEnabled: config.runCreation,
		ExecutionPermissionControlEnabled:       config.permissionControl,
		WorkspaceSandboxEnabled:                 localReadiness != nil && localReadiness.Ready,
		BrowserCDPPermissionControlEnabled:      config.browserCDPControl,
		FullCDPDebugEnabled:                     config.fullCDPDebug,
		OperatorApprovalEnabled:                 config.permissionControl,
		DangerFullAccessEnabled:                 config.dangerFullAccess,
		DebugMaximumAccessEnabled:               config.debugMaximumAccess,
		SessionMessageEnabled:                   config.sessionMessages,
		SessionSteeringControlEnabled:           config.sessionSteeringControl,
		RunLifecycleEnabled:                     config.runLifecycle,
		RunExecutionEnabled:                     config.runExecution,
		PlanDeliveryControlEnabled:              config.planDeliveryControl,
		ApprovalControlEnabled:                  config.approvalControl,
		ControlledCommandProposalControlEnabled: config.commandProposalControl,
		HostCommandProposalControlEnabled:       config.hostCommandProposals,
		ModelControlEnabled:                     config.modelControl,
		ProviderCredentialEnabled:               config.providerCredentials,
		FileEditReviewEnabled:                   config.fileEditReview,
		FileEditProposalEnabled:                 config.fileEditProposals,
		RunWakeControlEnabled:                   config.runWakeControl,
		FileEditApplyEnabled:                    config.fileEditApply,
		RunWakeExecutionEnabled:                 config.runWakeExecution,
		RunWakeWorkerEnabled:                    config.runWakeWorker,
		ScheduledJobControlEnabled:              config.scheduledJobControl,
		ScheduledJobWorkerEnabled:               config.scheduledJobWorker,
		SkillInstallationEnabled:                config.skillInstallation,
		EvidenceAttachmentEnabled:               config.evidenceAttachment,
		VerificationEvidenceEnabled:             config.verificationEvidence,
		EmbeddedAnalyzerExecutionEnabled:        config.embeddedAnalyzer,
		BatchDeliveryControlEnabled:             config.batchDeliveryControl,
		BatchDeliveryHostValidationEnabled:      config.batchValidation,
		UIEvidenceControlEnabled:                config.uiEvidence,
		UserTerminalEnabled:                     config.userTerminal,
		DockerExecutionEnabled:                  dockerExecutionEnabled,
		CodeIntelEnabled:                        controlPlane.CodeIntelEnabled(),
		GitAdvancedControlEnabled:               config.gitAdvanced,
		GitHubReviewControlEnabled:              config.githubReview,
		AppVersion:                              app.Version, UIDigest: bundle.Digest(), Selector: selector,
		PreviewBridge: preview, SkillInstaller: controlPlane.SkillInstaller(),
		WorkspaceResolver: controlPlane, WorkspaceLauncher: newNativeWorkspaceLauncher(),
		WorkspaceDirectoryPicker:          nativeWorkspaceDirectoryPicker{},
		WorkspaceRegistrar:                controlPlane,
		UserTerminalController:            controlPlane.UserTerminalController(),
		DebugTerminalAgentInputController: controlPlane.DebugTerminalAgentInputController(),
	})
	if err != nil {
		return err
	}

	appOptions := &options.App{
		Title: app.Name, Width: defaultDesktopWidth, Height: defaultDesktopHeight,
		MinWidth: 320, MinHeight: 320,
		Frameless:        true,
		WindowStartState: options.Normal,
		BackgroundColour: options.NewRGBA(0, 0, 0, 0),
		AssetServer: &assetserver.Options{
			Handler: inProcessAPIHandler{next: controlPlane.Handler()},
		},
		OnStartup: func(ctx context.Context) {
			lifecycle.Start(ctx)
			if screens, screenErr := runtime.ScreenGetAll(ctx); screenErr == nil &&
				shouldMaximiseDesktopWindow(screens) {
				runtime.WindowMaximise(ctx)
			}
		},
		OnShutdown: func(context.Context) {
			lifecycle.Stop()
		},
		Bind:                     []interface{}{bridge},
		EnableDefaultContextMenu: false,
		// Keep OS anti-phishing cloud submission disabled for a local-first app.
		EnableFraudulentWebsiteDetection: false,
		BindingsAllowedOrigins:           "",
		DragAndDrop: &options.DragAndDrop{
			EnableFileDrop: false, DisableWebViewDrop: true,
		},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               desktopSingleInstanceID,
			OnSecondInstanceLaunch: secondInstanceHandler(lifecycle),
		},
		Debug: options.Debug{OpenInspectorOnStartup: false},
		ErrorFormatter: func(err error) any {
			normalized := apperror.Normalize(err)
			return desktopBindingError{Code: string(apperror.CodeOf(normalized)), Message: normalized.Error()}
		},
	}
	applyDesktopPlatformOptions(appOptions)
	return wails.Run(appOptions)
}
