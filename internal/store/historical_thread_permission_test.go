package store

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/runmutation"
)

// Seed the old Workspace Access preference through the real Store transaction
// and historical SQL. This fixture cannot submit five-mode writes to a current
// database; current tests use the public three-mode service.
func seedHistoricalThreadWorkspacePreference(ctx context.Context, state *SQLiteStore,
	threadID, operationKey, requestedBy, reason string,
) (domain.ThreadExecutionPermissionSnapshot, domain.ThreadExecutionPermissionOperation, error) {
	version, err := state.SchemaVersion(ctx)
	if err != nil {
		return domain.ThreadExecutionPermissionSnapshot{}, domain.ThreadExecutionPermissionOperation{}, err
	}
	if version < 146 || version >= 178 {
		return domain.ThreadExecutionPermissionSnapshot{}, domain.ThreadExecutionPermissionOperation{},
			fmt.Errorf("historical Thread preference fixture rejects schema %d", version)
	}
	current, err := state.GetThreadExecutionPermission(ctx, threadID)
	if err != nil {
		return domain.ThreadExecutionPermissionSnapshot{}, domain.ThreadExecutionPermissionOperation{}, err
	}
	now := time.Now().UTC()
	if now.Before(current.CreatedAt) {
		now = current.CreatedAt
	}
	next, err := current.Next(idgen.New("historical-thread-preference"),
		domain.RunExecutionPermissionWorkspaceAccess, true, requestedBy, reason, now)
	if err != nil {
		return domain.ThreadExecutionPermissionSnapshot{}, domain.ThreadExecutionPermissionOperation{}, err
	}
	operation := domain.ThreadExecutionPermissionOperation{
		KeyDigest:          runmutation.Fingerprint("thread_execution_permission_operation.v1", threadID, operationKey),
		RequestFingerprint: threadExecutionPermissionRequestFingerprint(next),
		SnapshotID:         next.ID, ThreadID: threadID, RequestedBy: next.RequestedBy, CreatedAt: next.CreatedAt,
	}
	stored, recorded, _, err := state.TransitionThreadExecutionPermission(ctx, next, operation)
	return stored, recorded, err
}

func TestHistoricalThreadWorkspacePreferenceRejectsCurrentSchema(t *testing.T) {
	for _, version := range []int{178, 0} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			state := openRunSeedBoundaryStore(t, version)
			_, run, err := application.NewRunService(state).Create(t.Context(), application.CreateRunRequest{
				Goal: "current Thread preference fixture boundary", Budget: domain.Budget{MaxTurns: 2},
			})
			if err != nil {
				t.Fatal(err)
			}
			before := runSeedBoundaryRows(t, state)
			_, _, err = seedHistoricalThreadWorkspacePreference(t.Context(), state, domain.InitialThreadID(run.ID),
				"historical-thread-boundary-0001", "operator", "reject an old preference on current schema")
			if err == nil || !strings.Contains(err.Error(), "historical Thread preference fixture rejects schema") {
				t.Fatalf("historical Thread fixture did not reject current schema: %v", err)
			}
			if after := runSeedBoundaryRows(t, state); !reflect.DeepEqual(before, after) {
				t.Fatalf("rejected historical Thread fixture left rows: before=%v after=%v", before, after)
			}
			assertRunSeedPermission(t, state, run, false)
		})
	}
}
