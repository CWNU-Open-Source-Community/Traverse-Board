package modelregistry

import (
	"cyberagent-workbench/internal/llm"
	"testing"
)

func TestTypedIncompleteCompletionKeepsConfiguredQualificationStatus(t *testing.T) {
	for _, reason := range []llm.ProviderFailureReason{llm.ProviderFailureContextLimit, llm.ProviderFailureOutputLimit, llm.ProviderFailurePaused, llm.ProviderFailureRefusal} {
		status := QualificationStatusFor(llm.OutcomePermanent, reason)
		if status != QualificationStatusResponseIncomplete || !validQualificationStatus(status) {
			t.Fatalf("reason=%s status=%s", reason, status)
		}
	}
}
