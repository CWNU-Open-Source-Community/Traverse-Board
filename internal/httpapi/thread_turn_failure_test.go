package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
)

func TestThreadTurnFailureHTTPOnlyMarksDurablyClosedTurn(t *testing.T) {
	fixture := newAPIFixture(t)
	controller := &threadTurnControllerStub{}
	api, err := New(fixture.store, Config{AccessToken: testAccessToken, ControlToken: testControlToken, RunCreationEnabled: true, SessionMessageEnabled: true, RunLifecycleEnabled: true, RunExecutionEnabled: true, RunLifecycleController: application.NewRunLifecycleControlService(fixture.store), RunExecutionController: runExecutionControllerFake{}, ThreadTurnController: controller})
	if err != nil {
		t.Fatal(err)
	}
	path := ThreadCollectionPath + "/" + domain.InitialThreadID(fixture.run.ID) + "/turns"
	for _, closed := range []bool{false, true} {
		controller.err = apperror.New(apperror.CodeFailedPrecondition, "turn boundary")
		if closed {
			controller.err = &application.ThreadTurnFailedError{Cause: controller.err}
		}
		response := performSessionMessageRequest(t, api, http.MethodPost, path, testControlToken, "thread-failure-http-key", "application/json", strings.NewReader(`{"version":"thread_message_submission.v1","content":"original requirement"}`))
		var envelope errorEnvelope
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if response.Code != http.StatusPreconditionFailed || (envelope.Error.TurnFailed != nil) != closed || (closed && !*envelope.Error.TurnFailed) || envelope.Error.MessageQueued != nil {
			t.Fatalf("closed=%v lost exact failure semantics: %d %s", closed, response.Code, response.Body.String())
		}
		if envelope.Error.TurnFailure != nil {
			t.Fatal("older marker-only error must not invent a failure reference")
		}
	}
	sealed := &domain.ThreadTurnFailure{ThreadID: domain.InitialThreadID(fixture.run.ID), RunID: fixture.run.ID,
		MessageID: "original-message", EventSequence: 42}
	controller.err = &application.ThreadTurnFailedError{Cause: apperror.New(apperror.CodeFailedPrecondition, "turn boundary"), Failure: sealed}
	response := performSessionMessageRequest(t, api, http.MethodPost, path, testControlToken, "thread-failure-sealed-key", "application/json", strings.NewReader(`{"version":"thread_message_submission.v1","content":"original requirement"}`))
	var envelope errorEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusPreconditionFailed || envelope.Error.TurnFailed == nil || !*envelope.Error.TurnFailed ||
		envelope.Error.TurnFailure == nil || *envelope.Error.TurnFailure != *threadTurnFailureReference(sealed) {
		t.Fatalf("sealed failure identity not carried through HTTP: %d %s", response.Code, response.Body.String())
	}
}
