package store

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/idgen"
)

// The host-child migration needs old permission snapshots, not fabricated
// selection operations. Insert those snapshots under the actual old SQL guards;
// current-schema host audit tests still select Full through the public service.
func seedHistoricalHostPermission(ctx context.Context, state *SQLiteStore,
	runID string, mode domain.RunExecutionPermissionMode,
) (domain.RunExecutionPermissionSnapshot, error) {
	version, err := state.SchemaVersion(ctx)
	if err != nil {
		return domain.RunExecutionPermissionSnapshot{}, err
	}
	if version != 141 && version != 142 {
		return domain.RunExecutionPermissionSnapshot{}, fmt.Errorf("historical host permission fixture rejects schema %d", version)
	}
	if mode != domain.RunExecutionPermissionFullAccess && mode != domain.RunExecutionPermissionDebug {
		return domain.RunExecutionPermissionSnapshot{}, fmt.Errorf("historical host permission fixture rejects mode %q", mode)
	}
	current, err := state.GetRunExecutionPermission(ctx, runID)
	if err != nil {
		return domain.RunExecutionPermissionSnapshot{}, err
	}
	now := time.Now().UTC()
	if now.Before(current.CreatedAt) {
		now = current.CreatedAt
	}
	next, err := current.Next(idgen.New("historical-host-permission"), mode, true,
		"test_operator", "historical host execution child fixture", now)
	if err != nil {
		return domain.RunExecutionPermissionSnapshot{}, err
	}
	tx, err := state.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.RunExecutionPermissionSnapshot{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := insertRunExecutionPermissionSnapshotTx(ctx, tx, next); err != nil {
		return domain.RunExecutionPermissionSnapshot{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.RunExecutionPermissionSnapshot{}, err
	}
	return next, nil
}

func TestHistoricalHostPermissionRejectsCurrentSchema(t *testing.T) {
	for _, version := range []int{178, 0} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			state := openRunSeedBoundaryStore(t, version)
			before := runSeedBoundaryRows(t, state)
			for _, mode := range []domain.RunExecutionPermissionMode{domain.RunExecutionPermissionFullAccess, domain.RunExecutionPermissionDebug} {
				if _, err := seedHistoricalHostPermission(t.Context(), state, "unused-run", mode); err == nil || !strings.Contains(err.Error(), "historical host permission fixture rejects schema") {
					t.Fatalf("historical host fixture did not reject current schema: %v", err)
				}
			}
			if after := runSeedBoundaryRows(t, state); !reflect.DeepEqual(before, after) {
				t.Fatalf("rejected historical host fixture left rows: before=%v after=%v", before, after)
			}
		})
	}
}
