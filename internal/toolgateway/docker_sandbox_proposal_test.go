package toolgateway

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/toolbudget"
)

const dockerSandboxProposalPayload = `{
  "version":"sandbox_docker_run_proposal.v1",
  "plan_id":"docker-plan-1",
  "manifest":{
    "protocol_version":"sandbox_manifest.v1",
    "backend":"docker",
    "command":{"executable":"/bin/echo","arguments":["ok"],"working_directory":"/workspace"},
    "mounts":[{"source":".","target":"/workspace","access":"read_write"}],
    "network":{"mode":"disabled"},
    "resources":{"cpu_quota_millis":1000,"memory_bytes":33554432,"pids":32,"max_output_bytes":4096},
    "output":{"capture_stdout":true,"capture_stderr":true},
    "timeout_seconds":30,
    "cancellation":{"grace_period_millis":1000}
  }
}`

type dockerSandboxProposalExecutorStub struct {
	mu        sync.Mutex
	calls     int
	lastScope DockerSandboxProposalContext
	lastSpec  DockerSandboxProposalSpec
	result    DockerSandboxProposalResult
}

func (s *dockerSandboxProposalExecutorStub) ProposeDockerSandbox(_ context.Context,
	scope DockerSandboxProposalContext, spec DockerSandboxProposalSpec,
) (DockerSandboxProposalResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.lastScope = scope
	s.lastSpec = spec
	if s.result.ReasonCode == "" {
		return DockerSandboxProposalResult{
			AdmissionID: "docker-admission-1", Allowed: true,
			ReasonCode:      domain.DockerSandboxReasonReady,
			RemediationCode: domain.DockerSandboxRemediationNone,
		}, nil
	}
	return s.result, nil
}

func (s *dockerSandboxProposalExecutorStub) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func validDockerSandboxToolCall(payload string) ToolCall {
	return ToolCall{
		Name: DockerSandboxRunProposeTool, Payload: json.RawMessage(payload),
		OperationKey: "docker-proposal-operation", RunID: "run-1",
		AgentID: "agent-root", SessionID: "session-1",
		WorkspaceID: "workspace-1", RequestedBy: "run_supervisor",
		LeaseID: "lease-1", LeaseGeneration: 1,
	}
}

func TestDockerSandboxProposalDefinitionAndPayloadAreStrict(t *testing.T) {
	definition, found := SupervisorToolDefinition(DockerSandboxRunProposeTool)
	if !found || definition.Class != ClassAgentProposal ||
		definition.Approval != ApprovalAutomatic || !json.Valid(definition.InputSchema) {
		t.Fatalf("invalid Docker Sandbox proposal definition: %#v", definition)
	}
	schema := string(definition.InputSchema)
	for _, required := range []string{
		`"additionalProperties":false`,
		`"const":"sandbox_docker_run_proposal.v1"`,
		`"const":"sandbox_manifest.v1"`,
		`"backend":{"const":"docker"}`,
		`"mode":{"const":"disabled"}`,
	} {
		if !strings.Contains(schema, required) {
			t.Fatalf("Docker Sandbox schema omitted %s: %s", required, schema)
		}
	}
	for _, forbidden := range []string{
		`"environment"`, `"image"`, `"daemon"`, `"docker_flags"`,
		`"bind"`, `"proxy"`,
	} {
		if strings.Contains(schema, forbidden) {
			t.Fatalf("Docker authority field leaked into model schema: %s", forbidden)
		}
	}
	canonical, err := NormalizeSupervisorToolPayload(DockerSandboxRunProposeTool,
		json.RawMessage(dockerSandboxProposalPayload))
	if err != nil || !json.Valid(canonical) ||
		!strings.Contains(string(canonical), `"backend":"docker"`) {
		t.Fatalf("valid Docker Sandbox proposal failed: %s err=%v", canonical, err)
	}

	invalid := []string{
		strings.Replace(dockerSandboxProposalPayload, `"plan_id":"docker-plan-1",`,
			`"plan_id":"docker-plan-1","daemon_endpoint":"tcp://127.0.0.1",`, 1),
		strings.Replace(dockerSandboxProposalPayload, `"backend":"docker",`,
			`"backend":"docker","image":"untrusted:latest",`, 1),
		strings.Replace(dockerSandboxProposalPayload, `"network":{"mode":"disabled"},`,
			`"network":{"mode":"disabled"},"environment":[],`, 1),
		strings.Replace(dockerSandboxProposalPayload, `"network":{"mode":"disabled"}`,
			`"network":{"mode":"disabled","proxy":"http://127.0.0.1"}`, 1),
		strings.Replace(dockerSandboxProposalPayload, `"source":"."`,
			`"source":"/host/workspace"`, 1),
		strings.Replace(dockerSandboxProposalPayload, `"plan_id":"docker-plan-1"`,
			`"plan_id":"docker-plan-1","plan_id":"docker-plan-2"`, 1),
		dockerSandboxProposalPayload + `{}`,
	}
	for _, payload := range invalid {
		if _, err := NormalizeSupervisorToolPayload(DockerSandboxRunProposeTool,
			json.RawMessage(payload)); err == nil {
			t.Fatalf("unsafe Docker Sandbox proposal was accepted: %s", payload)
		}
	}
}

