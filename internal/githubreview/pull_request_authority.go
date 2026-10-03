package githubreview

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"

	"cyberagent-workbench/internal/toolcontract"
)

// DraftOperation binds the native, frozen request including exact branch SHAs,
// credential reference, reviewed text and durable idempotency marker. Publishing
// a draft is a shared remote write even when the Run preference is Full.
func DraftOperation(d PullRequestDraft) (toolcontract.Operation, error) {
	if err := d.Validate(); err != nil {
		return toolcontract.Operation{}, err
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return toolcontract.Operation{}, err
	}
	op := toolcontract.Operation{ID: "thread-pr-" + d.Marker, Kind: toolcontract.OperationToolCall,
		ToolID: "github.pull_request", Component: toolcontract.ComponentRef{PackageID: "traverse-board", ComponentID: "native-github"},
		AdapterID: "native-github", AdapterRevision: "1", InputFingerprint: Fingerprint("draft-input", string(raw)),
		CapabilityFingerprint: Fingerprint("draft-create-only"),
		Targets:               []toolcontract.Target{{Kind: "endpoint", Locator: "https://api.github.com" + repositoryAPIPath(d.Repository) + "/pulls"}},
		Effects:               []toolcontract.Effect{toolcontract.EffectPublicNetwork, toolcontract.EffectRemoteWrite}}
	return op, op.Validate()
}

type nativeWriteDispatchKey struct{}

type nativeWriteDispatch struct {
	guard       toolcontract.DispatchGuard
	fingerprint string
	posted      atomic.Bool
	mutation    atomic.Bool
}

type nativeWriteDispatchError struct {
	err   error
	state toolcontract.ReceiptState
}

func (e *nativeWriteDispatchError) Error() string { return e.err.Error() }
func (e *nativeWriteDispatchError) Unwrap() error { return e.err }

// DraftDispatchState distinguishes a locally blocked POST from a dispatched
// write whose response cannot establish its outcome. It never authorizes retry.
func DraftDispatchState(err error) (toolcontract.ReceiptState, bool) {
	return WriteDispatchState(err)
}

// WriteDispatchState also covers review writes. GraphQL observation POSTs do
// not mark a mutation as sent; a failed mutation response stays outcome_unknown.
func WriteDispatchState(err error) (toolcontract.ReceiptState, bool) {
	var result *nativeWriteDispatchError
	if errors.As(err, &result) {
		return result.state, true
	}
	return "", false
}

func bindDraftDispatch(ctx context.Context, d PullRequestDraft, guards []toolcontract.DispatchGuard) (context.Context, *nativeWriteDispatch, error) {
	op, err := DraftOperation(d)
	if err != nil {
		return nil, nil, err
	}
	return bindNativeWriteDispatch(ctx, op, guards, true)
}

// Draft creation and review writes share the credential/HTTP final boundary.
func bindNativeWriteDispatch(ctx context.Context, op toolcontract.Operation, guards []toolcontract.DispatchGuard, mutation bool) (context.Context, *nativeWriteDispatch, error) {
	if len(guards) == 0 {
		return ctx, nil, nil // Retained native callers keep their existing path.
	}
	if len(guards) != 1 || guards[0] == nil {
		return nil, nil, errors.New("native GitHub write requires exactly one host dispatch guard")
	}
	fingerprint, err := toolcontract.FingerprintOperation(op)
	if err != nil {
		return nil, nil, err
	}
	state := &nativeWriteDispatch{guard: guards[0], fingerprint: fingerprint}
	state.mutation.Store(mutation)
	return context.WithValue(ctx, nativeWriteDispatchKey{}, state), state, nil
}

func checkNativeWriteDispatch(ctx context.Context) error {
	if state, ok := ctx.Value(nativeWriteDispatchKey{}).(*nativeWriteDispatch); ok {
		if err := ctx.Err(); err != nil {
			return err
		}
		return state.guard(ctx, state.fingerprint)
	}
	return nil
}
