package runner

import "context"

type commandRuntimeDispatchKey struct{}

func withCommandRuntimeDispatchCheck(ctx context.Context,
	check func(context.Context, CommandRuntimeResolvedSpec) error,
) context.Context {
	if check == nil {
		return ctx
	}
	// Jobs own their process lifetime independently of a request context, but
	// detaching that context must not erase cancellation before actual dispatch.
	bound := func(actual CommandRuntimeResolvedSpec) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := check(ctx, actual); err != nil {
			return err
		}
		return ctx.Err()
	}
	return context.WithValue(ctx, commandRuntimeDispatchKey{}, bound)
}

// CheckCommandRuntimeDispatch is used at native process/sandbox boundaries.
// The callback was injected by the host Start request, not decoded from input.
// Direct legacy runner callers retain their existing admission checks; the
// application always injects the common operation authorizer for new starts.
func CheckCommandRuntimeDispatch(ctx context.Context, actual CommandRuntimeResolvedSpec) error {
	if ctx == nil {
		return ErrCommandRuntimeBoundary
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if check, ok := ctx.Value(commandRuntimeDispatchKey{}).(func(CommandRuntimeResolvedSpec) error); ok {
		return check(actual)
	}
	return nil
}
