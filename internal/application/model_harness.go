package application

import (
	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/llm"
)

func prepareModelHarnessRequest(router *llm.Router, ref llm.ModelRef,
	workload llm.HarnessWorkload, request llm.ChatRequest,
) (llm.ChatRequest, error) {
	if router == nil {
		return llm.ChatRequest{}, apperror.New(apperror.CodeFailedPrecondition,
			"model router is required")
	}
	prepared, _, err := router.PrepareHarnessRequest(ref, workload, request)
	if err != nil {
		return llm.ChatRequest{}, apperror.Wrap(apperror.CodeFailedPrecondition,
			"selected model Harness is incompatible with this workload", err)
	}
	return prepared, nil
}
