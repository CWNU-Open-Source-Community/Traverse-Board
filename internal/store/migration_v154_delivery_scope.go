package store

import "strings"

var threadDrydockDeliveryScopeStatements = func() []string {
	var statement string
	for _, candidate := range standardCodeDeliveryStatements {
		if strings.Contains(candidate, "CREATE TRIGGER trg_standard_code_delivery_insert") {
			statement = candidate
			break
		}
	}
	if statement == "" {
		panic("missing Standard Code delivery insertion guard")
	}
	statement = strings.Replace(statement, "AND drydock.run_id = NEW.run_id", `AND EXISTS(SELECT 1 FROM run_file_drydock_bindings holder
   LEFT JOIN threads thread ON thread.id=holder.thread_id
   WHERE holder.run_id=NEW.run_id AND holder.session_id=NEW.session_id
    AND holder.mission_id=NEW.mission_id AND holder.drydock_id=drydock.id
    AND holder.workspace_id=NEW.drydock_workspace_id
    AND holder.source_workspace_id=NEW.source_workspace_id
    AND (holder.thread_id='' OR thread.last_run_id=holder.run_id))`, 1)
	statement = strings.Replace(statement, "\t\t\t\t\tAND drydock.session_id = NEW.session_id\n", "", 1)
	return []string{"DROP TRIGGER trg_standard_code_delivery_insert;", statement}
}()
