package repository

import (
	"context"
	"encoding/json"
	"errors"

	"cyberagent-workbench/internal/gitadvanced"
	"cyberagent-workbench/internal/toolcontract"
)

func nativeThreadGitOperation(kind string, input any, root, target string, remote bool) (toolcontract.Operation, error) {
	raw, err := json.Marshal(struct {
		Root  string
		Input any
	}{root, input})
	if err != nil {
		return toolcontract.Operation{}, err
	}
	fingerprint := gitadvanced.Fingerprint("native-thread-git-input", kind, string(raw))
	operation := toolcontract.Operation{ID: "thread-git-" + fingerprint, Kind: toolcontract.OperationProcess,
		ToolID: "thread.git", Component: toolcontract.ComponentRef{PackageID: "traverse-board", ComponentID: "native-git"},
		AdapterID: "native-thread-git", AdapterRevision: "1", InputFingerprint: fingerprint,
		Targets: []toolcontract.Target{{Kind: "directory", Locator: target}},
		Effects: []toolcontract.Effect{toolcontract.EffectProcess, toolcontract.EffectUnknown}}
	if remote {
		operation.Targets[0].Kind = "endpoint"
		operation.Effects = append(operation.Effects, toolcontract.EffectPublicNetwork, toolcontract.EffectRemoteWrite)
	}
	return operation, operation.Validate()
}

func SelectedCommitOperation(root string, review SelectedGitReview, message, marker string, author GitCommitAuthor) (toolcontract.Operation, error) {
	return nativeThreadGitOperation("commit", struct {
		Review          SelectedGitReview
		Message, Marker string
		Author          GitCommitAuthor
	}{review, message, marker, author}, root, "repository:"+review.Binding.RepositorySHA256, false)
}

func SelectedIndexOperation(root string, review SelectedGitReview, unstage bool) (toolcontract.Operation, error) {
	return nativeThreadGitOperation("index", struct {
		Review  SelectedGitReview
		Unstage bool
	}{review, unstage}, root, "repository:"+review.Binding.RepositorySHA256, false)
}

func ThreadBranchOperation(root string, spec MutationSpec, binding gitadvanced.RepositoryBinding, target string) (toolcontract.Operation, error) {
	return nativeThreadGitOperation("branch", struct {
		Spec    MutationSpec
		Binding gitadvanced.RepositoryBinding
		Target  string
	}{spec, binding, target}, root, "repository:"+binding.RepositorySHA256, false)
}

func ThreadRemoteOperation(root string, spec RemoteSpec, binding RemoteBinding, key string) (toolcontract.Operation, error) {
	return nativeThreadGitOperation("remote", struct {
		Spec    RemoteSpec
		Binding RemoteBinding
		Key     string
	}{spec, binding, key}, root, spec.RemoteURL, true)
}

type threadGitDispatchKey struct{}

func threadGitDispatchContext(ctx context.Context, operation toolcontract.Operation, guards []toolcontract.DispatchGuard) (context.Context, error) {
	if len(guards) == 0 {
		return ctx, nil
	}
	if len(guards) != 1 || guards[0] == nil {
		return nil, errors.New("Thread Git requires exactly one native dispatch guard")
	}
	fingerprint, err := toolcontract.FingerprintOperation(operation)
	if err != nil {
		return nil, err
	}
	check := func(current context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return guards[0](current, fingerprint)
	}
	return context.WithValue(ctx, threadGitDispatchKey{}, check), nil
}

func checkThreadGitDispatch(ctx context.Context) error {
	if check, ok := ctx.Value(threadGitDispatchKey{}).(func(context.Context) error); ok {
		if err := ctx.Err(); err != nil {
			return err
		}
		return check(ctx)
	}
	return nil
}

func freezeSelectedReview(review SelectedGitReview) SelectedGitReview {
	review.Files = append([]SelectedGitFile(nil), review.Files...)
	for i := range review.Files {
		review.Files[i].Content = append([]byte(nil), review.Files[i].Content...)
	}
	return review
}

// The retained native entry accepts optional author data; this explicit entry
// additionally freezes the selected bytes and binds the common dispatch guard.
func (e *MutationExecutor) PrepareSelectedCommitAuthorized(ctx context.Context, root string, review SelectedGitReview, message, marker string, author GitCommitAuthor, guard toolcontract.DispatchGuard) (*PreparedSelectedCommit, error) {
	review = freezeSelectedReview(review)
	operation, err := SelectedCommitOperation(root, review, message, marker, author)
	if err != nil {
		return nil, err
	}
	ctx, err = threadGitDispatchContext(ctx, operation, []toolcontract.DispatchGuard{guard})
	if err != nil {
		return nil, err
	}
	return e.PrepareSelectedCommit(ctx, root, review, message, marker, author)
}
