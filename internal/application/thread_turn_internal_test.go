package application

import (
	"strings"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
)

func TestStoredThreadTurnFailureIsActionableWithoutProjectingRawProviderDetail(t *testing.T) {
	handoff := domain.RunExecutionHandoff{Result: &domain.RunExecutionHandoffResult{
		Status: domain.RunExecutionHandoffFailed, ErrorCode: "failed_precondition",
		StopReason: "provider-secret-response-must-not-appear",
	}}
	err := storedThreadTurnExecutionError(handoff)
	if apperror.CodeOf(err) != apperror.CodeFailedPrecondition ||
		!strings.Contains(err.Error(), "execution precondition was not met") ||
		strings.Contains(err.Error(), "fixed settings") ||
		strings.Contains(err.Error(), handoff.Result.StopReason) {
		t.Fatalf("unsafe or non-actionable Thread error: %v", err)
	}
}