func TestDockerSandboxProposalGatewayIsFencedAdmissionOnly(t *testing.T) {
	store := newTrackedStructuredStore()
	executor := &dockerSandboxProposalExecutorStub{}
	gateway := New(store, policy.NewDefaultChecker()).
		WithDockerSandboxProposalExecutor(executor)
	outcome, err := gateway.Invoke(t.Context(),
		validDockerSandboxToolCall(dockerSandboxProposalPayload))
	if err != nil || outcome.Execution == nil || outcome.Result == nil ||
		outcome.Execution.Backend != "docker_sandbox_admission" ||
		outcome.Execution.Status != StatusCompleted ||
		outcome.Result.Status != StatusCompleted ||
		outcome.Result.Metadata["admission_id"] != "docker-admission-1" ||
		outcome.Result.Metadata["allowed"] != "true" ||
		outcome.Result.Metadata["execution_authorized"] != "false" ||
		store.chargeCount() != 1 || executor.callCount() != 1 {
		t.Fatalf("unexpected Docker Sandbox proposal outcome: %#v err=%v", outcome, err)
	}
	if string(outcome.Call.Payload) != `{"redacted":true}` ||
		strings.Contains(string(outcome.Call.Payload), "/bin/echo") ||
		strings.Contains(string(outcome.Call.Payload), "/workspace") ||
		strings.Contains(string(outcome.Call.Payload), "docker-plan-1") {
		t.Fatalf("Docker Sandbox proposal exposed request details: %#v", outcome.Call)
	}
	executor.mu.Lock()
	defer executor.mu.Unlock()
	if executor.lastScope.RunID != "run-1" ||
		executor.lastScope.WorkspaceID != "workspace-1" ||
		executor.lastScope.SessionID != "session-1" ||
		executor.lastScope.RootAgentID != "agent-root" ||
		executor.lastScope.LeaseID != "lease-1" ||
		executor.lastScope.LeaseGeneration != 1 ||
		executor.lastScope.RequestedBy != "run_supervisor" ||
		executor.lastScope.PolicyDecision.Approval != ApprovalAutomatic {
		t.Fatalf("Docker Sandbox proposal lost its fence: %#v", executor.lastScope)
	}
}

