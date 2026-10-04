package repository

import (
	"context"
	"encoding/json"
	"errors"

	"cyberagent-workbench/internal/gitadvanced"
	"cyberagent-workbench/internal/toolcontract"
)

// AdvancedOperation describes the exact native preview, including its closed
// command recipe and repository evidence. Host Git is not an OS sandbox, so a
// command template or cwd must not establish verified reversible effects.
func AdvancedOperation(preview gitadvanced.Preview) (toolcontract.Operation, error) {
	raw, err := json.Marshal(preview)
	if err != nil {
		return toolcontract.Operation{}, err
	}
	operation := toolcontract.Operation{
		ID: preview.ID, Kind: toolcontract.OperationProcess, ToolID: gitadvanced.ApprovalToolName,
		Component: toolcontract.ComponentRef{PackageID: "traverse-board", ComponentID: "native-git"},
		AdapterID: "native-git", AdapterRevision: preview.Capability.Generation,
		InputFingerprint: gitadvanced.Fingerprint("native-git-operation", string(raw)),
		Targets:          []toolcontract.Target{{Kind: "directory", Locator: "repository:" + preview.Binding.RepositorySHA256}},
		Effects:          []toolcontract.Effect{toolcontract.EffectProcess, toolcontract.EffectUnknown},
	}
	return operation, operation.Validate()
}

type advancedDispatchKey struct{}

// The guard is attached only during execution, not preview or post-state
// inspection. Recovery must still observe partial effects after revocation.
func advancedDispatchContext(ctx context.Context, preview gitadvanced.Preview,
	guards []toolcontract.DispatchGuard,
) (context.Context, error) {
	if len(guards) == 0 {
		return ctx, nil // Existing native callers retain their own authority path.
	}
	if len(guards) != 1 || guards[0] == nil {
		return nil, errors.New("Git dispatch requires exactly one host guard")
	}
	operation, err := AdvancedOperation(preview)
	if err != nil {
		return nil, err
	}
	fingerprint, err := toolcontract.FingerprintOperation(operation)
	if err != nil {
		return nil, err
	}
	check := func(checkCtx context.Context) error {
		// Some native cleanup steps use WithoutCancel. They must not recover
		// cancelled authority to perform another mutation.
		if err := ctx.Err(); err != nil {
			return err
		}
		return guards[0](checkCtx, fingerprint)
	}
	return context.WithValue(ctx, advancedDispatchKey{}, check), nil
}

func checkAdvancedDispatch(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if check, ok := ctx.Value(advancedDispatchKey{}).(func(context.Context) error); ok {
		return check(ctx)
	}
	return nil
}
