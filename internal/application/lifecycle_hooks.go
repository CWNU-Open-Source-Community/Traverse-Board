package application

import (
	"context"
	"errors"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/hooks"
)

// executeLifecycleBoundary translates the extension runtime's deliberately
// small error surface into stable application errors. Plugin-controlled hook
// messages never become public error text.
func executeLifecycleBoundary(ctx context.Context, engine *hooks.Engine,
	event hooks.Event, runID, workspaceID string, payload any,
) error {
	_, err := hooks.ExecuteBoundary(ctx, engine, hooks.Input{
		Event: event, RunID: runID, WorkspaceID: workspaceID,
	}, payload)
	if err == nil {
		return nil
	}
	var denied hooks.DeniedError
	if errors.As(err, &denied) {
		return apperror.New(apperror.CodePolicyDenied,
			"restricted lifecycle hook denied the operation")
	}
	return apperror.Wrap(apperror.CodeUnavailable,
		"restricted lifecycle hooks are unavailable", err)
}
