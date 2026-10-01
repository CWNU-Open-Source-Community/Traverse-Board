package application

import (
	"regexp"
	"testing"

	"cyberagent-workbench/internal/contextmgr"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/skills"
)

// Every concrete lifecycle example sent as root policy must be accepted by the
// same decoder used for actual replies, with mutually exclusive action fields.
func TestSupervisorLifecycleExamplesConformToRuntimeDecoder(t *testing.T) {
	for _, thread := range []bool{false, true} {
		messages, _ := supervisorMessagesWithLayout(nil, "current request", contextmgr.Selection{},
			skills.ContextAssembly{}, skills.ExternalContextAssembly{}, domain.RunModeSnapshot{}, thread)
		examples := regexp.MustCompile(`\{"version":"root_lifecycle\.v1"[^{}]*\}`).FindAllString(messages[0].Content, -1)
		if len(examples) == 0 {
			t.Fatal("root policy has no concrete lifecycle example")
		}
		for _, example := range examples {
			if _, err := parseRootAction(example); err != nil {
				t.Errorf("thread=%t root policy advertises an invalid lifecycle example %s: %v", thread, example, err)
			}
		}
	}
}
