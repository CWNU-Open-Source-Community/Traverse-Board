package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/sandbox"
)

func removeSchemaV132ForTestStatements() []string {
	createActions := requireMigrationStatement(
		"CREATE TABLE sandbox_docker_lifecycle_actions (",
		sandboxDockerLifecycleStatements)
	createActions = replaceDockerLifecycleStdinMigrationFragment(createActions,
		"CREATE TABLE sandbox_docker_lifecycle_actions (",
		"CREATE TABLE sandbox_docker_lifecycle_actions_v131 (")
	return append(removeSchemaV133ForTestStatements(), []string{
		`DROP TRIGGER trg_sandbox_docker_lifecycle_action_insert`,
		`DROP TRIGGER trg_sandbox_docker_lifecycle_action_update_immutable`,
		`DROP TRIGGER trg_sandbox_docker_lifecycle_action_delete_immutable`,
		`DROP TRIGGER trg_sandbox_docker_lifecycle_transition_insert`,
		`DROP TRIGGER trg_sandbox_docker_lifecycle_cleanup_receipt_insert`,
		createActions,
		`INSERT INTO sandbox_docker_lifecycle_actions_v131
			SELECT * FROM sandbox_docker_lifecycle_actions`,
		`DROP TABLE sandbox_docker_lifecycle_actions`,
		`ALTER TABLE sandbox_docker_lifecycle_actions_v131
			RENAME TO sandbox_docker_lifecycle_actions`,
		requireMigrationTrigger("trg_sandbox_docker_lifecycle_action_insert",
			sandboxDockerLifecycleStatements),
		requireMigrationTrigger("trg_sandbox_docker_lifecycle_action_update_immutable",
			sandboxDockerLifecycleStatements),
		requireMigrationTrigger("trg_sandbox_docker_lifecycle_action_delete_immutable",
			sandboxDockerLifecycleStatements),
		requireMigrationTrigger("trg_sandbox_docker_lifecycle_transition_insert",
			sandboxDockerLifecycleStatements),
		requireMigrationTrigger("trg_sandbox_docker_lifecycle_cleanup_receipt_insert",
			legacyDockerLifecycleCleanupTriggerCompatibilityStatements),
		`DELETE FROM schema_migrations WHERE version = 132`,
	}...)
}

func TestSchemaV132PreservesLifecycleActionsAndFencesStdinAttach(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "docker-stdin-v131.db")
	state, runRecord, root := openSandboxManifestStoreAt(t, ctx, path)
	intent, request := newDockerContainerLifecycleStoreIntent(t, ctx, state,
		runRecord.ID, root, "docker-stdin-v132")
	record, _, err := state.BeginDockerContainerLifecycle(ctx, intent,
		"docker_stdin_migration_owner", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	stage, err := sandbox.NewDockerContainerStageResult(mustDockerLifecycleEndpoint(t),
		request, strings.Repeat("d", 64), false)
	if err != nil {
		t.Fatal(err)
	}
	nextAt := record.Lease.RenewedAt.Add(time.Millisecond)
	appendAction := func(verb string) {
		t.Helper()
		action, actionErr := sandbox.NewDockerContainerLifecyclePreparedAction(intent.ID,
			len(record.Actions)+1, record.Lease, verb, nextAt)
		if actionErr != nil {
			t.Fatal(actionErr)
		}
		record, _, actionErr = state.PrepareDockerContainerLifecycleAction(ctx,
			action, record.Lease)
		if actionErr != nil {
			t.Fatal(actionErr)
		}
		nextAt = nextAt.Add(time.Millisecond)
	}
	appendTransition := func(stateValue, reason string) {
		t.Helper()
		previous := ""
		if len(record.Transitions) > 0 {
			previous = record.Transitions[len(record.Transitions)-1].TransitionFingerprint
		}
		transition, transitionErr := sandbox.NewDockerContainerLifecycleTransition(
			intent.ID, len(record.Transitions)+1, record.Lease, stateValue, reason, nil,
			stage.ContainerIDFingerprint, previous, nextAt)
		if transitionErr != nil {
			t.Fatal(transitionErr)
		}
		record, _, transitionErr = state.AppendDockerContainerLifecycleTransition(ctx,
			transition, record.Lease)
		if transitionErr != nil {
			t.Fatal(transitionErr)
		}
		nextAt = nextAt.Add(time.Millisecond)
	}
	appendAction(string(sandbox.DockerContainerLifecycleActionCreate))
	appendTransition(sandbox.DockerContainerLifecycleTransitionCreated,
		sandbox.DockerContainerLifecycleReasonCreated)
	appendAction(string(sandbox.DockerContainerLifecycleActionStart))
	appendTransition(sandbox.DockerContainerLifecycleTransitionStarted,
		sandbox.DockerContainerLifecycleReasonStarted)
	original := append([]sandbox.DockerContainerLifecyclePreparedAction(nil),
		record.Actions...)

	for _, statement := range removeSchemaV132ForTestStatements() {
		if _, err := state.db.ExecContext(ctx, statement); err != nil {
			state.Close()
			t.Fatalf("restore schema v131: %v\n%s", err, statement)
		}
	}
	if version, err := state.SchemaVersion(ctx); err != nil || version != 131 {
		state.Close()
		t.Fatalf("restored schema version=%d want=131 err=%v", version, err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	loaded, err := upgraded.GetDockerContainerLifecycle(ctx, intent.ID)
	if err != nil || len(loaded.Actions) != len(original) {
		t.Fatalf("v132 lifecycle actions=%#v err=%v", loaded.Actions, err)
	}
	for index := range original {
		if loaded.Actions[index] != original[index] {
			t.Fatalf("v132 changed action %d: before=%#v after=%#v",
				index, original[index], loaded.Actions[index])
		}
	}
	attach, err := sandbox.NewDockerContainerLifecyclePreparedAction(intent.ID,
		len(loaded.Actions)+1, loaded.Lease,
		string(sandbox.DockerContainerLifecycleActionAttachStdin), nextAt)
	if err != nil {
		t.Fatal(err)
	}
	attached, replayed, err := upgraded.PrepareDockerContainerLifecycleAction(ctx,
		attach, loaded.Lease)
	if err != nil || replayed || len(attached.Actions) != len(original)+1 ||
		attached.Actions[len(attached.Actions)-1].Verb !=
			string(sandbox.DockerContainerLifecycleActionAttachStdin) {
		t.Fatalf("v132 stdin action=%#v replayed=%t err=%v",
			attached.Actions, replayed, err)
	}
	if _, replayed, err := upgraded.PrepareDockerContainerLifecycleAction(ctx,
		attach, loaded.Lease); err != nil || !replayed {
		t.Fatalf("v132 exact stdin action replay=%t err=%v", replayed, err)
	}
	assertNoForeignKeyViolations(t, upgraded.db)
}