func TestDockerSandboxProposalServiceDenialIsBoundedCompletedMetadata(t *testing.T) {
	store := newTrackedStructuredStore()
	executor := &dockerSandboxProposalExecutorStub{result: DockerSandboxProposalResult{
		Allowed: false, ReasonCode: domain.DockerSandboxReasonPolicyDenied,
		RemediationCode: domain.DockerSandboxRemediationReviewPolicy,
	}}
	outcome, err := New(store, policy.NewDefaultChecker()).
		WithDockerSandboxProposalExecutor(executor).
		Invoke(t.Context(), validDockerSandboxToolCall(dockerSandboxProposalPayload))
	if err != nil || !outcome.Decision.Allowed || outcome.Result == nil ||
		outcome.Result.Status != StatusCompleted || outcome.Execution == nil ||
		outcome.Result.Metadata["allowed"] != "false" ||
		outcome.Result.Metadata["reason"] != domain.DockerSandboxReasonPolicyDenied ||
		outcome.Result.Metadata["remediation"] != domain.DockerSandboxRemediationReviewPolicy ||
		outcome.Result.Metadata["execution_authorized"] != "false" ||
		outcome.Result.Metadata["admission_id"] != "" || len(outcome.Result.Metadata) != 5 {
		t.Fatalf("service denial was not a bounded completed result: %#v err=%v", outcome, err)
	}
}

func TestDockerSandboxProposalPolicyDenialNeverInvokesExecutor(t *testing.T) {
	store := newTrackedStructuredStore()
	executor := &dockerSandboxProposalExecutorStub{}
	payload := strings.Replace(dockerSandboxProposalPayload, "/bin/echo",
		"/usr/bin/masscan", 1)
	outcome, err := New(store, policy.NewDefaultChecker()).
		WithDockerSandboxProposalExecutor(executor).
		Invoke(t.Context(), validDockerSandboxToolCall(payload))
	if err != nil || outcome.Result == nil ||
		outcome.Result.Status != StatusDenied || outcome.Decision.Allowed ||
		executor.callCount() != 0 || store.chargeCount() != 1 {
		t.Fatalf("policy denial reached Docker admission: %#v err=%v", outcome, err)
	}
}

type dockerSandboxBudgetDeniedStore struct {
	*trackedStructuredStore
	mu    sync.Mutex
	calls int
}

func (s *dockerSandboxBudgetDeniedStore) ChargeToolCall(_ context.Context,
	_ toolbudget.ChargeRequest,
) (toolbudget.Usage, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	return toolbudget.Usage{}, errors.New("tool call budget exhausted")
}

func TestDockerSandboxProposalChargesBudgetBeforeExecutor(t *testing.T) {
	store := &dockerSandboxBudgetDeniedStore{
		trackedStructuredStore: newTrackedStructuredStore(),
	}
	executor := &dockerSandboxProposalExecutorStub{}
	_, err := New(store, policy.NewDefaultChecker()).
		WithDockerSandboxProposalExecutor(executor).
		Invoke(t.Context(), validDockerSandboxToolCall(dockerSandboxProposalPayload))
	store.mu.Lock()
	calls := store.calls
	store.mu.Unlock()
	if err == nil || calls != 1 || executor.callCount() != 0 {
		t.Fatalf("budget fence order drifted: err=%v charges=%d executor=%d",
			err, calls, executor.callCount())
	}
}

func TestDockerSandboxProposalRejectsUnfencedAndUnknownBeforeBudget(t *testing.T) {
	store := newTrackedStructuredStore()
	executor := &dockerSandboxProposalExecutorStub{}
	gateway := New(store, policy.NewDefaultChecker()).
		WithDockerSandboxProposalExecutor(executor)
	unfenced := validDockerSandboxToolCall(dockerSandboxProposalPayload)
	unfenced.RequestedBy = "model"
	unfenced.LeaseID = ""
	unfenced.LeaseGeneration = 0
	if _, err := gateway.Invoke(t.Context(), unfenced); err == nil {
		t.Fatal("unfenced Docker Sandbox proposal was accepted")
	}
	unknown := validDockerSandboxToolCall(strings.Replace(dockerSandboxProposalPayload,
		`"version":"sandbox_docker_run_proposal.v1",`,
		`"version":"sandbox_docker_run_proposal.v1","docker_flags":[],`, 1))
	if _, err := gateway.Invoke(t.Context(), unknown); err == nil {
		t.Fatal("unknown Docker authority field was accepted")
	}
	if store.chargeCount() != 0 || executor.callCount() != 0 {
		t.Fatalf("invalid proposal crossed pre-budget validation: charges=%d executor=%d",
			store.chargeCount(), executor.callCount())
	}
}
