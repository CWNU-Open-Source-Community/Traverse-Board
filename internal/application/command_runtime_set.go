package application

import (
	"context"
	"errors"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/sandbox"
)

type CommandRuntimeSetStore interface {
	CommandRuntimeStore
	LocalSandboxCommandRuntimeStore
}

// Hosts select their startup gates and pass the same capabilities to every
// service. The set borrows the store, Drydock and sandbox backends.
type CommandRuntimeSetOptions struct {
	HostEnabled               bool
	Capabilities              domain.ExecutionPermissionRuntimeCapabilities
	Drydocks                  *RunWorktreeService
	LocalBackend              sandbox.LocalBackend
	LocalReadiness            *sandbox.LocalReadiness
	StandardCodeDockerRuntime *StandardCodeDockerService
	SBXBackend                *sandbox.SBXBackend
	StartupShutdownTimeout    time.Duration
}

type CommandRuntimeSet struct {
	Runtime  CommandRuntimeRuntime
	managers []*runner.CommandRuntimeManager
}

// OpenCommandRuntimeSet takes ownership of recoveryManager, including when
// startup fails. It starts no workers or jobs and does not activate authority.
func OpenCommandRuntimeSet(ctx context.Context, store CommandRuntimeSetStore,
	recoveryManager *runner.CommandRuntimeManager, options CommandRuntimeSetOptions,
) (_ *CommandRuntimeSet, resultErr error) {
	set := &CommandRuntimeSet{managers: []*runner.CommandRuntimeManager{recoveryManager}}
	defer func() {
		if resultErr != nil {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), options.StartupShutdownTimeout)
			defer cancel()
			_ = set.Shutdown(shutdownCtx)
		}
	}()
	if _, err := recoveryManager.ReconcileStartup(ctx); err != nil {
		return nil, apperror.Wrap(apperror.CodeUnavailable,
			"command runtime startup reconciliation failed", err)
	}
	adapters := make([]*CommandRuntimeService, 0, 4)
	if options.HostEnabled {
		service, err := NewCommandRuntimeService(store, recoveryManager, options.Capabilities)
		if err != nil {
			return nil, err
		}
		adapters = append(adapters, service)
	}
	if options.LocalBackend != nil && options.LocalReadiness != nil && options.Drydocks != nil {
		executor, err := NewLocalSandboxCommandRuntimeExecutor(store,
			options.LocalBackend, *options.LocalReadiness)
		if err != nil {
			return nil, err
		}
		manager, err := runner.NewSandboxCommandRuntimeManager(store, executor,
			idgen.New("command-runtime-local-owner"))
		if err != nil {
			return nil, err
		}
		set.managers = append(set.managers, manager)
		if _, err := manager.ReconcileStartup(ctx); err != nil {
			return nil, apperror.Wrap(apperror.CodeUnavailable,
				"Local Command Runtime startup reconciliation failed", err)
		}
		service, err := NewSandboxedCommandRuntimeService(store, manager, executor,
			options.Capabilities, options.Drydocks)
		if err != nil {
			return nil, err
		}
		adapters = append(adapters, service)
	}
	if options.StandardCodeDockerRuntime != nil && options.Drydocks != nil {
		executor, err := NewDockerSandboxCommandRuntimeExecutor(options.StandardCodeDockerRuntime)
		if err != nil {
			return nil, err
		}
		manager, err := runner.NewSandboxCommandRuntimeManager(store, executor,
			idgen.New("command-runtime-docker-owner"))
		if err != nil {
			return nil, err
		}
		set.managers = append(set.managers, manager)
		if _, err := manager.ReconcileStartup(ctx); err != nil {
			return nil, apperror.Wrap(apperror.CodeUnavailable,
				"Docker Command Runtime startup reconciliation failed", err)
		}
		service, err := NewSandboxedCommandRuntimeService(store, manager, executor,
			options.Capabilities, options.Drydocks)
		if err != nil {
			return nil, err
		}
		adapters = append(adapters, service)
	}
	if options.SBXBackend != nil && options.Drydocks != nil {
		if err := options.SBXBackend.RecoverStartup(ctx); err != nil {
			return nil, apperror.Wrap(apperror.CodeUnavailable, "Docker Sandboxes owned execution recovery needs attention", err)
		}
		executor, err := NewSBXCommandRuntimeExecutor(store, options.SBXBackend)
		if err != nil {
			return nil, err
		}
		manager, err := runner.NewSandboxCommandRuntimeManager(store, executor, idgen.New("command-runtime-sbx-owner"))
		if err != nil {
			return nil, err
		}
		set.managers = append(set.managers, manager)
		if _, err := manager.ReconcileStartup(ctx); err != nil {
			return nil, apperror.Wrap(apperror.CodeUnavailable, "Docker Sandboxes Command Runtime startup reconciliation failed", err)
		}
		service, err := NewSandboxedCommandRuntimeService(store, manager, executor, options.Capabilities, options.Drydocks)
		if err != nil {
			return nil, err
		}
		adapters = append(adapters, service)
	}
	if len(adapters) == 1 {
		set.Runtime = adapters[0]
	} else if len(adapters) > 1 {
		var err error
		set.Runtime, err = NewCommandRuntimeMultiplexer(adapters...)
		if err != nil {
			return nil, err
		}
	}
	return set, nil
}

func (s *CommandRuntimeSet) Shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	var result error
	for _, manager := range s.managers {
		result = errors.Join(result, manager.Shutdown(ctx))
	}
	return result
}
