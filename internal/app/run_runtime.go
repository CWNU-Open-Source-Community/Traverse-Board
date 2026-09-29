package app

import (
	"context"
	"errors"
	"sync"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runner"
)

func (a *App) runRuntimeDependencies() application.RunRuntimeDependencies {
	d := application.RunRuntimeDependencies{
		ActiveCalls: a.calls, LifecycleHooks: a.newLifecycleHookEngine(),
		CodeIntel: a.codeIntel, WebEvidence: a.newWebEvidenceService(),
		WebFetchAuthorizationScheduler: true,
	}
	if executor := a.newDockerSandboxProposalExecutor(); executor != nil {
		d.DockerSandbox = executor
	}
	if client := a.newMCPClientManager(); client != nil {
		d.MCPClient = client
	}
	return d
}

// A foreground CLI invocation owns its adapters until it returns. The same
// capability value (including its revocation fence) reaches every sibling.
type cliExecutionRuntime struct {
	capabilities domain.ExecutionPermissionRuntimeCapabilities
	commands     *application.CommandRuntimeService
	browser      *application.AgentBrowserService
	manager      *runner.CommandRuntimeManager
	stop         func() error
	closeOnce    sync.Once
	closeErr     error
}

func (a *App) newCLIExecutionRuntime(ctx context.Context,
	capabilities domain.ExecutionPermissionRuntimeCapabilities, withCommands bool,
) (*cliExecutionRuntime, error) {
	if err := capabilities.Validate(); err != nil {
		return nil, apperror.Wrap(apperror.CodeInvalidArgument, "invalid CLI runtime capabilities", err)
	}
	r := &cliExecutionRuntime{capabilities: capabilities, stop: func() error { return nil }}
	if withCommands {
		var err error
		r.manager, r.commands, err = a.newCLICommandRuntimeWithCapabilities(ctx, capabilities)
		if err != nil {
			return nil, err
		}
		r.stop = a.startCLICommandRuntimeReconciler(ctx, r.commands)
	}
	if capabilities.DangerFullAccessEnabled {
		r.browser = application.NewAgentBrowserService(a.store, application.AgentBrowserOptions{
			HomePath: a.home, Capabilities: capabilities, Headless: true,
		})
	}
	return r, nil
}

func (r *cliExecutionRuntime) dependencies(a *App) application.RunRuntimeDependencies {
	d := a.runRuntimeDependencies()
	d.ExecutionCapabilities = r.capabilities
	if r.commands != nil {
		d.CommandRuntime = r.commands
	}
	d.AgentBrowser = r.browser
	return d
}

func (r *cliExecutionRuntime) close() error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		r.closeErr = r.stop()
		if r.browser != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
			r.closeErr = errors.Join(r.closeErr, r.browser.Shutdown(ctx))
			cancel()
		}
		r.closeErr = errors.Join(r.closeErr, shutdownCLICommandRuntime(r.manager))
	})
	return r.closeErr
}
