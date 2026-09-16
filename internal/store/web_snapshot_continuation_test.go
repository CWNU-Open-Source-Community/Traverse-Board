package store

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/webevidence"
)

type predecessorSnapshotFixture struct {
	state                *SQLiteStore
	mission              domain.Mission
	first, second, third domain.Run
	thread               domain.Thread
	source               webevidence.Source
	snapshot             webevidence.Snapshot
}

func newPredecessorSnapshotFixture(t *testing.T) predecessorSnapshotFixture {
	t.Helper()
	ctx := t.Context()
	state := openStructuredToolTestStore(t)
	runs := application.NewRunService(state)
	mission, first, err := runs.Create(ctx, application.CreateRunRequest{
		Goal: "read a previous official document", Profile: "review", WorkspaceID: "workspace-evidence-history",
		Budget: domain.Budget{MaxTurns: 4}})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	source, err := webevidence.SealSource(webevidence.Source{ID: "source-history", RunID: first.ID,
		MissionID: mission.ID, WorkspaceID: mission.WorkspaceID, CanonicalURL: "https://docs.example.com/guide",
		Title: "Saved guide", Provider: "direct", State: webevidence.SourceFetched, DiscoveredAt: at})
	if err != nil {
		t.Fatal(err)
	}
	body := "原始🙂 saved guide"
	snapshot, err := webevidence.SealSnapshot(webevidence.Snapshot{ID: "snapshot-history", SourceID: source.ID,
		RunID: first.ID, MissionID: mission.ID, RequestedURL: source.CanonicalURL, FinalURL: source.CanonicalURL,
		FetchedAt: at, StaleAt: at.Add(time.Hour), Digest: webevidence.DigestBytes([]byte(body)),
		MIME: "text/plain", Body: body, State: webevidence.SourceFetched, Robots: "allowed", Provider: "direct"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := state.SaveWebFetch(ctx, source, snapshot,
		webOperation(t, first.ID, "web_fetch", "history-fetch", map[string]string{"status": "fetched"}, at)); err != nil {
		t.Fatal(err)
	}
	thread, err := state.GetThreadByRun(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	previous := first
	successors := []domain.Run{}
	for i := 0; i < 2; i++ {
		if _, err := runs.Cancel(ctx, previous.ID); err != nil {
			t.Fatal(err)
		}
		next, err := application.NewThreadService(state).Submit(ctx, application.SubmitThreadMessageRequest{
			Version: domain.ThreadMessageProtocolVersion, ThreadID: thread.ID, Content: "continue reading",
			OperationKey: fmt.Sprintf("snapshot-history-next-%d", i), RequestedBy: "test_operator"})
		if err != nil {
			t.Fatal(err)
		}
		successors = append(successors, next.Run)
		previous = next.Run
	}
	return predecessorSnapshotFixture{state, mission, first, successors[0], successors[1], thread, source, snapshot}
}

func (f predecessorSnapshotFixture) read(ctx context.Context) (webevidence.Source, webevidence.Snapshot, error) {
	return f.state.GetThreadPredecessorWebSnapshot(ctx, f.third.ID, f.mission.ID,
		f.mission.WorkspaceID, f.source.ID, f.snapshot.ID)
}

func TestThreadPredecessorWebSnapshotReadsExactHistoryWithoutMutation(t *testing.T) {
	f := newPredecessorSnapshotFixture(t)
	ctx := t.Context()
	var before, after int
	if err := f.state.db.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		source, snapshot, err := f.read(ctx)
		if err != nil || !reflect.DeepEqual(source, f.source) || !reflect.DeepEqual(snapshot, f.snapshot) {
			t.Fatalf("historical evidence identity/content changed: source=%+v snapshot=%+v err=%v", source, snapshot, err)
		}
	}
	if err := f.state.db.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&after); err != nil || before != after {
		t.Fatalf("read changed the database: before=%d after=%d err=%v", before, after, err)
	}
	if _, err := f.state.GetWebSource(ctx, f.third.ID, f.source.ID); apperror.CodeOf(err) != apperror.CodeNotFound {
		t.Fatal("historical paging expanded ordinary current-Run source access")
	}
	if _, err := f.state.GetWebSnapshot(ctx, f.third.ID, f.snapshot.ID); apperror.CodeOf(err) != apperror.CodeNotFound {
		t.Fatal("historical paging expanded ordinary current-Run snapshot access")
	}
}

func TestThreadPredecessorWebSnapshotRejectsScopeAndForgedContinuation(t *testing.T) {
	for _, test := range []string{"other thread", "other workspace", "other mission", "wrong snapshot",
		"duplicate receipt", "forged predecessor", "nonterminal ancestor"} {
		t.Run(test, func(t *testing.T) {
			f := newPredecessorSnapshotFixture(t)
			ctx := t.Context()
			runID, missionID, workspaceID, snapshotID := f.third.ID, f.mission.ID, f.mission.WorkspaceID, f.snapshot.ID
			switch test {
			case "other thread":
				mission, run, err := application.NewRunService(f.state).Create(ctx, application.CreateRunRequest{
					Goal: "unrelated task", Profile: "review", WorkspaceID: f.mission.WorkspaceID})
				if err != nil {
					t.Fatal(err)
				}
				runID, missionID = run.ID, mission.ID
			case "other workspace":
				workspaceID = "workspace-unrelated"
			case "other mission":
				missionID = "mission-unrelated"
			case "wrong snapshot":
				snapshotID = "snapshot-unrelated"
			case "duplicate receipt":
				if _, err := f.state.db.ExecContext(ctx, `INSERT INTO thread_events
					(thread_id,run_id,type,source,payload_json,created_at)
					VALUES (?,?,'thread.run_successor_created','thread_continuation','{}',?)`,
					f.thread.ID, f.third.ID, ts(time.Now().UTC())); err != nil {
					t.Fatal(err)
				}
			case "forged predecessor":
				// Corrupt only this disposable fixture to verify that a same-Thread
				// label cannot replace a valid edge and its immutable receipt.
				if _, err := f.state.db.ExecContext(ctx, `DROP TRIGGER trg_thread_runs_update_immutable`); err != nil {
					t.Fatal(err)
				}
				if _, err := f.state.db.ExecContext(ctx, `UPDATE thread_runs SET predecessor_run_id=? WHERE run_id=?`,
					f.first.ID, f.third.ID); err != nil {
					t.Fatal(err)
				}
			case "nonterminal ancestor":
				if _, err := f.state.db.ExecContext(ctx, `UPDATE runs SET status='created',finished_at=NULL WHERE id=?`, f.second.ID); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err := f.state.GetThreadPredecessorWebSnapshot(ctx, runID, missionID, workspaceID,
				f.source.ID, snapshotID); err == nil {
				t.Fatal("accepted scope drift or forged historical access")
			}
		})
	}
}
