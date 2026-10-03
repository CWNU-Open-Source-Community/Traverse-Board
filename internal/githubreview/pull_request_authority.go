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

type draftDispatchKey struct{}

type draftDispatch struct {
	guard       toolcontract.DispatchGuard
	fingerprint string
	posted      atomic.Bool
}

type draftDispatchError struct {
	err   error
	state toolcontract.ReceiptState
}

func (e *draftDispatchError) Error() string { return e.err.Error() }
func (e *draftDispatchError) Unwrap() error { return e.err }

// DraftDispatchState distinguishes a locally blocked POST from a dispatched
// write whose response cannot establish its outcome. It never authorizes retry.
func DraftDispatchState(err error) (toolcontract.ReceiptState, bool) {
	var result *draftDispatchError
	if errors.As(err, &result) {
		return result.state, true
	}
	return "", false
}

func bindDraftDispatch(ctx context.Context, d PullRequestDraft, guards []toolcontract.DispatchGuard) (context.Context, *draftDispatch, error) {
	if len(guards) == 0 {
		return ctx, nil, nil // Retained native callers keep their existing path.
	}
	if len(guards) != 1 || guards[0] == nil {
		return nil, nil, errors.New("draft creation requires exactly one host dispatch guard")
	}
	op, err := DraftOperation(d)
	if err != nil {
		return nil, nil, err
	}
	fingerprint, err := toolcontract.FingerprintOperation(op)
	if err != nil {
		return nil, nil, err
	}
	state := &draftDispatch{guard: guards[0], fingerprint: fingerprint}
	return context.WithValue(ctx, draftDispatchKey{}, state), state, nil
}

func checkDraftDispatch(ctx context.Context) error {
	if state, ok := ctx.Value(draftDispatchKey{}).(*draftDispatch); ok {
		if err := ctx.Err(); err != nil {
			return err
		}
		return state.guard(ctx, state.fingerprint)
	}
	return nil
}
