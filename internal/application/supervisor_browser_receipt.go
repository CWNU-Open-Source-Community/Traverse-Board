package application

import "cyberagent-workbench/internal/domain"

// A historical receipt is not a current snapshot. Generic array compaction
// would preserve only three layout nodes while leaving truncated=false. Keep
// the exact history pointer in the enclosing receipt and omit layout as a unit.
func supervisorHistoricalBrowserResult(call domain.SupervisorToolCall, result any) any {
	if call.ToolName != "browser_snapshot" {
		return result
	}
	object, ok := result.(map[string]any)
	if !ok {
		return result
	}
	out := make(map[string]any, len(object))
	for key, value := range object {
		if key != "layout" {
			out[key] = value
		}
	}
	if _, ok := object["layout"]; ok {
		out["layout_omitted"] = true
	}
	out["browser_references_historical"] = true
	return out
}
