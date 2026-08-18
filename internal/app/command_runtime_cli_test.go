package app

import (
	"testing"

	"cyberagent-workbench/internal/apperror"
)

func TestCLICommandRuntimeRequiresBothProcessStartupGates(t *testing.T) {
	app := &App{}
	manager, service, err := app.newCLICommandRuntime(t.Context(), false, false)
	if err != nil || manager != nil || service != nil {
		t.Fatalf("default CLI unexpectedly installed command runtime: manager=%v service=%v err=%v",
			manager, service, err)
	}
	for _, gates := range [][2]bool{{true, false}, {false, true}} {
		if _, _, err := app.newCLICommandRuntime(t.Context(),
			gates[0], gates[1]); apperror.CodeOf(err) != apperror.CodeInvalidArgument {
			t.Fatalf("partial startup gates %v returned %v", gates, err)
		}
	}
}
