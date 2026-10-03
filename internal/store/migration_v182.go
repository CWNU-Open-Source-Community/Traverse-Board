package store

import (
	"fmt"
	"strings"
)

// Frozen v181 trigger inputs come from the historical migration prefix, never
// from the generated clean-install baseline or a current application writer.
// Only budget predicates change. Candidate snapshots, tables, rows, and all
// existing lease, cancellation, permission, artifact and Run bindings survive.
func sandboxLiveCandidateBudgetStatements(previous []migration) []string {
	if len(previous) != 181 || previous[len(previous)-1].Version != 181 {
		panic("v182 requires the frozen v1-v181 migration prefix")
	}
	names := []string{
		"trg_sandbox_execution_candidate_insert",
		"trg_sandbox_disabled_execution_insert",
		"trg_sandbox_disabled_preflight_insert",
		"trg_sandbox_backend_evidence_insert",
		"trg_sandbox_output_simulation_insert",
		"trg_sandbox_docker_observation_insert",
		"trg_sandbox_docker_container_plan_insert",
		"trg_sandbox_docker_product_admission_insert",
	}
	definitions := make(map[string]string, len(names))
	for _, step := range previous {
		for _, statement := range step.Statements {
			for _, name := range names {
				if strings.HasPrefix(statement, "CREATE TRIGGER "+name+"\n") {
					definitions[name] = statement
				} else if statement == "DROP TRIGGER "+name+";" || statement == "DROP TRIGGER IF EXISTS "+name+";" {
					delete(definitions, name)
				}
			}
		}
	}
	var result []string
	for _, name := range names {
		definition := definitions[name]
		if definition == "" {
			panic("v182 lost historical trigger " + name)
		}
		reference := "candidate"
		if name == "trg_sandbox_execution_candidate_insert" {
			reference = "NEW"
		}
		var start, end string
		var admission string
		if name == "trg_sandbox_docker_product_admission_insert" {
			start = "\t\t\t\tAND candidate.tool_calls_used = COALESCE((SELECT usage.consumed"
			end = "\t\t\t\t\t\t- candidate.tool_calls_used)\n\t\t\t\t\tEND"
			admission = sandboxAdmissionCurrentBudgetV182
		} else {
			start = "\t\t\t\tAND " + reference + ".tokens_used ="
			end = "\t\t\t\t\tOR " + reference + ".tool_calls_used < CAST(json_extract(run.budget_json, '$.max_tool_calls') AS INTEGER))"
		}
		if strings.Count(definition, start) != 1 || strings.Count(definition, end) != 1 {
			panic("v182 lost exact budget anchors for " + name)
		}
		first, last := strings.Index(definition, start), strings.Index(definition, end)+len(end)
		if last <= first {
			panic("v182 budget anchors are out of order for " + name)
		}
		guard := fmt.Sprintf(sandboxCurrentBudgetGuardV182, reference, admission)
		definition = definition[:first] + guard + definition[last:]
		result = append(result, "DROP TRIGGER "+name+";", definition)
	}
	return result
}

// Current usage is read by the INSERT statement inside its real transaction.
// A live candidate's immutable snapshot is a lower bound, while a quiescent
// candidate still requires equality. Existing exact lease checks remain outside
// this fragment; no declaration by the candidate grants authority on its own.
const sandboxCurrentBudgetGuardV182 = `				AND EXISTS (
					SELECT 1 FROM (SELECT
						COALESCE((SELECT SUM(node.tokens_used) FROM agent_nodes node
							WHERE node.run_id = NEW.run_id), 0) +
						COALESCE((SELECT SUM(CASE WHEN call.usage_recorded = 1 THEN call.total_tokens
							ELSE call.reserved_total_tokens END) FROM readonly_fanout_model_calls call
							WHERE call.run_id = NEW.run_id), 0) AS tokens,
						COALESCE((SELECT checkpoint.execution_millis FROM run_supervisor_checkpoints checkpoint
							WHERE checkpoint.run_id = NEW.run_id), 0) +
						COALESCE((SELECT SUM(call.elapsed_millis) FROM specialist_model_calls call
							WHERE call.run_id = NEW.run_id), 0) +
						COALESCE((SELECT SUM(CASE WHEN call.elapsed_recorded = 1 THEN call.elapsed_millis
							ELSE call.reserved_millis END) FROM readonly_fanout_model_calls call
							WHERE call.run_id = NEW.run_id), 0) AS execution_millis,
						COALESCE((SELECT usage.consumed FROM run_tool_usage usage
							WHERE usage.run_id = NEW.run_id), 0) AS tool_calls
					) current_usage
					WHERE current_usage.tokens >= %[1]s.tokens_used
						AND current_usage.execution_millis >= %[1]s.execution_millis_used
						AND current_usage.tool_calls >= %[1]s.tool_calls_used
						AND (%[1]s.lease_quiescent = 0 OR (
							current_usage.tokens = %[1]s.tokens_used
							AND current_usage.execution_millis = %[1]s.execution_millis_used
							AND current_usage.tool_calls = %[1]s.tool_calls_used))
						AND (COALESCE(CAST(json_extract(run.budget_json, '$.max_tokens') AS INTEGER), 0) = 0
							OR current_usage.tokens < CAST(json_extract(run.budget_json, '$.max_tokens') AS INTEGER))
						AND (COALESCE(CAST(json_extract(run.budget_json, '$.timeout_seconds') AS INTEGER), 0) = 0
							OR current_usage.execution_millis < CAST(json_extract(run.budget_json, '$.timeout_seconds') AS INTEGER) * 1000)
						AND (COALESCE(CAST(json_extract(run.budget_json, '$.max_tool_calls') AS INTEGER), 0) = 0
							OR current_usage.tool_calls < CAST(json_extract(run.budget_json, '$.max_tool_calls') AS INTEGER))%[2]s
				)`

// Admission limits must be derived from the current ledger, not the original
// candidate snapshot. A charge racing admission creation must never leave an
// overstated grant, even when the old candidate itself remains within budget.
const sandboxAdmissionCurrentBudgetV182 = `
						AND NEW.wall_clock_seconds = CASE
							WHEN COALESCE(CAST(json_extract(run.budget_json, '$.timeout_seconds') AS INTEGER), 0) = 0
								THEN plan.timeout_seconds
							ELSE MIN(plan.timeout_seconds,
								(CAST(json_extract(run.budget_json, '$.timeout_seconds') AS INTEGER) * 1000
									- current_usage.execution_millis) / 1000)
							END
						AND NEW.tool_calls_remaining = CASE
							WHEN COALESCE(CAST(json_extract(run.budget_json, '$.max_tool_calls') AS INTEGER), 0) = 0
								THEN 100
							ELSE MIN(100, CAST(json_extract(run.budget_json, '$.max_tool_calls') AS INTEGER)
								- current_usage.tool_calls)
							END`
