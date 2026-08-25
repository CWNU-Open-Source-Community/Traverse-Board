package runner

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
)

func TestRiskEscalationScopeIsStableCategorizedAndRejectsSecrets(t *testing.T) {
	request := RiskEscalationScopeRequest{
		Kinds: []RiskEscalationKind{RiskEscalationPolicyDenial,
			RiskEscalationNetwork, RiskEscalationCredential},
		NetworkTargets:  []string{"z.example.test:443", "a.example.test:443"},
		NetworkPurpose:  "send one exact request",
		CredentialKinds: []string{"github_app", "client_certificate"},
		PolicyCode:      "workspace.network_denied",
		PolicyReason:    "the target is outside Workspace Access",
	}
	first, err := NewRiskEscalationScope(request)
	if err != nil {
		t.Fatal(err)
	}
	request.Kinds[0], request.Kinds[2] = request.Kinds[2], request.Kinds[0]
	request.NetworkTargets[0], request.NetworkTargets[1] =
		request.NetworkTargets[1], request.NetworkTargets[0]
	request.CredentialKinds[0], request.CredentialKinds[1] =
		request.CredentialKinds[1], request.CredentialKinds[0]
	second, err := NewRiskEscalationScope(request)
	if err != nil || second.Fingerprint != first.Fingerprint {
		t.Fatalf("normalized scope is unstable: first=%+v second=%+v err=%v",
			first, second, err)
	}
	secretRequest := request
	secretRequest.NetworkTargets = []string{
		"Authorization: Bearer ghp_abcdefghijklmnopqrstuvwxyz1234567890",
	}
	if _, err := NewRiskEscalationScope(secretRequest); err == nil {
		t.Fatal("secret-like risk metadata was accepted")
	}
}

func TestRiskEscalationScopeBuildsEachRequiredStableCategory(t *testing.T) {
	hostPath := filepath.Join(t.TempDir(), "outside-cache")
	tests := []struct {
		name    string
		kind    RiskEscalationKind
		request RiskEscalationScopeRequest
	}{
		{name: "network", kind: RiskEscalationNetwork,
			request: RiskEscalationScopeRequest{Kinds: []RiskEscalationKind{RiskEscalationNetwork},
				NetworkTargets: []string{"proxy.example.test:443"},
				NetworkPurpose: "download one declared dependency"}},
		{name: "credential kind", kind: RiskEscalationCredential,
			request: RiskEscalationScopeRequest{Kinds: []RiskEscalationKind{RiskEscalationCredential},
				CredentialKinds: []string{"private_registry_token"}}},
		{name: "host path", kind: RiskEscalationHostPath,
			request: RiskEscalationScopeRequest{Kinds: []RiskEscalationKind{RiskEscalationHostPath},
				HostPaths: []string{hostPath}}},
		{name: "policy refusal", kind: RiskEscalationPolicyDenial,
			request: RiskEscalationScopeRequest{Kinds: []RiskEscalationKind{RiskEscalationPolicyDenial},
				PolicyCode:   "workspace.network_denied",
				PolicyReason: "the requested operation exceeds Workspace Access"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			first, err := NewRiskEscalationScope(test.request)
			if err != nil {
				t.Fatal(err)
			}
			second, err := NewRiskEscalationScope(test.request)
			if err != nil || first.Fingerprint != second.Fingerprint ||
				len(first.Kinds) != 1 || first.Kinds[0] != test.kind {
				t.Fatalf("category proposal is unstable: first=%+v second=%+v err=%v",
					first, second, err)
			}
		})
	}
}

type riskHostExecutionFixture struct {
	hostExecutionFixture
	proposal      RiskEscalationProposal
	authorization RiskEscalationAuthorization
}

