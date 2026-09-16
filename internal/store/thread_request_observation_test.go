package store

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/policy"
)

// query_only makes an accidental Reserve/Enqueue/update fail, rather than merely
// relying on equal row counts. It applies to the store's sole SQLite connection.
func requestObservationQueryOnly(t *testing.T, st *SQLiteStore, enabled bool) {
	t.Helper()
	statement := "PRAGMA query_only=OFF"
	if enabled {
		statement = "PRAGMA query_only=ON"
	}
	if _, err := st.db.Exec(statement); err != nil {
		t.Fatal(err)
	}
}

func TestThreadRequestObservationCreationSurvivesRestartAndChangedRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "observation.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	workspace := WorkspaceRecord{ID: "workspace-observe", Name: "observation", RootPath: t.TempDir(), CreatedAt: time.Now().UTC()}
	if err = st.SaveWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	key := "creation-observation-original"
	requestObservationQueryOnly(t, st, true)
	missing, err := st.InspectThreadCreationRequest(t.Context(), workspace.ID, key, "http_thread_operator")
	if err != nil || missing.State != "not_received" || missing.Settled {
		t.Fatalf("missing=%#v %v", missing, err)
	}
	requestObservationQueryOnly(t, st, false)
	created, err := application.NewControlledRunCreationService(st).Create(t.Context(), application.ControlledRunCreationRequest{
		Version: domain.RunCreationProtocolVersion, Goal: "observe original creation", WorkspaceID: workspace.ID, OperationKey: key, RequestedBy: "http_thread_operator"})
	if err != nil {
		t.Fatal(err)
	}
	runs := application.NewRunService(st)
	if _, err = runs.Start(t.Context(), created.Run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = runs.Fail(t.Context(), created.Run.ID, "fixture later failure"); err != nil {
		t.Fatal(err)
	}
	if err = st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	requestObservationQueryOnly(t, st, true)
	got, err := st.InspectThreadCreationRequest(t.Context(), workspace.ID, key, "http_thread_operator")
	if err != nil || got.State != "completed" || !got.Settled || got.RunID != created.Run.ID || got.ThreadID != domain.InitialThreadID(created.Run.ID) {
		t.Fatalf("observation=%#v %v", got, err)
	}
	run, err := st.GetRun(t.Context(), got.RunID)
	if err != nil || run.Status != domain.RunFailed {
		t.Fatalf("lookup rewrote old Run: %#v %v", run, err)
	}
	for _, pair := range [][2]string{{"workspace-other", "http_thread_operator"}, {workspace.ID, "other_operator"}} {
		if _, err = st.InspectThreadCreationRequest(t.Context(), pair[0], key, pair[1]); apperror.CodeOf(err) != apperror.CodeConflict {
			t.Fatalf("scope mismatch accepted: %v", err)
		}
	}
}

func TestThreadRequestObservationReservedFilesRejectedAndUnknownAreReadOnly(t *testing.T) {
	st, other, _, request, _ := threadIntentFixture(t)
	requestObservationQueryOnly(t, other, true)
	for range 2 {
		got, err := other.InspectThreadTurnRequest(t.Context(), request.ThreadID, request.OperationKey, request.RequestedBy)
		if err != nil || got.State != "received" || got.Settled || got.MessageID != "" {
			t.Fatalf("reserved=%#v %v", got, err)
		}
		got, err = other.InspectThreadTurnRequest(t.Context(), request.ThreadID, "unknown-original-request", request.RequestedBy)
		if err != nil || got.State != "not_received" || got.Settled {
			t.Fatalf("missing=%#v %v", got, err)
		}
	}
	if rejected, err := st.RejectThreadMessageIntent(t.Context(), request); err != nil || !rejected {
		t.Fatalf("reject=%v %v", rejected, err)
	}
	got, err := other.InspectThreadTurnRequest(t.Context(), request.ThreadID, request.OperationKey, request.RequestedBy)
	if err != nil || got.State != "rejected" || !got.Settled {
		t.Fatalf("rejected=%#v %v", got, err)
	}
	var intents, messages, attachments int
	if err = other.db.QueryRow(`SELECT (SELECT COUNT(*) FROM thread_message_intents),(SELECT COUNT(*) FROM operator_steering_messages),(SELECT COUNT(*) FROM session_messages)`).Scan(&intents, &messages, &attachments); err != nil {
		t.Fatal(err)
	}
	if intents != 1 || messages != 0 || attachments != 0 {
		t.Fatalf("query published data: %d/%d/%d", intents, messages, attachments)
	}
}

