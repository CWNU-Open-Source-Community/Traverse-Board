package application

import (
	"encoding/json"
	"testing"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/llm"
	"cyberagent-workbench/internal/toolgateway"
)

func TestSupervisorHostCommandToolIsExposedOnlyInApprovalPermission(t *testing.T) {
	for _, test := range []struct {
		name string
		mode domain.RunExecutionPermissionMode
		want bool
	}{
		{name: "conservative", mode: domain.RunExecutionPermissionConservative},
		{name: "approval", mode: domain.RunExecutionPermissionApproval, want: true},
		{name: "full access", mode: domain.RunExecutionPermissionFullAccess},
		{name: "debug", mode: domain.RunExecutionPermissionDebug},
	} {
		t.Run(test.name, func(t *testing.T) {
			found := false
			for _, spec := range supervisorStructuredToolSpecs(domain.ExecutionPhaseDeliver, test.mode) {
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
		domain.ExecutionPhaseDeliver, domain.RunExecutionPermissionConservative)
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
		domain.ExecutionPhaseDeliver, domain.RunExecutionPermissionConservative)
	if err != nil || len(prepared) != 1 ||
		prepared[0].Name != string(toolgateway.DockerSandboxRunProposeTool) {
		t.Fatalf("advertised Docker Sandbox proposal was rejected: %#v err=%v", prepared, err)
	}
}

func TestSupervisorRejectsForgedHostCommandToolOutsideApprovalPermission(t *testing.T) {
	payload := json.RawMessage(`{"version":"host_command_proposal.v1","executable_path":"/workspace/tool","argv":["version"],"working_directory":"/workspace","timeout_milliseconds":1000,"purpose":"inspect the exact tool version"}`)
	calls := []llm.ToolCall{{ID: "provider-call-1", Name: string(toolgateway.HostCommandProposeTool), Arguments: payload}}
	if _, err := prepareSupervisorToolCalls(calls, "run-1", 1, 1,
		domain.ExecutionPhaseDeliver, domain.RunExecutionPermissionConservative); err == nil {
		t.Fatal("forged host command proposal was accepted outside approval permission")
	}
	prepared, err := prepareSupervisorToolCalls(calls, "run-1", 1, 1,
		domain.ExecutionPhaseDeliver, domain.RunExecutionPermissionApproval)
	if err != nil || len(prepared) != 1 || prepared[0].Name != string(toolgateway.HostCommandProposeTool) {
		t.Fatalf("approval host command proposal was rejected: %#v err=%v", prepared, err)
	}
}