func newRiskHostExecutionFixture(t *testing.T) riskHostExecutionFixture {
	t.Helper()
	base := newHostExecutionFixture(t)
	at := base.intent.CreatedAt.Add(time.Second)
	permission, err := base.permission.Next("permission-risk-workspace",
		domain.RunExecutionPermissionWorkspaceAccess, true, "test_operator",
		"Workspace Access escalation", at)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := NewRiskEscalationScope(RiskEscalationScopeRequest{
		Kinds:          []RiskEscalationKind{RiskEscalationNetwork},
		NetworkTargets: []string{"api.example.test:443"},
		NetworkPurpose: "send one exact verification request",
	})
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := NewRiskEscalationProposal(RiskEscalationProposalRequest{
		ID: "risk-execution-proposal", RunID: base.intent.RunID,
		MissionID: base.intent.MissionID, SessionID: base.intent.SessionID,
		WorkspaceID: base.intent.WorkspaceID, RootAgentID: "root-risk-execution",
		SupervisorTurn: 1, SupervisorToolCallID: "tool-risk-execution",
		ToolInvocationID: "invocation-risk-execution",
		ModeSnapshotID:   "mode-risk-execution", ModeRevision: 1,
		InteractionSnapshotID:      base.interaction.ID,
		InteractionRevision:        base.interaction.Revision,
		ExecutionProfileSnapshotID: base.profile.ID,
		ExecutionProfileRevision:   base.profile.Revision,
		Permission:                 permission, WorkspaceRootFingerprint: strings.Repeat("a", 64),
		CapabilityGeneration: strings.Repeat("b", 64), Spec: base.intent.Spec,
		Scope: scope, RequestedBy: "run_supervisor", CreatedAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := NewRiskEscalationAuthorization(proposal,
		"approval-risk-execution", 2, strings.Repeat("c", 64), "", 0, "",
		"test_operator", at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	intent, err := NewRiskEscalationHostExecutionIntent(proposal, authorization,
		strings.Repeat("d", 64), at.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	base.intent = intent
	base.permission = permission
	return riskHostExecutionFixture{hostExecutionFixture: base,
		proposal: proposal, authorization: authorization}
}

func riskHostExecutionRequest(fixture riskHostExecutionFixture) HostExecutionRequest {
	request := hostExecutionRequestFromFixture(fixture.hostExecutionFixture)
	request.Runtime = domain.ExecutionPermissionRuntimeCapabilities{
		WorkspaceSandboxEnabled: true, OperatorApprovalEnabled: true,
	}
	request.Escalation = &fixture.authorization
	return request
}

func TestHostExecutorRequiresExactInProcessRiskEscalationAuthorization(t *testing.T) {
	fixture := newRiskHostExecutionFixture(t)
	for _, principal := range []string{"model", "skill", "mcp", "repository_content",
		"repo_content", "run_supervisor"} {
		if _, err := NewRiskEscalationAuthorization(fixture.proposal,
			"approval-risk-principal", 2, strings.Repeat("c", 64), "", 0, "",
			principal, fixture.authorization.AuthorizedAt); err == nil {
			t.Fatalf("non-operator principal %q created risk authorization", principal)
		}
	}
	starter := &hostStarterStub{available: true, result: validHostStartResult()}
	executor, err := NewHostExecutor(starter)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), riskHostExecutionRequest(fixture))
	if err != nil || result.PermissionMode != domain.RunExecutionPermissionWorkspaceAccess ||
		result.AuthorizationProposalID != fixture.proposal.ID || starter.started.RequestID == "" {
		t.Fatalf("exact risk escalation result=%+v started=%+v err=%v",
			result, starter.started, err)
	}
	tests := []struct {
		name   string
		mutate func(*HostExecutionRequest)
	}{
		{name: "missing authorization", mutate: func(request *HostExecutionRequest) {
			request.Escalation = nil
		}},
		{name: "process authority missing", mutate: func(request *HostExecutionRequest) {
			request.Runtime.OperatorApprovalEnabled = false
		}},
		{name: "approval binding changed", mutate: func(request *HostExecutionRequest) {
			copy := *request.Escalation
			copy.ApprovalFingerprint = strings.Repeat("e", 64)
			request.Escalation = &copy
		}},
		{name: "model cannot consume", mutate: func(request *HostExecutionRequest) {
			request.RequestedBy = "model"
		}},
		{name: "skill cannot consume", mutate: func(request *HostExecutionRequest) {
			request.RequestedBy = "skill"
		}},
		{name: "mcp cannot consume", mutate: func(request *HostExecutionRequest) {
			request.RequestedBy = "mcp"
		}},
		{name: "repository content cannot consume", mutate: func(request *HostExecutionRequest) {
			request.RequestedBy = "repository_content"
		}},
		{name: "another run cannot consume", mutate: func(request *HostExecutionRequest) {
			request.Intent.RunID = "run-other"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := riskHostExecutionRequest(fixture)
			test.mutate(&request)
			isolatedStarter := &hostStarterStub{available: true,
				result: validHostStartResult()}
			isolated, err := NewHostExecutor(isolatedStarter)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := isolated.Execute(context.Background(), request); err == nil ||
				isolatedStarter.started.RequestID != "" {
				t.Fatalf("unauthorized risk request started: start=%+v err=%v",
					isolatedStarter.started, err)
			}
		})
	}
}