func TestThreadRequestObservationQueuedAndCompletedPreserveOriginalBinding(t *testing.T) {
	st, other, run, request, prepared := threadIntentFixture(t)
	queued, err := st.CommitThreadMessage(t.Context(), request, run.ID, prepared)
	if err != nil {
		t.Fatal(err)
	}
	requestObservationQueryOnly(t, other, true)
	got, err := other.InspectThreadTurnRequest(t.Context(), request.ThreadID, request.OperationKey, request.RequestedBy)
	if err != nil || got.State != "received" || got.MessageID != queued.Message.ID || got.MessageStatus != domain.OperatorSteeringPending {
		t.Fatalf("queued=%#v %v", got, err)
	}
	lease := acquireTestRunExecutionLease(t, t.Context(), st, run.ID)
	turn, err := st.BeginSupervisorTurn(t.Context(), lease, "")
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, response := recordOperatorSteeringModelSuccess(t, t.Context(), st, turn.Checkpoint, "read recorded files")
	_, _, _, err = st.CompleteSupervisorTurn(t.Context(), checkpoint, response, domain.RootAction{Version: domain.RootLifecycleVersion, Kind: domain.RootActionFinish, Message: response.Text, Summary: "done"}, policy.Decision{Allowed: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	before, err := st.ExportThread(t.Context(), request.ThreadID)
	if err != nil {
		t.Fatal(err)
	}
	got, err = other.InspectThreadTurnRequest(t.Context(), request.ThreadID, request.OperationKey, request.RequestedBy)
	if err != nil || got.State != "completed" || !got.Settled || got.MessageID != queued.Message.ID {
		t.Fatalf("completed=%#v %v", got, err)
	}
	after, err := st.ExportThread(t.Context(), request.ThreadID)
	after.ExportedAt = before.ExportedAt
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("query changed history: %v", err)
	}
	if _, err = other.InspectThreadTurnRequest(t.Context(), request.ThreadID, request.OperationKey, "other_operator"); apperror.CodeOf(err) != apperror.CodeConflict {
		t.Fatalf("wrong requester=%v", err)
	}
}

func TestThreadRequestObservationLegacyCancellationDoesNotAdopt(t *testing.T) {
	st, other, run, request, _ := threadIntentFixture(t)
	key := "legacy-observation-request"
	requestObservationQueryOnly(t, other, true)
	missing, err := other.InspectThreadTurnRequest(t.Context(), request.ThreadID, key, request.RequestedBy)
	if err != nil || missing.State != "not_received" {
		t.Fatalf("before delayed arrival=%#v %v", missing, err)
	}
	queued, err := st.EnqueueOperatorSteering(t.Context(), domain.EnqueueOperatorSteeringRequest{RunID: run.ID, SessionID: run.SessionID, Content: "legacy message", OperationKey: key, RequestedBy: request.RequestedBy})
	if err != nil {
		t.Fatal(err)
	}
	got, err := other.InspectThreadTurnRequest(t.Context(), request.ThreadID, key, request.RequestedBy)
	if err != nil || got.State != "received" || got.MessageID != queued.Message.ID {
		t.Fatalf("legacy=%#v %v", got, err)
	}
	_, err = st.CancelOperatorSteering(t.Context(), domain.CancelOperatorSteeringRequest{MessageID: queued.Message.ID, OperationKey: "legacy-observation-cancel", RequestedBy: request.RequestedBy, Reason: "operator cancelled"})
	if err != nil {
		t.Fatal(err)
	}
	got, err = other.InspectThreadTurnRequest(t.Context(), request.ThreadID, key, request.RequestedBy)
	if err != nil || got.State != "cancelled" || !got.Settled {
		t.Fatalf("cancelled=%#v %v", got, err)
	}
	var count int
	if err = other.db.QueryRow(`SELECT COUNT(*) FROM thread_message_intents`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("legacy query adopted new intent: %d %v", count, err)
	}
}
