package httpapi

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"cyberagent-workbench/internal/domain"
)

func TestThreadQueuedMessagesCompleteListAndRevisionObservation(t *testing.T) {
	f := newAPIFixture(t)
	thread, err := f.store.GetThreadByRun(t.Context(), f.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var messageID string
	for i := range 25 {
		queued, err := f.store.EnqueueOperatorSteering(t.Context(), domain.EnqueueOperatorSteeringRequest{RunID: f.run.ID,
			SessionID: f.run.SessionID, Content: fmt.Sprintf("queued input %d", i), OperationKey: fmt.Sprintf("http-list-queued-message-%04d", i), RequestedBy: "test_operator"})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			messageID = queued.Message.ID
		}
	}
	path := "/api/v1/threads/" + thread.ID + "/queued-messages"
	var queue ThreadQueuedMessagesView
	decodeData(t, f.get(t, path), &queue)
	if queue.Version != domain.ThreadQueuedMessagesProtocolVersion || queue.ThreadID != thread.ID || queue.RunID != f.run.ID ||
		queue.SessionID != f.run.SessionID || len(queue.Items) != 25 || queue.Pending != 25 || queue.Prepared != 0 || queue.CapabilityGrant {
		t.Fatalf("queue was truncated or escaped binding: %#v", queue)
	}
	if queue.Items[0].ID != messageID || queue.Items[0].Content != "queued input 0" || !queue.Items[0].CanEdit || !queue.Items[0].CanCancel {
		t.Fatalf("first pending item unavailable: %#v", queue.Items[0])
	}
	revisionPath := "/api/v1/sessions/" + f.run.SessionID + "/messages/" + messageID + "/revise"
	key := "http-revise-queued-message-0001"
	observationPath := "/api/v1/sessions/" + f.run.SessionID + "/messages/" + messageID + "/revisions/" + key
	var observation SessionSteeringRevisionObservationView
	decodeData(t, f.get(t, observationPath), &observation)
	if observation.State != "absent" || observation.Revision != nil || observation.Message == nil ||
		observation.Message.ID != messageID || observation.Message.RunID != f.run.ID || observation.Message.SessionID != f.run.SessionID ||
		observation.Message.Revision != 0 || observation.Message.Status != "pending" {
		t.Fatalf("invented receipt: %#v", observation)
	}
	body := `{"version":"session_steering_revision.v1","expected_revision":0,"content":"revised input"}`
	var revised SessionSteeringRevisionView
	for i := range 2 {
		response := performSessionMessageRequest(t, f.api, http.MethodPost, revisionPath, testControlToken, key, "application/json", strings.NewReader(body))
		if response.Code != http.StatusAccepted {
			t.Fatalf("revise %d: %d %s", i, response.Code, response.Body.String())
		}
		decodeDataStatus(t, response, http.StatusAccepted, &revised)
		if revised.RunID != f.run.ID || revised.SessionID != f.run.SessionID || revised.MessageID != messageID ||
			revised.Receipt.FromRevision != 0 || revised.Receipt.ToRevision != 1 || revised.Replayed != (i == 1) ||
			revised.ExecutionStarted || revised.ModelCalled || revised.ToolCalled || revised.CapabilityGrant {
			t.Fatalf("revision receipt differs: %#v", revised)
		}
		if strings.Contains(response.Body.String(), "original_content") || strings.Contains(response.Body.String(), "request_fingerprint") {
			t.Fatal("internal original request exposed")
		}
	}
	decodeData(t, f.get(t, observationPath), &observation)
	if observation.State != "sealed" || observation.Revision == nil || observation.Revision.Receipt.ID != revised.Receipt.ID {
		t.Fatalf("lost response observation changed receipt: %#v", observation)
	}
	decodeData(t, f.get(t, path), &queue)
	if queue.Items[0].ID != messageID || queue.Items[0].Revision != 1 || queue.Items[0].Content != "revised input" || len(queue.Items) != 25 {
		t.Fatalf("revision reordered or duplicated input: %#v", queue)
	}
	conflict := performSessionMessageRequest(t, f.api, http.MethodPost, revisionPath, testControlToken, key, "application/json",
		strings.NewReader(strings.Replace(body, "revised input", "different input", 1)))
	if conflict.Code != http.StatusConflict {
		t.Fatalf("different intent replay: %d %s", conflict.Code, conflict.Body.String())
	}
}

