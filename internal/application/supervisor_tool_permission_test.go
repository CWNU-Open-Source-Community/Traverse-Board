package application

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/toolgateway"
)

func TestSupervisorHostCommandToolIsExposedOnlyInApprovalPermission(t *testing.T) {
	for _, test := range []struct {
		name    string
		surface domain.ExecutionSurface
		mode    domain.RunExecutionPermissionMode
		want    bool
	}{
		{name: "conservative", surface: domain.ExecutionSurfaceCode,
			mode: domain.RunExecutionPermissionConservative},
		{name: "approval", surface: domain.ExecutionSurfaceCode,
			mode: domain.RunExecutionPermissionApproval, want: true},
		{name: "Cyber approval", surface: domain.ExecutionSurfaceCyber,
			mode: domain.RunExecutionPermissionApproval},
		{name: "full access", surface: domain.ExecutionSurfaceCode,
			mode: domain.RunExecutionPermissionFullAccess},
		{name: "debug", surface: domain.ExecutionSurfaceCode,
			mode: domain.RunExecutionPermissionDebug},
	} {
		t.Run(test.name, func(t *testing.T) {
			found := false
			for _, spec := range supervisorStructuredToolSpecs(
				test.surface, domain.ExecutionPhaseDeliver,
				test.mode, false, false) {
				if spec.Name == string(toolgateway.HostCommandProposeTool) {
					found = true
				}
			}
			if found != test.want {
				t.Fatalf("host command tool visible=%t want=%t for %s", found, test.want, test.mode)
			}
		})
	}
}

func TestSupervisorAcceptsChildTaskProposalInDeliverPhase(t *testing.T) {
	payload := json.RawMessage(`{"version":"child_task_proposal.v1","tasks":[{"title":"Inspect","goal":"Inspect the parser","skills":["model.chat","read_file"],"turn_limit":2,"token_limit":128,"timeout_millis":60000}]}`)
	calls := []llm.ToolCall{{ID: "provider-call-1", Name: string(toolgateway.ChildTaskProposeTool), Arguments: payload}}
	prepared, err := prepareSupervisorToolCalls(calls, "run-1", 1, 1,
		domain.ExecutionSurfaceCode, domain.ExecutionPhaseDeliver,
		domain.RunExecutionPermissionConservative, false, false)
	if err != nil || len(prepared) != 1 || prepared[0].Name != string(toolgateway.ChildTaskProposeTool) {
		t.Fatalf("child task proposal was rejected: %#v err=%v", prepared, err)
	}
}

func TestSupervisorAcceptsAdvertisedDockerSandboxProposal(t *testing.T) {
	payload := json.RawMessage(`{
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
}`)
	calls := []llm.ToolCall{{ID: "provider-call-docker",
		Name: string(toolgateway.DockerSandboxRunProposeTool), Arguments: payload}}
	prepared, err := prepareSupervisorToolCalls(calls, "run-1", 1, 1,
		domain.ExecutionSurfaceCode, domain.ExecutionPhaseDeliver,
		domain.RunExecutionPermissionConservative, false, false)
	if err != nil || len(prepared) != 1 ||
		prepared[0].Name != string(toolgateway.DockerSandboxRunProposeTool) {
		t.Fatalf("advertised Docker Sandbox proposal was rejected: %#v err=%v", prepared, err)
	}
}

