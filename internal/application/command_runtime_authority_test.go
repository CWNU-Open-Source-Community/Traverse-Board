package application

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/commandruntimeadapter"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/runner"
	"cyberagent-workbench/internal/store"
	"cyberagent-workbench/internal/toolgateway"
)

type commandRuntimeBindingReadStore struct {
	*store.SQLiteStore
	reads map[string]int
}

func (s *commandRuntimeBindingReadStore) GetRun(ctx context.Context, id string) (domain.Run, error) {
	s.reads["run"]++
	return s.SQLiteStore.GetRun(ctx, id)
}

func (s *commandRuntimeBindingReadStore) GetMission(ctx context.Context, id string) (domain.Mission, error) {
	s.reads["mission"]++
	return s.SQLiteStore.GetMission(ctx, id)
}

func (s *commandRuntimeBindingReadStore) GetRootAgent(ctx context.Context, id string) (domain.AgentNode, bool, error) {
	s.reads["root"]++
	return s.SQLiteStore.GetRootAgent(ctx, id)
}

func (s *commandRuntimeBindingReadStore) GetRunExecutionPermission(ctx context.Context, id string) (domain.RunExecutionPermissionSnapshot, error) {
	s.reads["permission"]++
	return s.SQLiteStore.GetRunExecutionPermission(ctx, id)
}

func (s *commandRuntimeBindingReadStore) GetRunExecutionLease(ctx context.Context, id string) (domain.RunExecutionLease, bool, error) {
	s.reads["lease"]++
	return s.SQLiteStore.GetRunExecutionLease(ctx, id)
}

func TestCommandRuntimeAuthorityBindingReadsOnceAndReloadsReleasedLease(t *testing.T) {
	ctx := t.Context()
	state, run, root, lease, capabilities := newCommandRuntimeTestRuntime(t, ctx)
	manager, err := runner.NewPlatformCommandRuntimeManager(state, "command-binding-read-owner")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := manager.Shutdown(shutdownCtx); err != nil {
			t.Error(err)
		}
	})
	probe := &commandRuntimeBindingReadStore{SQLiteStore: state, reads: make(map[string]int)}
	service, err := NewCommandRuntimeService(probe, manager, capabilities)
	if err != nil {
		t.Fatal(err)
	}
	scope := commandRuntimeTestScope(t, ctx, state, service, run, root, lease, "binding-read-count")
	authority, err := commandruntimeadapter.EncodeAuthority(commandruntimeadapter.Authority{
		ProtocolVersion: commandruntimeadapter.OperationAuthorityVersion,
		RunID:           run.ID, Adapter: service.adapter,
		PermissionSnapshotID: scope.PermissionSnapshotID, PermissionMode: scope.PermissionMode,
		PermissionRevision: scope.PermissionRevision, PermissionGeneration: scope.PermissionGeneration,
		PermissionRuntimeEpoch: scope.PermissionRuntimeEpoch, RunAuthorizationFence: scope.RunAuthorizationFence,
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(toolgateway.CommandRuntimeInput{
		Version: toolgateway.CommandRuntimeToolProtocolVersion, Action: toolgateway.CommandRuntimeActionList,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, payload, err = toolgateway.NormalizeCommandRuntimePayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	assertReads := func(want int) {
		t.Helper()
		for _, name := range []string{"run", "mission", "root", "permission", "lease"} {
			if got := probe.reads[name]; got != want {
				t.Fatalf("%s binding reads=%d want=%d", name, got, want)
			}
		}
	}
	bound, err := service.BindCommandRuntimeAuthority(ctx, authority, payload)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := commandruntimeadapter.DecodeAuthority(bound)
	if err != nil || prepared.ScopeFingerprint == "" {
		t.Fatalf("authority was not bound: %+v err=%v", prepared, err)
	}
	assertReads(1)

	// A later binding must reload durable state instead of retaining this snapshot.
	if _, _, err := state.ReleaseRunExecutionLease(ctx, lease); err != nil {
		t.Fatal(err)
	}
	if _, err := service.BindCommandRuntimeAuthority(ctx, bound, payload); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("released lease remained bound: %v", err)
	}
	assertReads(2)
}
