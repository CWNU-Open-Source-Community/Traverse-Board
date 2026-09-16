package httpapi

import (
	"context"

	"cyberagent-workbench/internal/application"
)

type ThreadExecutionController interface {
	ExecutionState(context.Context, string) (application.ThreadExecutionState, error)
	Interrupt(context.Context, string, string) (application.ThreadExecutionState, error)
}

type ThreadInterruptRequestView struct {
	Version     string `json:"version"`
	ExecutionID string `json:"execution_id"`
}
