package application

import (
	"time"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/toolcontract"
)

func mcpStoppedResult(call domain.SupervisorToolCall, code, message string, receipt *toolcontract.Receipt) domain.SupervisorToolResult {
	metadata := map[string]string{"execution_receipt": code, "automatic_retry": "forbidden"}
	if receipt != nil {
		metadata["operation_id"] = receipt.OperationID
		metadata["execution_receipt"] = string(receipt.State)
		if receipt.ErrorCode != "" {
			metadata["operation_error_code"] = receipt.ErrorCode
		}
	}
	raw, _ := marshalSupervisorToolResultEnvelope(supervisorToolResultEnvelope{Version: supervisorToolResultVersion, Tool: call.ToolName,
		Status: string(domain.SupervisorToolFailed), Code: code, Message: boundedSupervisorToolMessage(message), Metadata: metadata})
	return domain.SupervisorToolResult{CallID: call.CallID, Status: domain.SupervisorToolFailed, ResultJSON: string(raw), ErrorCode: code, CompletedAt: time.Now().UTC()}
}
