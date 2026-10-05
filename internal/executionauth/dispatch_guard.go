package executionauth

import (
	"context"
	"errors"
	"sync"

	"cyberagent-workbench/internal/toolcontract"
)

// NewRecheckingDispatchGuard consumes the initial dispatch decision once and
// checks live authority before later steps of the same native operation. All
// checks are serialized, and an input mismatch or denied check locks the guard.
// Callers retain ownership of the native authority checks and denial message.
func NewRecheckingDispatchGuard(expectedFingerprint string, beforeDispatch toolcontract.DispatchGuard,
	recheck func(context.Context) error, denialMessage string,
) toolcontract.DispatchGuard {
	var mu sync.Mutex
	started, denied := false, false
	return func(ctx context.Context, actualFingerprint string) (err error) {
		mu.Lock()
		defer mu.Unlock()
		if denied || actualFingerprint != expectedFingerprint {
			denied = true
			return errors.New(denialMessage)
		}
		defer func() { denied = err != nil }()
		if !started {
			started = true
			return beforeDispatch(ctx, actualFingerprint)
		}
		return recheck(ctx)
	}
}
