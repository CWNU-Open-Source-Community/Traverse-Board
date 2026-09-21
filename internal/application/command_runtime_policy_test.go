package application

import (
	"strings"
	"testing"

	"cyberagent-workbench/internal/contextmgr"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/skills"
)

func TestSupervisorSystemPolicySeparatesCommandRuntimeOwnership(t *testing.T) {
	messages := supervisorMessages(nil, "continue", contextmgr.Selection{},
		skills.ContextAssembly{}, skills.ExternalContextAssembly{}, domain.RunModeSnapshot{})
	if len(messages) == 0 || messages[0].Role != "system" {
		t.Fatalf("Supervisor messages do not start with system policy: %#v", messages)
	}
	for _, clause := range []string{
		"explicitly offered command_runtime",
		"network intent shown in its current adapter schema",
		"host adapter may offer network=host without a destination allowlist",
		"Before destructive database operations",
		"never conflate its Job ownership with a user or Debug terminal",
	} {
		if !strings.Contains(messages[0].Content, clause) {
			t.Fatalf("Supervisor command-runtime policy is missing %q", clause)
		}
	}
}
