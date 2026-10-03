package toolgateway

import (
	"encoding/json"

	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/tools"
)

func NormalizeCommandRuntimePayload(raw json.RawMessage) (CommandRuntimeInput, json.RawMessage, error) {
	return normalizeCommandRuntimePayload(raw)
}

// Native host policy is evaluated from canonical inputs both before review and
// at the process/stdin sink. NeedsApproval is preserved for the common decision;
// an approved call never overrides an explicit current host denial.
func CommandRuntimePolicyDecision(checker policy.Checker, input CommandRuntimeInput) policy.Decision {
	if checker == nil {
		return policy.Decision{Allowed: false, Risk: "high", Reason: "command host policy is unavailable"}
	}
	raw, _ := json.Marshal(input)
	decision := checker.CheckToolCall(tools.Call{Name: string(CommandRuntimeTool), Args: map[string]string{"payload": string(raw)}})
	if !decision.Allowed {
		return decision
	}
	for _, command := range input.Commands {
		current := CommandRuntimeCommandPolicy(checker, command)
		if !current.Allowed {
			return current
		}
		if current.NeedsApproval {
			decision = current
		}
	}
	return decision
}

func CommandRuntimeCommandPolicy(checker policy.Checker, command runner.CommandRuntimeSpec) policy.Decision {
	if checker == nil {
		return policy.Decision{Allowed: false, Risk: "high", Reason: "command host policy is unavailable"}
	}
	if reason := commandRuntimeNetworkViolation(command); reason != "" {
		return policy.Decision{Allowed: false, Risk: "high", Reason: reason}
	}
	encoded, _ := json.Marshal(command)
	name, argument := ShellTool, "command"
	if command.Profile == runner.CommandRuntimeProcess {
		name, argument = ScriptProcessTool, "proposal"
	}
	return checker.CheckToolCall(tools.Call{Name: string(name), Args: map[string]string{argument: string(encoded)}})
}
