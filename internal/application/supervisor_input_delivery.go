package application

import "encoding/json"

// This model-only projection comes from the store's exact prepared-input
// boundary lookup, never from user/tool text. Keep the durable accepted input
// untouched and append newly accepted mid-turn corrections after this text.
func supervisorBoundaryInputDelivery(input string, returnedReceipts int) string {
	if returnedReceipts == 0 {
		return input
	}
	encoded, _ := json.Marshal(struct {
		Version       string `json:"version"`
		Delivery      string `json:"delivery"`
		AcceptedInput string `json:"accepted_input"`
	}{"supervisor_input_delivery.v1", "tool_boundary_continuation", input})
	return "Harness input delivery: this is a new active tool segment of the SAME already accepted user request. " +
		"The preceding segment ended at an internal tool-round boundary; this delivery is not a new user submission or a request to restart the task. " +
		"Use the currently offered native tools for unfinished work. This marker is not the no-tool boundary announcement and does not request a tool-free continue. " +
		"The accepted_input field below preserves the original user instruction and all its scope constraints. " +
		"Continue from sealed observed results; do not repeat a completed setup or action merely because it also appears in the original request. " +
		"For a still-authorized live browser, first inspect a fresh snapshot and preserve the current page state instead of navigating again to redo completed checks. " +
		"This delivery does not prove an action succeeded, a browser survived cancellation or restart, or historical permissions remain active. " +
		"Recover missing state from actual evidence and current permissions; never automatically repeat an outcome_unknown action. " +
		"Later accepted user corrections remain authoritative and may explicitly change or restart the task.\n" + string(encoded)
}