func TestSessionSteeringRevisionUnchangedProof(t *testing.T) {
	f := newAPIFixture(t)
	queued, err := f.store.EnqueueOperatorSteering(t.Context(), domain.EnqueueOperatorSteeringRequest{RunID: f.run.ID,
		SessionID: f.run.SessionID, Content: "original body", OperationKey: "http-unchanged-seed-0001", RequestedBy: "test_operator"})
	if err != nil {
		t.Fatal(err)
	}
	key, content := "http-unchanged-revise-0001", "  original body  "
	body, _ := json.Marshal(SessionSteeringRevisionRequestView{Version: domain.SessionSteeringRevisionProtocolVersion, ExpectedRevision: new(int64), Content: &content})
	path := "/api/v1/sessions/" + f.run.SessionID + "/messages/" + queued.Message.ID + "/revise"
	for range 2 { // An explicit retry of the original request returns the same bound proof, never an applied receipt.
		response := performSessionMessageRequest(t, f.api, http.MethodPost, path, testControlToken, key, "application/json", strings.NewReader(string(body)))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("no-op status: %d %s", response.Code, response.Body.String())
		}
		var envelope struct {
			Error apiErrorView `json:"error"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		proof := envelope.Error.RevisionUnchanged
		if envelope.Error.Code != "INVALID_ARGUMENT" || proof == nil || proof.Version != domain.QueueRevisionUnchangedProtocolVersion ||
			proof.RunID != f.run.ID || proof.SessionID != f.run.SessionID || proof.MessageID != queued.Message.ID || proof.ExpectedRevision != 0 ||
			proof.OperationKeySHA256 != fmt.Sprintf("%x", sha256.Sum256([]byte(key))) || proof.RequestContentSHA256 != fmt.Sprintf("%x", sha256.Sum256([]byte(content))) ||
			proof.CurrentContentSHA256 != queued.Message.ContentSHA256 || proof.NormalizedContentSHA256 != queued.Message.ContentSHA256 ||
			proof.ExecutionStarted || proof.ModelCalled || proof.ToolCalled || proof.CapabilityGrant {
			t.Fatalf("proof differs: %#v", proof)
		}
		if strings.Contains(response.Body.String(), content) || strings.Contains(response.Body.String(), key) {
			t.Fatal("proof leaks request text or key")
		}
	}
	message, err := f.store.GetOperatorSteering(t.Context(), queued.Message.ID)
	if err != nil || message.Revision != 0 || message.Content != "original body" {
		t.Fatalf("no-op mutated: %#v %v", message, err)
	}
	var observation SessionSteeringRevisionObservationView
	decodeData(t, f.get(t, strings.TrimSuffix(path, "revise")+"revisions/"+key), &observation)
	if observation.State != "absent" || observation.Revision != nil || observation.Message == nil || observation.Message.Revision != 0 {
		t.Fatalf("no-op invented receipt: %#v", observation)
	}
}

func TestSessionSteeringCancellationObservationExactOperation(t *testing.T) {
	f := newAPIFixture(t)
	queued, err := f.store.EnqueueOperatorSteering(t.Context(), domain.EnqueueOperatorSteeringRequest{RunID: f.run.ID,
		SessionID: f.run.SessionID, Content: "private cancellation target", OperationKey: "http-cancel-observation-seed", RequestedBy: "test_operator"})
	if err != nil {
		t.Fatal(err)
	}
	prefix := "/api/v1/sessions/" + f.run.SessionID + "/messages/" + queued.Message.ID + "/"
	key := "http-cancel-observation-key"
	read := func(operationKey string) SessionSteeringCancellationObservationView {
		t.Helper()
		response := f.get(t, prefix+"cancellations/"+operationKey)
		var view SessionSteeringCancellationObservationView
		decodeData(t, response, &view)
		if view.SessionID != f.run.SessionID || view.MessageID != queued.Message.ID || view.CapabilityGrant {
			t.Fatalf("wrong scope: %#v", view)
		}
		for _, private := range []string{"private cancellation target", "request_fingerprint", "operation_key_digest", "reason_sha256"} {
			if strings.Contains(response.Body.String(), private) {
				t.Fatalf("observation leaked %q", private)
			}
		}
		return view
	}
	before := read(key)
	if before.State != "absent" || before.Receipt != nil || before.Message == nil || before.Message.Status != "pending" {
		t.Fatalf("before: %#v", before)
	}
	response := performSessionMessageRequest(t, f.api, http.MethodPost, prefix+"cancel", testControlToken, key, "application/json",
		strings.NewReader(`{"version":"session_steering_cancellation.v1","reason":"test exact observation"}`))
	if response.Code != http.StatusAccepted {
		t.Fatalf("cancel: %d %s", response.Code, response.Body.String())
	}
	sealed := read(key)
	if sealed.State != "sealed" || sealed.Receipt == nil || sealed.Message != nil || sealed.Receipt.RunID != f.run.ID || sealed.Receipt.Kind != "operator" {
		t.Fatalf("sealed: %#v", sealed)
	}
	other := read("http-cancel-observation-other")
	if other.State != "absent" || other.Receipt != nil || other.Message == nil || other.Message.Status != "cancelled" {
		t.Fatalf("another operation became success: %#v", other)
	}
}

func TestSessionSteeringRevisionRejectsUnauthenticatedMalformedAndForeignInputs(t *testing.T) {
	f := newAPIFixture(t)
	queued, err := f.store.EnqueueOperatorSteering(t.Context(), domain.EnqueueOperatorSteeringRequest{RunID: f.run.ID,
		SessionID: f.run.SessionID, Content: "unchanged", OperationKey: "http-invalid-revision-seed", RequestedBy: "test_operator"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/sessions/" + f.run.SessionID + "/messages/" + queued.Message.ID + "/revise"
	valid := `{"version":"session_steering_revision.v1","expected_revision":0,"content":"changed"}`
	for _, body := range []string{
		`{"version":"session_steering_revision.v1","content":"changed"}`,
		`{"version":"session_steering_revision.v1","expected_revision":null,"content":"changed"}`,
		`{"version":"session_steering_revision.v1","expected_revision":0,"content":null}`,
		`{"version":"session_steering_revision.v1","expected_revision":0,"expected_revision":1,"content":"changed"}`,
		`{"version":"session_steering_revision.v1","expected_revision":0,"content":"changed","requested_by":"arbitrary"}`,
	} {
		response := performSessionMessageRequest(t, f.api, http.MethodPost, path, testControlToken, "http-invalid-revision-operation", "application/json", strings.NewReader(body))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("malformed body accepted: %d %s", response.Code, response.Body.String())
		}
	}
	response := performSessionMessageRequest(t, f.api, http.MethodPost, path, testAccessToken, "http-invalid-revision-operation", "application/json", strings.NewReader(valid))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("read token mutated queue: %d", response.Code)
	}
	response = performSessionMessageRequest(t, f.api, http.MethodPost, strings.Replace(path, f.run.SessionID, "foreign-session", 1), testControlToken,
		"http-invalid-revision-operation", "application/json", strings.NewReader(valid))
	if response.Code < 400 {
		t.Fatalf("foreign session accepted: %d", response.Code)
	}
	f.api.sessionSteeringControlEnabled = false
	response = performSessionMessageRequest(t, f.api, http.MethodPost, path, testControlToken, "http-invalid-revision-operation", "application/json", strings.NewReader(valid))
	if response.Code != http.StatusNotFound {
		t.Fatalf("disabled feature accepted: %d", response.Code)
	}
	message, err := f.store.GetOperatorSteering(t.Context(), queued.Message.ID)
	if err != nil || message.Content != "unchanged" || message.Revision != 0 {
		t.Fatalf("rejected request changed message: %#v %v", message, err)
	}
	// The ordinary Run metadata projection must remain free of queue bodies.
	var detail map[string]any
	decodeData(t, f.get(t, "/api/v1/runs/"+f.run.ID), &detail)
	encoded, _ := json.Marshal(detail["steering"])
	if strings.Contains(string(encoded), "unchanged") {
		t.Fatal("private queue body leaked into Run metadata")
	}
}
