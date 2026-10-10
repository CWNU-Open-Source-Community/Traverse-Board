package store

import "strings"

// Historical migrations stay frozen. Replace only the old fixed budget guard;
// all Mission/Session/mode/authority/root/event checks remain byte-identical.
func controlledTaskConfigurationStatements() []string {
	trigger := controlledRunExplicitModelRouteTrigger()
	const oldBudget = `AND json_extract(run.budget_json, '$.max_turns') = 100
				AND COALESCE(json_extract(run.budget_json, '$.max_tokens'), 0) = 0
				AND json_extract(run.budget_json, '$.max_tool_calls') = 100
				AND COALESCE(json_extract(run.budget_json, '$.max_cost_usd'), 0) = 0
				AND COALESCE(json_extract(run.budget_json, '$.timeout_seconds'), 0) = 0`
	const bounded = `AND (
					(json_type(run.config_json, '$.requested_budget') IS NULL
						AND json_extract(run.budget_json, '$.max_turns') = 100
						AND json_extract(run.budget_json, '$.max_tool_calls') = 100
						AND COALESCE(json_extract(run.budget_json, '$.max_tokens'), 0) = 0
						AND COALESCE(json_extract(run.budget_json, '$.max_cost_usd'), 0) = 0
						AND COALESCE(json_extract(run.budget_json, '$.timeout_seconds'), 0) = 0)
					OR (json_type(run.config_json, '$.requested_budget') = 'object'
						AND json_type(run.config_json, '$.requested_budget.max_turns') = 'integer'
						AND json_extract(run.config_json, '$.requested_budget.max_turns') BETWEEN 1 AND 10000
						AND json_type(run.config_json, '$.requested_budget.max_tool_calls') = 'integer'
						AND json_extract(run.config_json, '$.requested_budget.max_tool_calls') BETWEEN 1 AND 1000000
						AND COALESCE(json_type(run.config_json, '$.requested_budget.max_tokens'), 'integer') = 'integer'
						AND COALESCE(json_extract(run.config_json, '$.requested_budget.max_tokens'), 0) BETWEEN 0 AND 1000000000
						AND COALESCE(json_type(run.config_json, '$.requested_budget.timeout_seconds'), 'integer') = 'integer'
						AND COALESCE(json_extract(run.config_json, '$.requested_budget.timeout_seconds'), 0) BETWEEN 0 AND 604800
						AND COALESCE(json_type(run.config_json, '$.requested_budget.max_cost_usd'), 'real') IN ('real', 'integer')
						AND COALESCE(json_extract(run.config_json, '$.requested_budget.max_cost_usd'), 0) BETWEEN 0 AND 100000
						AND NOT EXISTS (SELECT 1 FROM json_each(run.config_json, '$.requested_budget') WHERE key NOT IN ('max_turns','max_tool_calls','max_tokens','max_cost_usd','timeout_seconds'))
						AND json_type(run.budget_json, '$.max_turns') = 'integer'
						AND json_extract(run.budget_json, '$.max_turns') BETWEEN 1 AND json_extract(run.config_json, '$.requested_budget.max_turns')
						AND json_type(run.budget_json, '$.max_tool_calls') = 'integer'
						AND json_extract(run.budget_json, '$.max_tool_calls') BETWEEN 1 AND json_extract(run.config_json, '$.requested_budget.max_tool_calls')
						AND COALESCE(json_extract(run.budget_json, '$.max_tokens'), 0) = COALESCE(json_extract(run.config_json, '$.requested_budget.max_tokens'), 0)
						AND COALESCE(json_extract(run.budget_json, '$.max_cost_usd'), 0) = COALESCE(json_extract(run.config_json, '$.requested_budget.max_cost_usd'), 0)
						AND COALESCE(json_extract(run.budget_json, '$.timeout_seconds'), 0) = COALESCE(json_extract(run.config_json, '$.requested_budget.timeout_seconds'), 0)
						AND (
							(json_type(run.config_json, '$.project_config') IS NULL
								AND COALESCE(json_extract(run.config_json, '$.project_config_fingerprint'), '') = ''
								AND json_extract(run.budget_json, '$.max_turns') = json_extract(run.config_json, '$.requested_budget.max_turns')
								AND json_extract(run.budget_json, '$.max_tool_calls') = json_extract(run.config_json, '$.requested_budget.max_tool_calls'))
							OR (json_type(run.config_json, '$.project_config') = 'object'
								AND json_extract(run.config_json, '$.project_config.protocol') = 'project_config.v1'
								AND length(json_extract(run.config_json, '$.project_config_fingerprint')) = 64
								AND json_extract(run.config_json, '$.project_config_fingerprint') NOT GLOB '*[^0-9a-f]*'
								AND json_extract(run.budget_json, '$.max_turns') = COALESCE(NULLIF(json_extract(run.config_json, '$.project_config.max_turns'), 0), json_extract(run.config_json, '$.requested_budget.max_turns'))
								AND json_extract(run.budget_json, '$.max_tool_calls') = COALESCE(NULLIF(json_extract(run.config_json, '$.project_config.max_tool_calls'), 0), json_extract(run.config_json, '$.requested_budget.max_tool_calls'))
								AND (COALESCE(json_extract(run.config_json, '$.project_config.read_only'), 0) = 0 OR mission.profile IN ('review', 'learn'))
								AND (COALESCE(json_array_length(run.config_json, '$.project_config.allowed_profiles'), 0) = 0 OR EXISTS (SELECT 1 FROM json_each(run.config_json, '$.project_config.allowed_profiles') WHERE value = mission.profile)))
							)
						)
					)`
	const oldRoot = "AND root.child_limit = 0 AND root.turn_limit = 100 AND root.token_limit = 0"
	if strings.Count(trigger, oldBudget) != 1 || strings.Count(trigger, oldRoot) != 1 {
		panic("task configuration migration lost its fixed budget/root predicate")
	}
	trigger = strings.Replace(trigger, oldBudget, bounded, 1)
	trigger = strings.Replace(trigger, oldRoot, `AND root.child_limit = 0
				AND root.turn_limit = json_extract(run.budget_json, '$.max_turns')
				AND root.token_limit = COALESCE(json_extract(run.budget_json, '$.max_tokens'), 0)`, 1)
	return []string{`DROP TRIGGER trg_run_creation_operation_insert;`, trigger,
		`CREATE TRIGGER trg_run_task_configuration_immutable BEFORE UPDATE OF config_json, budget_json ON runs
		 WHEN json_type(OLD.config_json, '$.requested_budget') = 'object' AND (
		  NEW.budget_json != OLD.budget_json
		  OR COALESCE(json_extract(NEW.config_json, '$.requested_budget'), '') != json_extract(OLD.config_json, '$.requested_budget')
		  OR COALESCE(json_extract(NEW.config_json, '$.project_config'), '') != COALESCE(json_extract(OLD.config_json, '$.project_config'), '')
		  OR COALESCE(json_extract(NEW.config_json, '$.project_config_fingerprint'), '') != COALESCE(json_extract(OLD.config_json, '$.project_config_fingerprint'), ''))
		 BEGIN SELECT RAISE(ABORT, 'Run task configuration is immutable'); END;`,
	}
}
