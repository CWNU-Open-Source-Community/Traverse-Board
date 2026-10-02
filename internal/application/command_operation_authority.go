package application

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/commandruntimeadapter"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/executionauth"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/toolcontract"
	"cyberagent-workbench/internal/toolgateway"
)

// This bridge moves actual process effects through the same authorizer as
// file edits. Old durable permissions remain compatibility inputs during C3;
// their existing gates are re-read, not translated into newly issued grants.
func (s *CommandRuntimeService) authorizedCommandStart(scope toolgateway.CommandRuntimeContext,
	bindings commandRuntimeBindings, operationKey string, spec runner.CommandRuntimeResolvedSpec,
) (runner.CommandRuntimeStartRequest, error) {
	runnerScope := s.runnerScope(scope, bindings, operationKey)
	_, operationID := runner.CommandRuntimeOperationIdentity(scope.RunID, operationKey)
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return runner.CommandRuntimeStartRequest{}, err
	}
	prepare := func(actual runner.CommandRuntimeResolvedSpec) (toolcontract.Operation, error) {
		return commandOperation(key, operationID, s.adapter, struct {
			Scope runner.CommandRuntimeScope
			Spec  runner.CommandRuntimeResolvedSpec
		}{runnerScope, actual}, actual.Spec.Network,
			[]toolcontract.Target{{Kind: "process", Locator: actual.ExecutablePath},
				{Kind: "directory", Locator: actual.AbsoluteDirectory}})
	}
	operation, err := prepare(spec)
	if err != nil {
		return runner.CommandRuntimeStartRequest{}, err
	}
	check, err := s.commandOperationCheck(scope, bindings, operation, spec.Spec.Network, "")
	if err != nil {
		return runner.CommandRuntimeStartRequest{}, err
	}
	return runner.CommandRuntimeStartRequest{Scope: runnerScope, Spec: spec,
		DispatchCheck: func(ctx context.Context, actual runner.CommandRuntimeResolvedSpec) error {
			prepared, err := prepare(actual)
			if err != nil {
				return err
			}
			return check(ctx, prepared)
		}}, nil
}

func (s *CommandRuntimeService) commandStdinDispatchCheck(scope toolgateway.CommandRuntimeContext,
	bindings commandRuntimeBindings, input toolgateway.CommandRuntimeInput,
) (func(context.Context, string, []byte, bool) error, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	_, operationID := runner.CommandRuntimeOperationIdentity(scope.RunID, scope.OperationKey)
	prepare := func(jobID string, data []byte, closeAfter bool) (toolcontract.Operation, error) {
		return commandOperation(key, operationID, s.adapter, struct {
			JobID string
			Data  []byte
			Close bool
		}{jobID, data, closeAfter}, runner.CommandRuntimeNetworkDisabled,
			[]toolcontract.Target{{Kind: "process", Locator: jobID}})
	}
	operation, err := prepare(input.JobID, []byte(*input.Stdin), *input.CloseStdin)
	if err != nil {
		return nil, err
	}
	check, err := s.commandOperationCheck(scope, bindings, operation,
		runner.CommandRuntimeNetworkDisabled, input.JobID)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, jobID string, data []byte, closeAfter bool) error {
		actual, err := prepare(jobID, data, closeAfter)
		if err != nil {
			return err
		}
		return check(ctx, actual)
	}, nil
}

func commandOperation(key []byte, id string, adapter commandruntimeadapter.Identity,
	input any, network runner.CommandRuntimeNetwork, targets []toolcontract.Target,
) (toolcontract.Operation, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return toolcontract.Operation{}, err
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("command-operation-input\x00"))
	_, _ = mac.Write(raw)
	effects := []toolcontract.Effect{toolcontract.EffectProcess, toolcontract.EffectUnknown}
	if adapter.Kind == commandruntimeadapter.KindSandboxedWorkspace &&
		adapter.IsolationGrade == commandruntimeadapter.IsolationWorkspaceSandbox &&
		adapter.NetworkPolicy == commandruntimeadapter.NetworkDenied &&
		adapter.CredentialPolicy == commandruntimeadapter.CredentialsNone &&
		network == runner.CommandRuntimeNetworkDisabled {
		effects = []toolcontract.Effect{toolcontract.EffectProcess, toolcontract.EffectWorkspaceRead,
			toolcontract.EffectReversibleWrite}
	}
	// A host cwd, network intent, or executable name is not isolation evidence.
	operation := toolcontract.Operation{ID: id, Kind: toolcontract.OperationProcess,
		ToolID: "command_runtime", Component: toolcontract.ComponentRef{PackageID: "builtin", ComponentID: "command_runtime"},
		AdapterID: adapter.BackendIdentity, AdapterRevision: adapter.Generation,
		InputFingerprint: hex.EncodeToString(mac.Sum(nil)), Targets: targets, Effects: effects}
	return operation, operation.Validate()
}

