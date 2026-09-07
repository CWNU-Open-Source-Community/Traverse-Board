package store

import "strings"

// threadPermissionPreparingDeferredStatements repairs the v146 operation
// binding so the observable preparing phase has the same immutable-current-Run
// semantics as running and waiting_approval. The Thread preference advances,
// while the preparing Run keeps its exact permission snapshot until a successor
// is created.
var threadPermissionPreparingDeferredStatements = []string{
	`DROP TRIGGER trg_thread_execution_permission_operation_insert;`,
	threadPermissionPreparingDeferredTrigger(),
}

func threadPermissionPreparingDeferredTrigger() string {
	trigger := threadPermissionDeferredEffectStatements[7]
	return strings.Replace(trigger,
		`AND run.status IN ('running', 'waiting_approval')`,
		`AND run.status IN ('preparing', 'running', 'waiting_approval')`, 1)
}
