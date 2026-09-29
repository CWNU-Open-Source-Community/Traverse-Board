package httpapi

import (
	"context"
	"cyberagent-workbench/internal/application"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/store"
)

// The live-owner state machine is exercised with a real service/provider in
// application tests. This fixture exercises HTTP authorization and SQLite receipts.
type promotionHTTPController struct {
	threadTurnControllerStub
	store *store.SQLiteStore
	calls int
}

func TestPromotionHTTPSealedRejectionSurvivesRestartAndAllowsFreshIntent(t *testing.T) {
	f := newAPIFixture(t)
	q, err := f.store.EnqueueOperatorSteering(t.Context(), domain.EnqueueOperatorSteeringRequest{RunID: f.run.ID, SessionID: f.run.SessionID, Content: "retained pending", OperationKey: "http-promotion-reject-source-0001", RequestedBy: "http_session_operator"})
	if err != nil {
		t.Fatal(err)
	}
	f.api.threadTurnController = application.NewThreadTurnService(f.store, nil, nil)
	path := "/api/v1/sessions/" + f.run.SessionID + "/messages/" + q.Message.ID + "/promote"
	key := "http-promotion-rejected-key-0001"
	data, _ := json.Marshal(SessionSteeringPromotionRequestView{Version: domain.SessionSteeringPromotionProtocolVersion, ExpectedRevision: new(int64), ExpectedContentSHA256: q.Message.ContentSHA256, ExpectedAttemptID: f.checkpoint.AttemptID, ExpectedExecutionID: "execution-before-restart"})
	r := performSessionMessageRequest(t, f.api, http.MethodPost, path, testControlToken, key, "application/json", strings.NewReader(string(data)))
	var refused SessionSteeringPromotionRejectionView
	decodeDataStatus(t, r, 202, &refused)
	if !refused.Rejected || refused.Replayed || refused.Receipt.ExecutionID != "execution-before-restart" || refused.Receipt.ContentSHA256 != q.Message.ContentSHA256 || refused.ExecutionStarted || refused.ModelCalled || refused.ToolCalled || refused.CapabilityGrant {
		t.Fatalf("rejection=%#v", refused)
	}
	f.api.threadTurnController = nil
	var observed SessionSteeringPromotionObservationView
	decodeData(t, f.get(t, strings.TrimSuffix(path, "promote")+"promotions/"+key), &observed)
	if observed.State != "rejected" || observed.Rejection == nil || observed.Rejection.Receipt.ID != refused.Receipt.ID || observed.Message != nil || observed.Promotion != nil {
		t.Fatalf("rejection lookup=%#v", observed)
	}
	old, err := f.store.GetOperatorSteering(t.Context(), q.Message.ID)
	if err != nil || old.Status != domain.OperatorSteeringPending {
		t.Fatalf("rejection lost pending=%#v %v", old, err)
	}
	f.api.threadTurnController = &promotionHTTPController{store: f.store}
	r = performSessionMessageRequest(t, f.api, http.MethodPost, path, testControlToken, key, "application/json", strings.NewReader(string(data)))
	var retry SessionSteeringPromotionRejectionView
	decodeDataStatus(t, r, 202, &retry)
	if !retry.Rejected || !retry.Replayed || retry.Receipt.ID != refused.Receipt.ID {
		t.Fatalf("late original key=%#v", retry)
	}
	r = performSessionMessageRequest(t, f.api, http.MethodPost, path, testControlToken, "http-promotion-new-intent-0001", "application/json", strings.NewReader(string(data)))
	var admitted SessionSteeringPromotionView
	decodeDataStatus(t, r, 202, &admitted)
	if admitted.Receipt.ReplacementMessageID == "" {
		t.Fatal("fresh intent did not promote")
	}
}

