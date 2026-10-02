package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/executionauth"
	"cyberagent-workbench/internal/mcp"
	"cyberagent-workbench/internal/toolcontract"
	"cyberagent-workbench/internal/toolgateway"
)

// Only the host supplies this resolver. MCP adapters describe actual operations;
// neither a tool annotation nor a plugin declaration supplies execution authority.
func (e *MCPClientToolExecutor) operationAuthorizer(ctx context.Context, scope toolgateway.MCPExecutionScope, serverID string) (executionauth.SubjectRef, *executionauth.PolicyAuthorizer, func(context.Context) error, error) {
	store, ok := e.store.(interface {
		GetRootAgent(context.Context, string) (domain.AgentNode, bool, error)
		GetRunMode(context.Context, string) (domain.RunModeSnapshot, error)
	})
	if !ok {
		return executionauth.SubjectRef{}, nil, nil, errors.New("MCP host actor/mode reader unavailable")
	}
	root, found, err := store.GetRootAgent(ctx, scope.RunID)
	if err != nil || !found {
		return executionauth.SubjectRef{}, nil, nil, errors.New("MCP root actor is unavailable")
	}
	subject := executionauth.SubjectRef{RunID: scope.RunID, ActorID: root.ID}
	component := toolcontract.ComponentRef{PackageID: mcp.ClientProtocolVersion, ComponentID: serverID}
	readBinding := func(ctx context.Context) (string, domain.ExecutionApprovalMode, error) {
		permission, err := e.currentExecutionScope(ctx, scope)
		if err != nil {
			return "", "", err
		}
		current, found, err := store.GetRootAgent(ctx, scope.RunID)
		if err != nil || !found || current.ID != root.ID || current.Role != scope.Role || current.ParentID != "" ||
			(scope.AgentID != "" && (scope.AgentID != current.ID || scope.AgentAttemptID != current.ActiveAttemptID || current.Status != domain.AgentRunning)) {
			return "", "", apperror.New(apperror.CodeConflict, "MCP root actor or attempt changed")
		}
		mode, err := store.GetRunMode(ctx, scope.RunID)
		if err != nil {
			return "", "", err
		}
		if mode.Surface != scope.Surface || mode.Phase != scope.Phase || mode.MissionID != scope.MissionID {
			return "", "", apperror.New(apperror.CodeConflict, "MCP execution mode changed")
		}
		raw, err := json.Marshal(struct {
			Scope           toolgateway.MCPExecutionScope
			Mode            domain.RunModeSnapshot
			RootID, Attempt string
			Status          domain.AgentStatus
		}{scope, mode, current.ID, current.ActiveAttemptID, current.Status})
		if err != nil {
			return "", "", err
		}
		sum := sha256.Sum256(raw)
		projection, err := domain.ExecutionPermissionApproval(permission)
		return hex.EncodeToString(sum[:]), projection.Mode, err
	}
	expected, _, err := readBinding(ctx)
	if err != nil {
		return subject, nil, nil, err
	}
	// Capture the initial host binding before resolving component metadata.
	// Revocation during that lookup is then observed by Manager's actual guard,
	// preserving its not-dispatched receipt and existing call audit boundary.
	if records, ok := e.store.(interface {
		GetMCPClientServer(context.Context, string) (mcp.ServerRecord, error)
	}); ok {
		record, err := records.GetMCPClientServer(ctx, serverID)
		if err != nil && apperror.CodeOf(err) != apperror.CodeNotFound {
			return subject, nil, nil, err
		}
		if err == nil && record.Descriptor.NativeSource != nil {
			source := *record.Descriptor.NativeSource
			if source.Validate() != nil || source.Surface != string(scope.Surface) {
				return subject, nil, nil, apperror.New(apperror.CodePolicyDenied, "native MCP surface does not match its host subject")
			}
			component = source.Component
		}
	}
	recheck := func(ctx context.Context) error {
		binding, _, err := readBinding(ctx)
		if err == nil && binding != expected {
			err = apperror.New(apperror.CodeConflict, "MCP operation authority changed")
		}
		return err
	}
	authorizer := executionauth.NewPolicyAuthorizer(func(ctx context.Context, actual executionauth.SubjectRef, operation toolcontract.Operation, approvalRef string) (executionauth.OperationAuthority, error) {
		if actual != subject || approvalRef != "" || operation.AdapterID != mcp.RuntimeAdapterID || operation.AdapterRevision != mcp.RuntimeAdapterRevision ||
			operation.Component != component ||
			(operation.Kind != toolcontract.OperationConnect && operation.Kind != toolcontract.OperationDiscovery && operation.Kind != toolcontract.OperationToolCall) {
			return executionauth.OperationAuthority{}, apperror.New(apperror.CodePolicyDenied, "MCP host operation does not match its runtime")
		}
		binding, mode, err := readBinding(ctx)
		if err != nil {
			return executionauth.OperationAuthority{}, err
		}
		if binding != expected {
			return executionauth.OperationAuthority{}, apperror.New(apperror.CodeConflict, "MCP operation authority changed")
		}
		// Current Full activation was checked above. Server claims never turn
		// unknown remote/host effects into proven safe auto operations.
		return executionauth.OperationAuthority{Mode: mode, BindingFingerprint: binding, RuntimeAvailable: true,
			FullActivated: mode == domain.ExecutionApprovalFull, EffectsVerified: false}, nil
	})
	return subject, authorizer, recheck, nil
}
