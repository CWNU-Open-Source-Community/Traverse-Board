package app

import (
	"context"
	"fmt"
	"strings"
)

func (a *App) onceCommandCommand(ctx context.Context, args []string) error {
	if len(args) != 2 || args[0] != "proposals" {
		return fmt.Errorf("usage: cyberagent once-command proposals <run-id>; new commands use run host-execute or command_runtime")
	}
	if err := a.ensureStore(); err != nil {
		return err
	}
	values, err := a.store.ListOnceCommandProposals(ctx, strings.TrimSpace(args[1]), 50)
	if err != nil {
		return err
	}
	for _, proposal := range values {
		fmt.Fprintf(a.out, "%s\tstatus=%s\texecutable=%s\targv=%s\tpurpose=%s\n", proposal.ID, proposal.Status, proposal.ExecutablePath, strings.Join(proposal.Argv, " "), proposal.Purpose)
	}
	_, err = fmt.Fprintf(a.out, "proposal_count: %d\n", len(values))
	return err
}
