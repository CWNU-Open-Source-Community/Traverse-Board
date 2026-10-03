package runner

import (
	"context"
	"encoding/json"
	"path/filepath"
	"time"
)

const RestrictedFixedCommandBackend = "windows-fixed-restricted.v1"

type commandRuntimeOperatorPreparationKey struct{}
type commandRuntimeOperatorPreparation struct{ jobID, fingerprint string }

// OperatorCommandJobPrepared is a store-side provenance check, not an approval.
// Only Start, after its host DispatchCheck succeeds, creates this private value.
// Serialized actor strings and saved SQLite rows cannot recreate it.
func OperatorCommandJobPrepared(ctx context.Context, job CommandRuntimeJob) bool {
	if ctx == nil || ctx.Err() != nil {
		return false
	}
	proof, ok := ctx.Value(commandRuntimeOperatorPreparationKey{}).(commandRuntimeOperatorPreparation)
	return ok && proof.jobID == job.ID && proof.fingerprint == job.RequestFingerprint
}

// A closed plan supplies native launch details only. The ordinary manager,
// operator authority and Job ledger still own execution, cancellation and replay.
type fixedCommandRuntime struct {
	plan   ControlledCommandPlan
	root   string
	intent CommandRuntimeSpec
}

func NewFixedCommandRuntimeManager(store CommandRuntimeStore, owner string, plan ControlledCommandPlan,
	root string,
) (*CommandRuntimeManager, CommandRuntimeSpec, error) {
	if plan.Validate() != nil || plan.WorkspaceRootSHA256 != commandRuntimeStringSHA256(filepath.Clean(root)) {
		return nil, CommandRuntimeSpec{}, ErrControlledExecutionBoundary
	}
	plan.Argv = append([]string{}, plan.Argv...)
	fixed := &fixedCommandRuntime{plan: plan, root: filepath.Clean(root)}
	starter, intent, err := newFixedCommandRuntimeStarter(fixed)
	if err != nil {
		return nil, CommandRuntimeSpec{}, err
	}
	fixed.intent, err = NormalizeCommandRuntimeIntent(intent)
	if err != nil {
		return nil, CommandRuntimeSpec{}, err
	}
	manager, err := NewCommandRuntimeManager(store, starter, owner)
	if err != nil {
		return nil, CommandRuntimeSpec{}, err
	}
	manager.fixed = fixed
	manager.adapter.BackendIdentity = RestrictedFixedCommandBackend
	intent = fixed.intent
	intent.Arguments = append(intent.Arguments[:0:0], intent.Arguments...)
	intent.Environment = append([]CommandRuntimeEnvironment{}, intent.Environment...)
	return manager, intent, nil
}

func (m *CommandRuntimeManager) FixedCommandPlan() (ControlledCommandPlan, bool) {
	if m == nil || m.fixed == nil {
		return ControlledCommandPlan{}, false
	}
	plan := m.fixed.plan
	plan.Argv = append([]string{}, plan.Argv...)
	return plan, true
}

func (f *fixedCommandRuntime) startSpec() ControlledStartSpec {
	return ControlledStartSpec{RequestID: ControlledExecutionRequestID(f.plan), PlanID: f.plan.ID,
		PlanFingerprint: f.plan.Fingerprint, ExecutableID: f.plan.ExecutableID,
		Argv: append([]string{}, f.plan.Argv...), WorkspaceRoot: f.root,
		Timeout: time.Duration(f.plan.TimeoutMilliseconds) * time.Millisecond}
}

func (f *fixedCommandRuntime) matchesIntent(spec CommandRuntimeSpec, root string) bool {
	if f == nil || filepath.Clean(root) != f.root || f.plan.Validate() != nil {
		return false
	}
	normalized, err := NormalizeCommandRuntimeIntent(spec)
	if err != nil {
		return false
	}
	actual, _ := json.Marshal(normalized)
	expected, _ := json.Marshal(f.intent)
	return string(actual) == string(expected)
}