func (s *CommandRuntimeService) commandOperationCheck(scope toolgateway.CommandRuntimeContext,
	bindings commandRuntimeBindings, operation toolcontract.Operation, network runner.CommandRuntimeNetwork, jobID string,
) (func(context.Context, toolcontract.Operation) error, error) {
	actor := scope.AgentID
	if actor == "" {
		actor = bindings.root.ID
	}
	subject := executionauth.SubjectRef{RunID: scope.RunID, ActorID: actor}
	expectedScope := s.runnerScope(scope, bindings, scope.OperationKey)
	bindingFingerprint := func(value runner.CommandRuntimeScope) (string, error) {
		raw, err := json.Marshal(value)
		if err != nil {
			return "", err
		}
		sum := sha256.Sum256(raw)
		return hex.EncodeToString(sum[:]), nil
	}
	expectedBinding, err := bindingFingerprint(expectedScope)
	if err != nil {
		return nil, err
	}
	authorizer := executionauth.NewPolicyAuthorizer(func(ctx context.Context, actualSubject executionauth.SubjectRef,
		_ toolcontract.Operation, approvalRef string,
	) (executionauth.OperationAuthority, error) {
		if actualSubject != subject || approvalRef != "" || !s.commandRuntimeAdapterCurrent() {
			return executionauth.OperationAuthority{}, apperror.New(apperror.CodePolicyDenied, "command operation host authority changed")
		}
		current, err := s.loadAuthorizedBindings(ctx, scope, network == runner.CommandRuntimeNetworkHost)
		if err != nil {
			return executionauth.OperationAuthority{}, err
		}
		if scope.RequestedBy == "run_supervisor" && (scope.AgentID != current.root.ID ||
			current.root.Status != domain.AgentRunning || current.root.ActiveAttemptID != scope.AgentAttemptID) {
			return executionauth.OperationAuthority{}, apperror.New(apperror.CodeConflict,
				"command operation Agent attempt changed before dispatch")
		}
		binding, err := bindingFingerprint(s.runnerScope(scope, current, scope.OperationKey))
		if err != nil || binding != expectedBinding {
			return executionauth.OperationAuthority{}, apperror.New(apperror.CodeConflict, "command operation scope changed before dispatch")
		}
		if jobID != "" {
			if _, err := s.authorizeActiveJob(ctx, jobID, current); err != nil {
				return executionauth.OperationAuthority{}, err
			}
		}
		projection, err := domain.ProjectLegacyExecutionPermission(current.permission)
		if err != nil {
			return executionauth.OperationAuthority{}, err
		}
		return executionauth.OperationAuthority{Mode: projection.Mode, BindingFingerprint: binding,
			RuntimeAvailable: true, FullActivated: projection.Mode == domain.ExecutionApprovalFull,
			EffectsVerified: s.adapter.Kind == commandruntimeadapter.KindSandboxedWorkspace}, nil
	})
	var mu sync.Mutex
	var decision executionauth.Decision
	started := false
	denied := false
	return func(ctx context.Context, actual toolcontract.Operation) (err error) {
		mu.Lock()
		defer mu.Unlock()
		if denied {
			return errors.New("command operation dispatch was already denied")
		}
		defer func() { denied = err != nil }()
		if !started {
			started = true
			var err error
			decision, err = authorizer.Authorize(ctx, subject, operation, "")
			if err != nil {
				return err
			}
			if decision.Outcome != "allow" || decision.Validate() != nil {
				return apperror.New(apperror.CodePolicyDenied, "command operation was not authorized")
			}
			fingerprint, err := toolcontract.FingerprintOperation(actual)
			if err != nil {
				return err
			}
			return decision.BeforeDispatch(ctx, fingerprint)
		}
		if decision.AuthorizationRef == "" {
			return errors.New("command operation dispatch was already denied")
		}
		return authorizer.Recheck(ctx, subject, actual, "", decision.AuthorizationRef)
	}, nil
}
