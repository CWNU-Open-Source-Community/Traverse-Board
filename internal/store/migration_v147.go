package store

import "strings"

// controlledRunExplicitModelRouteStatements repairs the v144 creation guard so
// a caller may atomically pin a normalized provider/model route into the first
// Run. The Session must carry the exact same route; named profile routes remain
// valid for legacy and default creation requests.
var controlledRunExplicitModelRouteStatements = []string{
	`DROP TRIGGER trg_run_creation_operation_insert;`,
	controlledRunExplicitModelRouteTrigger(),
}

func controlledRunExplicitModelRouteTrigger() string {
	trigger := controlledRunExactNetworkAllowlistStatements[1]
	trigger = strings.Replace(trigger,
		`AND json_extract(run.config_json, '$.model_route') = mission.profile`,
		`AND (
					json_extract(run.config_json, '$.model_route') = mission.profile
					OR (
						typeof(json_extract(run.config_json, '$.model_route')) = 'text'
						AND json_extract(run.config_json, '$.model_route') =
							trim(json_extract(run.config_json, '$.model_route'))
						AND instr(json_extract(run.config_json, '$.model_route'), char(0)) = 0
						AND length(json_extract(run.config_json, '$.model_route')) BETWEEN 3 AND 513
						AND instr(json_extract(run.config_json, '$.model_route'), '/') BETWEEN 2 AND 257
						AND length(substr(json_extract(run.config_json, '$.model_route'),
							instr(json_extract(run.config_json, '$.model_route'), '/') + 1)) BETWEEN 1 AND 256
					)
				)`, 1)
	trigger = strings.Replace(trigger,
		`AND session_record.route = mission.profile`,
		`AND session_record.route = json_extract(run.config_json, '$.model_route')`, 1)
	return trigger
}