func (c *promotionHTTPController) PromoteCurrentSteering(ctx context.Context, r domain.PromoteOperatorSteeringRequest) (domain.PromoteOperatorSteeringResult, error) {
	c.calls++
	return c.store.PromoteOperatorSteering(ctx, r)
}
func (c *promotionHTTPController) InspectCurrentSteeringPromotion(ctx context.Context, s, m, k, a string) (domain.OperatorSteeringPromotionInspection, error) {
	result, err := c.store.InspectOperatorSteeringPromotion(ctx, s, m, k, a)
	if result.State == domain.OperatorSteeringRevisionAbsent {
		result.ExecutionObserved = true
		result.ExecutionID = "http-execution-fixture"
	}
	return result, err
}
func TestPromotionHTTPControlStrictShapeReceiptAndReadonlyRecovery(t *testing.T) {
	f := newAPIFixture(t)
	q, err := f.store.EnqueueOperatorSteering(t.Context(), domain.EnqueueOperatorSteeringRequest{RunID: f.run.ID, SessionID: f.run.SessionID, Content: "guide from queue", OperationKey: "http-promotion-queued-0001", RequestedBy: "http_session_operator"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/sessions/" + f.run.SessionID + "/messages/" + q.Message.ID + "/promote"
	key := "http-promotion-operation-0001"
	observationPath := strings.TrimSuffix(path, "promote") + "promotions/" + key
	bodyBytes, _ := json.Marshal(SessionSteeringPromotionRequestView{Version: domain.SessionSteeringPromotionProtocolVersion, ExpectedRevision: new(int64), ExpectedContentSHA256: q.Message.ContentSHA256, ExpectedAttemptID: f.checkpoint.AttemptID, ExpectedExecutionID: "http-execution-fixture"})
	body := string(bodyBytes)
	// Store alone must never bypass the current task's live owner admission.
	absentController := performSessionMessageRequest(t, f.api, http.MethodPost, path, testControlToken, key, "application/json", strings.NewReader(body))
	if absentController.Code != http.StatusPreconditionFailed {
		t.Fatalf("missing owner=%d %s", absentController.Code, absentController.Body.String())
	}
	var observation SessionSteeringPromotionObservationView
	decodeData(t, f.get(t, observationPath), &observation)
	if observation.State != "absent" || observation.ExecutionObserved || observation.ExecutionID != "" {
		t.Fatalf("invented owner=%#v", observation)
	}
	controller := &promotionHTTPController{store: f.store}
	f.api.threadTurnController = controller
	for _, test := range []struct {
		name, token, contentType, body string
		want                           int
	}{
		{"read-token", testAccessToken, "application/json", body, 401},
		{"no-token", "", "application/json", body, 401},
		{"duplicate", testControlToken, "application/json", strings.TrimSuffix(body, "}") + `,"expected_revision":0}`, 400},
		{"attachments", testControlToken, "application/json", strings.TrimSuffix(body, "}") + `,"images":[]}`, 400},
		{"missing-execution", testControlToken, "application/json", strings.Replace(body, `"expected_execution_id":"http-execution-fixture"`, `"expected_execution_id":""`, 1), 400},
		{"media", testControlToken, "text/plain", body, 415},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := performSessionMessageRequest(t, f.api, http.MethodPost, path, test.token, key, test.contentType, strings.NewReader(test.body))
			if r.Code != test.want {
				t.Fatalf("status=%d %s", r.Code, r.Body.String())
			}
		})
	}
	if controller.calls != 0 {
		t.Fatal("invalid request reached controller")
	}
	observation = SessionSteeringPromotionObservationView{}
	decodeData(t, f.get(t, observationPath), &observation)
	if !observation.ExecutionObserved || observation.ExecutionID != "http-execution-fixture" {
		t.Fatalf("owner=%#v", observation)
	}
	var receiptID string
	for i := 0; i < 2; i++ {
		r := performSessionMessageRequest(t, f.api, http.MethodPost, path, testControlToken, key, "application/json", strings.NewReader(body))
		var result SessionSteeringPromotionView
		decodeDataStatus(t, r, http.StatusAccepted, &result)
		if result.Replayed != (i == 1) || result.ExecutionStarted || result.ModelCalled || result.ToolCalled || result.CapabilityGrant || result.Receipt.ExecutionID != "http-execution-fixture" || result.Receipt.ReplacementMessageID == q.Message.ID {
			t.Fatalf("receipt=%#v", result)
		}
		if i == 0 {
			receiptID = result.Receipt.ID
		} else if receiptID != result.Receipt.ID {
			t.Fatal("replay receipt changed")
		}
	}
	f.api.threadTurnController = nil
	observation = SessionSteeringPromotionObservationView{}
	decodeData(t, f.get(t, observationPath), &observation)
	if observation.State != "sealed" || observation.Promotion == nil || observation.Promotion.Receipt.ID != receiptID || observation.Message != nil {
		t.Fatalf("restart receipt=%#v", observation)
	}
}
