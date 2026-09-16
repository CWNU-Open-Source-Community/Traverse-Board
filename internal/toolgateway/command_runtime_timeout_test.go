package toolgateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"

	"cyberagent-workbench/internal/policy"
	"cyberagent-workbench/internal/runner"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

func commandRuntimeTimeoutPayload(t *testing.T, action string, timeouts ...int64) json.RawMessage {
	t.Helper()
	var input CommandRuntimeInput
	if err := json.Unmarshal(commandRuntimeValidPayload("Write-Output ok"), &input); err != nil {
		t.Fatal(err)
	}
	command := input.Commands[0]
	input.Commands = nil
	input.Action = action
	if action == CommandRuntimeActionStart {
		input.FailurePolicy, input.MaxBytes = "", nil
	}
	for _, timeout := range timeouts {
		command.TimeoutMilliseconds = timeout
		input.Commands = append(input.Commands, command)
	}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestCommandRuntimePublishedTimeoutSchemaMatchesActionContract(t *testing.T) {
	definition, found := SupervisorToolDefinition(CommandRuntimeTool)
	if !found {
		t.Fatal("published Command Runtime definition is missing")
	}
	listed := 0
	for _, candidate := range SupervisorToolDefinitions() {
		if candidate.Name == CommandRuntimeTool {
			listed++
			if !bytes.Equal(candidate.InputSchema, definition.InputSchema) || candidate.Description != definition.Description {
				t.Fatal("published catalog and direct tool schema differ")
			}
		}
	}
	if listed != 1 {
		t.Fatalf("published Command Runtime count=%d", listed)
	}
	var rawSchema map[string]any
	if err := json.Unmarshal(definition.InputSchema, &rawSchema); err != nil {
		t.Fatal(err)
	}
	properties := rawSchema["properties"].(map[string]any)
	commandProperties := properties["commands"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	actionDescription := properties["action"].(map[string]any)["description"].(string)
	timeoutDescription := commandProperties["timeout_milliseconds"].(map[string]any)["description"].(string)
	guidance := strings.Join([]string{definition.Description, actionDescription, timeoutDescription}, "\n")
	for _, required := range []string{"SUM", "ALL commands", strconv.Itoa(MaxCommandRuntimeForegroundMillis) + "ms",
		"action=start", "original job_id", "cursor", "wait_milliseconds",
		strconv.FormatInt(runner.MaxCommandRuntimeTimeout.Milliseconds(), 10) + "ms"} {
		if !strings.Contains(guidance, required) {
			t.Fatalf("published timeout contract omits %q", required)
		}
	}
	if !strings.Contains(actionDescription, "foreground") || !strings.Contains(actionDescription, "background Job") ||
		!strings.Contains(timeoutDescription, "Milliseconds") || !strings.Contains(timeoutDescription, "batch sum") {
		t.Fatal("action and timeout fields omit their respective execution mode and unit/sum constraints")
	}
	compiler := jsonschema.NewCompiler()
	compiler.Draft = jsonschema.Draft2020
	compiler.LoadURL = func(string) (io.ReadCloser, error) {
		return nil, errors.New("external schema loads are forbidden in this test")
	}
	if err := compiler.AddResource("command-runtime.json", bytes.NewReader(definition.InputSchema)); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("command-runtime.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name       string
		action     string
		timeouts   []int64
		schemaPass bool
		goPass     bool
	}{
		{"run_exact_limit", CommandRuntimeActionRun, []int64{MaxCommandRuntimeForegroundMillis}, true, true},
		{"run_wire_300000", CommandRuntimeActionRun, []int64{300000}, false, false},
		{"run_correction_120000", CommandRuntimeActionRun, []int64{120000}, false, false},
		{"run_batch_exact_sum", CommandRuntimeActionRun, []int64{12000, 13000}, true, true},
		// JSON Schema bounds each item; the existing Go guard proves the SUM.
		{"run_batch_exceeds_sum", CommandRuntimeActionRun, []int64{15000, 15000}, true, false},
		{"start_long_command", CommandRuntimeActionStart, []int64{300000}, true, true},
		{"start_original_maximum", CommandRuntimeActionStart, []int64{runner.MaxCommandRuntimeTimeout.Milliseconds()}, true, true},
		{"start_over_maximum", CommandRuntimeActionStart, []int64{runner.MaxCommandRuntimeTimeout.Milliseconds() + 1}, false, false},
		{"start_exactly_one_command", CommandRuntimeActionStart, []int64{1000, 1000}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := commandRuntimeTimeoutPayload(t, tc.action, tc.timeouts...)
			var value any
			if err := json.Unmarshal(payload, &value); err != nil {
				t.Fatal(err)
			}
			if err := schema.Validate(value); (err == nil) != tc.schemaPass {
				t.Fatalf("published schema acceptance=%t want=%t: %v", err == nil, tc.schemaPass, err)
			}
			normalized, err := NormalizeSupervisorToolPayload(CommandRuntimeTool, payload)
			if (err == nil) != tc.goPass {
				t.Fatalf("Go boundary acceptance=%t want=%t: %v", err == nil, tc.goPass, err)
			}
			if tc.goPass {
				var input CommandRuntimeInput
				if err := json.Unmarshal(normalized, &input); err != nil {
					t.Fatal(err)
				}
				for index, command := range input.Commands {
					if command.TimeoutMilliseconds != tc.timeouts[index] || input.Action != tc.action {
						t.Fatal("timeout was silently clamped or action changed")
					}
				}
			}
		})
	}
}

func TestCommandRuntimeForegroundTimeoutDiagnosticBeforeExecution(t *testing.T) {
	state := &commandRuntimePolicyStore{trackedStructuredStore: newTrackedStructuredStore()}
	executor := &commandRuntimeExecutorStub{}
	gateway := New(state, policy.NewDefaultChecker()).WithCommandRuntimeExecutor(executor)
	for _, timeouts := range [][]int64{{300000}, {120000}, {15000, 15000}} {
		_, err := gateway.Invoke(t.Context(), commandRuntimeToolCall(
			commandRuntimeTimeoutPayload(t, CommandRuntimeActionRun, timeouts...)))
		if err == nil {
			t.Fatal("oversized foreground batch reached execution")
		}
		total := int64(0)
		for _, timeout := range timeouts {
			total += timeout
		}
		for _, wanted := range []string{strconv.FormatInt(total, 10) + "ms", "25000ms", "action=start", "original job_id", "do not submit the same command again"} {
			if !strings.Contains(err.Error(), wanted) {
				t.Fatalf("timeout diagnostic omits %q: %v", wanted, err)
			}
		}
	}
	if executor.calls != 0 || state.chargeCount() != 0 {
		t.Fatalf("invalid timeouts executed or consumed tool budget: calls=%d charged=%d", executor.calls, state.chargeCount())
	}
}