func TestSupervisorExposesAndAcceptsOneShotCommandProposal(t *testing.T) {
	found := false
	for _, spec := range supervisorStructuredToolSpecs(
		domain.ExecutionSurfaceCode, domain.ExecutionPhaseDeliver,
		domain.RunExecutionPermissionConservative, false, false) {
		if spec.Name == string(toolgateway.OneShotCommandProposeTool) {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("one-shot command proposal is not exposed to the Supervisor")
	}
	payload, err := json.Marshal(toolgateway.OneShotCommandProposalSpec{
		Version: "once_command.v1", ExecutablePath: filepath.Join(t.TempDir(), "tool.exe"),
		Argv: []string{"version"}, WorkingDirectory: t.TempDir(),
		Environment: []string{}, TimeoutMS: 1000, Purpose: "inspect tool version",
	})
	if err != nil {
		t.Fatal(err)
	}
	calls := []llm.ToolCall{{ID: "provider-call-once",
		Name: string(toolgateway.OneShotCommandProposeTool), Arguments: payload}}
	prepared, err := prepareSupervisorToolCalls(calls, "run-1", 1, 1,
		domain.ExecutionSurfaceCode, domain.ExecutionPhaseDeliver,
		domain.RunExecutionPermissionConservative, false, false)
	if err != nil || len(prepared) != 1 ||
		prepared[0].Name != string(toolgateway.OneShotCommandProposeTool) {
		t.Fatalf("one-shot command proposal was rejected: %#v err=%v", prepared, err)
	}
}

func TestSupervisorRejectsForgedHostCommandToolOutsideApprovalPermission(t *testing.T) {
	payload := json.RawMessage(`{"version":"host_command_proposal.v1","executable_path":"/workspace/tool","argv":["version"],"working_directory":"/workspace","timeout_milliseconds":1000,"purpose":"inspect the exact tool version"}`)
	calls := []llm.ToolCall{{ID: "provider-call-1", Name: string(toolgateway.HostCommandProposeTool), Arguments: payload}}
	if _, err := prepareSupervisorToolCalls(calls, "run-1", 1, 1,
		domain.ExecutionSurfaceCode, domain.ExecutionPhaseDeliver,
		domain.RunExecutionPermissionConservative, false, false); err == nil {
		t.Fatal("forged host command proposal was accepted outside approval permission")
	}
	prepared, err := prepareSupervisorToolCalls(calls, "run-1", 1, 1,
		domain.ExecutionSurfaceCode, domain.ExecutionPhaseDeliver,
		domain.RunExecutionPermissionApproval, false, false)
	if err != nil || len(prepared) != 1 || prepared[0].Name != string(toolgateway.HostCommandProposeTool) {
		t.Fatalf("approval host command proposal was rejected: %#v err=%v", prepared, err)
	}
	if _, err := prepareSupervisorToolCalls(calls, "run-1", 1, 1,
		domain.ExecutionSurfaceCyber, domain.ExecutionPhaseDeliver,
		domain.RunExecutionPermissionApproval, false, false); err == nil {
		t.Fatal("forged host command proposal was accepted on the Cyber surface")
	}
}

func TestSupervisorSkillCandidateToolRequiresExplicitGeneratorContext(t *testing.T) {
	found := func(enabled bool) bool {
		for _, spec := range supervisorStructuredToolSpecs(
			domain.ExecutionSurfaceCode, domain.ExecutionPhaseDeliver,
			domain.RunExecutionPermissionConservative, enabled, false) {
			if spec.Name == string(toolgateway.SkillCandidateProposeTool) {
				return true
			}
		}
		return false
	}
	if found(false) || !found(true) {
		t.Fatal("Skill candidate tool exposure did not follow explicit generator context")
	}
	payload := json.RawMessage(`{"version":"skill_candidate_proposal.v1","name":"bounded-helper","skill_version":"1.0.0","description":"A reusable generated workflow.","profiles":["code"],"surfaces":["code"],"phases":["deliver"],"roles":["root"],"user_invocable":true,"model_invocable":false,"explicit_only":true,"tool_dependencies":["read_file"],"content":"# Bounded helper\n\nInspect and report verified facts.\n"}`)
	calls := []llm.ToolCall{{ID: "provider-call-candidate",
		Name: string(toolgateway.SkillCandidateProposeTool), Arguments: payload}}
	if _, err := prepareSupervisorToolCalls(calls, "run-1", 1, 1,
		domain.ExecutionSurfaceCode, domain.ExecutionPhaseDeliver,
		domain.RunExecutionPermissionConservative, false, false); err == nil {
		t.Fatal("forged Skill candidate proposal was accepted without generator context")
	}
	if _, err := prepareSupervisorToolCalls(calls, "run-1", 1, 1,
		domain.ExecutionSurfaceCode, domain.ExecutionPhaseDeliver,
		domain.RunExecutionPermissionConservative, true, false); err != nil {
		t.Fatalf("explicit generator Skill candidate proposal was rejected: %v", err)
	}
}

func TestSupervisorDebugTerminalRequiresDeliverDebugAndRuntime(t *testing.T) {
	tests := []struct {
		name    string
		surface domain.ExecutionSurface
		phase   domain.ExecutionPhase
		mode    domain.RunExecutionPermissionMode
		enabled bool
		want    bool
	}{
		{name: "enabled", surface: domain.ExecutionSurfaceCode,
			phase: domain.ExecutionPhaseDeliver,
			mode:  domain.RunExecutionPermissionDebug, enabled: true, want: true},
		{name: "no runtime", surface: domain.ExecutionSurfaceCode,
			phase: domain.ExecutionPhaseDeliver,
			mode:  domain.RunExecutionPermissionDebug},
		{name: "Plan", surface: domain.ExecutionSurfaceCode,
			phase: domain.ExecutionPhasePlan,
			mode:  domain.RunExecutionPermissionDebug, enabled: true},
		{name: "approval permission", surface: domain.ExecutionSurfaceCode,
			phase: domain.ExecutionPhaseDeliver,
			mode:  domain.RunExecutionPermissionApproval, enabled: true},
		{name: "Cyber surface", surface: domain.ExecutionSurfaceCyber,
			phase: domain.ExecutionPhaseDeliver,
			mode:  domain.RunExecutionPermissionDebug, enabled: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			found := false
			for _, spec := range supervisorStructuredToolSpecs(test.surface, test.phase,
				test.mode, false, test.enabled) {
				found = found || spec.Name == string(toolgateway.DebugTerminalTool)
			}
			if found != test.want {
				t.Fatalf("debug terminal visible=%t want=%t", found, test.want)
			}
		})
	}
	payload := json.RawMessage(`{"version":"debug_terminal.v1","action":"read","cursor":0,"max_bytes":4096,"wait_milliseconds":0}`)
	calls := []llm.ToolCall{{ID: "provider-call-debug", Name: string(toolgateway.DebugTerminalTool), Arguments: payload}}
	if _, err := prepareSupervisorToolCalls(calls, "run-1", 1, 1,
		domain.ExecutionSurfaceCyber, domain.ExecutionPhaseDeliver,
		domain.RunExecutionPermissionDebug, false, true); err == nil {
		t.Fatal("forged Debug terminal call was accepted on the Cyber surface")
	}
	if recoverableSupervisorToolError(toolgateway.NoteCreateTool,
		apperror.CodeFailedPrecondition) {
		t.Fatal("Debug lease recovery semantics widened another Supervisor tool")
	}
	if !recoverableSupervisorToolError(toolgateway.DebugTerminalTool,
		apperror.CodeFailedPrecondition) {
		t.Fatal("missing Debug terminal lease was not model-recoverable")
	}
}
