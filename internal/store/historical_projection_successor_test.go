package store

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/events"
	"cyberagent-workbench/internal/idgen"
	"cyberagent-workbench/internal/session"
)

// The v140 projection fixture needs two old Sessions in one Thread before its
// existing inverse chain runs. Reuse the frozen v1 graph seed; the live Thread
// service now creates v2 Ask tuples and is exercised separately on current DBs.
// This is deliberately limited to an unbound review Thread at the v177 prefix.
func seedHistoricalProjectionSuccessor(ctx context.Context, state *SQLiteStore,
	predecessor domain.Run,
) (domain.Run, error) {
	version, err := state.SchemaVersion(ctx)
	if err != nil {
		return domain.Run{}, err
	}
	if version != 177 {
		return domain.Run{}, fmt.Errorf("historical projection successor rejects schema %d", version)
	}
	thread, err := state.GetThreadByRun(ctx, predecessor.ID)
	if err != nil {
		return domain.Run{}, err
	}
	mission, err := state.GetMission(ctx, predecessor.MissionID)
	if err != nil {
		return domain.Run{}, err
	}
	if !predecessor.Terminal() || thread.LastRunID != predecessor.ID ||
		thread.ActiveRunID != "" || thread.Status != domain.ThreadActive ||
		mission.WorkspaceID != "" || mission.Profile != "review" {
		return domain.Run{}, fmt.Errorf("historical projection successor requires its terminal review fixture")
	}
	previousMode, err := state.GetRunMode(ctx, predecessor.ID)
	if err != nil {
		return domain.Run{}, err
	}
	now := time.Now().UTC()
	linked := session.New(mission.WorkspaceID, thread.Title, predecessor.Config.ModelRoute)
	linked.CreatedAt, linked.UpdatedAt = now, now
	candidate := domain.Run{ID: idgen.New("run"), MissionID: mission.ID,
		SessionID: linked.ID, Status: domain.RunCreated, Config: predecessor.Config,
		Budget: predecessor.Budget, CreatedAt: now, UpdatedAt: now}
	mode, err := domain.NewInitialRunModeSnapshot(idgen.New("run-mode"), candidate,
		mission, previousMode.Surface, previousMode.Phase, "test_operator",
		"historical Thread successor; runtime authority reset", now)
	if err != nil {
		return domain.Run{}, err
	}
	created, err := events.New(candidate.ID, mission.ID, events.RunCreatedEvent,
		"thread_service", candidate.ID, map[string]any{
			"status": candidate.Status, "profile": mission.Profile,
			"thread_id": thread.ID, "predecessor_run_id": predecessor.ID,
			"session_id": linked.ID, "runtime_authority_inherited": false,
		})
	if err != nil {
		return domain.Run{}, err
	}
	attached, err := events.New(candidate.ID, mission.ID, events.SessionAttachedEvent,
		"thread_service", linked.ID, map[string]any{
			"created": true, "route": linked.Route, "workspace_id": linked.WorkspaceID,
			"thread_id": thread.ID,
		})
	if err != nil {
		return domain.Run{}, err
	}
	created.CreatedAt, attached.CreatedAt = now, now
	tx, err := state.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Run{}, err
	}
	defer tx.Rollback()
	if err := seedLegacyRunGraphTx(ctx, tx, mission, candidate, mode, linked,
		true, false, []events.Event{created, attached}); err != nil {
		return domain.Run{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO thread_runs
        (thread_id, run_id, session_id, ordinal, predecessor_run_id, created_at)
        VALUES (?, ?, ?, 2, ?, ?)`, thread.ID, candidate.ID, linked.ID,
		predecessor.ID, ts(now)); err != nil {
		return domain.Run{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Run{}, err
	}
	return candidate, nil
}

func TestHistoricalProjectionSuccessorRejectsCurrentSchema(t *testing.T) {
	state := openRunSeedBoundaryStore(t, 0)
	before := runSeedBoundaryRows(t, state)
	if _, err := seedHistoricalProjectionSuccessor(t.Context(), state, domain.Run{}); err == nil || !strings.Contains(err.Error(), "rejects schema") {
		t.Fatalf("historical successor accepted current schema: %v", err)
	}
	if after := runSeedBoundaryRows(t, state); !reflect.DeepEqual(before, after) {
		t.Fatalf("rejected historical successor changed rows: before=%v after=%v", before, after)
	}
}
