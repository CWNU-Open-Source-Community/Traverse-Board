package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/session"
)

func TestThreadHTTPContractContinuesTerminalRunAndPreservesLifecycleHistory(t *testing.T) {
	fixture := newAPIFixture(t)
	api, err := New(fixture.store, Config{AccessToken: testAccessToken,
		ControlToken: testControlToken, RunCreationEnabled: true,
		SessionMessageEnabled: true, AppVersion: "thread-http-contract-test"})
	if err != nil {
		t.Fatal(err)
	}
	creationBody, _ := json.Marshal(ThreadCreationControlRequestView{
		Version: domain.ThreadCreationProtocolVersion, Goal: "HTTP stable Thread task",
		WorkspaceID: fixture.workspace.ID, Profile: "review", Surface: "code", Phase: "deliver",
	})
	response := performSessionMessageRequest(t, api, http.MethodPost, ThreadCollectionPath,
		testControlToken, "thread-http-create-operation-0001", "application/json",
		bytes.NewReader(creationBody))
	var created ThreadCreationControlView
	decodeDataStatus(t, response, http.StatusAccepted, &created)
	if created.Thread.ID == "" || created.Thread.ActiveRunID != created.Run.ID ||
		created.Thread.LastRunID != created.Run.ID || created.Thread.Status != "active" {
		t.Fatalf("invalid Thread creation projection: %#v", created)
	}

	runs := application.NewRunService(fixture.store)
	started, err := runs.Start(t.Context(), created.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	terminal, err := runs.Fail(t.Context(), started.ID, "HTTP fixture terminal")
	if err != nil || terminal.Status != domain.RunFailed {
		t.Fatalf("terminal Run=%#v err=%v", terminal, err)
	}
	messageBody, _ := json.Marshal(ThreadMessageControlRequestView{
		Version: domain.ThreadMessageProtocolVersion, Content: "continue after failure",
	})
	response = performSessionMessageRequest(t, api, http.MethodPost,
		ThreadCollectionPath+"/"+created.Thread.ID+"/messages", testControlToken,
		"thread-http-message-operation-0001", "application/json", bytes.NewReader(messageBody))
	var continued ThreadMessageControlView
	decodeDataStatus(t, response, http.StatusAccepted, &continued)
	if continued.Thread.ID != created.Thread.ID || !continued.SuccessorCreated ||
		continued.PredecessorRunID != terminal.ID || continued.RunID == terminal.ID ||
		continued.Thread.ActiveRunID != continued.RunID || continued.CapabilityGrant ||
		continued.ExecutionStarted || continued.ModelCalled || continued.ToolCalled {
		t.Fatalf("invalid Thread continuation projection: %#v", continued)
	}

	response = performSessionMessageRequest(t, api, http.MethodGet,
		ThreadCollectionPath+"/"+created.Thread.ID, testAccessToken, "", "", nil)
	var detail ThreadDetailView
	decodeDataStatus(t, response, http.StatusOK, &detail)
	if detail.Thread.ID != created.Thread.ID || detail.ActiveRun == nil ||
		detail.ActiveRun.ID != continued.RunID || len(detail.Runs) != 2 ||
		detail.Runs[1].PredecessorRunID != terminal.ID {
		t.Fatalf("invalid Thread detail after continuation: %#v", detail)
	}
	response = performSessionMessageRequest(t, api, http.MethodGet,
		ThreadCollectionPath+"/"+created.Thread.ID+"/messages?limit=100&include_compacted=true",
		testAccessToken, "", "", nil)
	var messages []ThreadMessageView
	decodeDataStatus(t, response, http.StatusOK, &messages)
	if len(messages) != 1 || messages[0].ThreadID != created.Thread.ID ||
		messages[0].RunID != continued.RunID || messages[0].Content != "continue after failure" ||
		messages[0].ProvenanceVersion != session.ContextProvenanceVersion ||
		messages[0].SourceKind != session.SourceOperatorMessage ||
		messages[0].ContentSHA256 == "" || !messages[0].InstructionAuthorized {
		t.Fatalf("invalid cross-Run Thread messages: %#v", messages)
	}

	archiveBody, _ := json.Marshal(ThreadLifecycleControlRequestView{
		Version:         domain.ThreadLifecycleProtocolVersion,
		ExpectedVersion: continued.Thread.Version,
	})
	archivePath := ThreadCollectionPath + "/" + created.Thread.ID + "/archive"
	response = performSessionMessageRequest(t, api, http.MethodPost, archivePath,
		testControlToken, "thread-http-archive-operation-0001", "application/json",
		bytes.NewReader(archiveBody))
	var archived ThreadLifecycleControlView
	decodeDataStatus(t, response, http.StatusOK, &archived)
	if archived.Thread.Status != "archived" || archived.CapabilityGrant {
		t.Fatalf("invalid archived Thread: %#v", archived)
	}
	response = performSessionMessageRequest(t, api, http.MethodPost, archivePath,
		testControlToken, "thread-http-archive-operation-0001", "application/json",
		bytes.NewReader(archiveBody))
	var replayed ThreadLifecycleControlView
	decodeDataStatus(t, response, http.StatusOK, &replayed)
	if replayed.Thread.Version != archived.Thread.Version ||
		replayed.Thread.UpdatedAt != archived.Thread.UpdatedAt {
		t.Fatalf("Thread lifecycle replay drifted: original=%#v replay=%#v", archived, replayed)
	}
	restoreBody, _ := json.Marshal(ThreadLifecycleControlRequestView{
		Version:         domain.ThreadLifecycleProtocolVersion,
		ExpectedVersion: archived.Thread.Version,
	})
	response = performSessionMessageRequest(t, api, http.MethodPost,
		ThreadCollectionPath+"/"+created.Thread.ID+"/restore", testControlToken,
		"thread-http-restore-operation-0001", "application/json", bytes.NewReader(restoreBody))
	var restored ThreadLifecycleControlView
	decodeDataStatus(t, response, http.StatusOK, &restored)
	if restored.Thread.Status != "active" {
		t.Fatalf("invalid restored Thread: %#v", restored)
	}

	response = performSessionMessageRequest(t, api, http.MethodGet,
		ThreadCollectionPath+"/"+created.Thread.ID+"/export", testAccessToken, "", "", nil)
	var exported ThreadExportView
	decodeDataStatus(t, response, http.StatusOK, &exported)
	if exported.Thread.ID != created.Thread.ID || len(exported.Runs) != 2 ||
		len(exported.Sessions) != 2 ||
		len(exported.Messages) != 1 || len(exported.Events) < 5 ||
		len(exported.AuditEvents) == 0 {
		t.Fatalf("Thread export lost bound history: %#v", exported)
	}
}

func TestThreadHTTPControlFailsClosedWithoutControlBearer(t *testing.T) {
	fixture := newAPIFixture(t)
	body := bytes.NewBufferString(`{"version":"thread_message_submission.v1","content":"no"}`)
	response := performSessionMessageRequest(t, fixture.api, http.MethodPost,
		ThreadCollectionPath+"/"+domain.InitialThreadID(fixture.run.ID)+"/messages",
		testAccessToken, "thread-http-denied-operation-0001", "application/json", body)
	assertAPIError(t, response, http.StatusUnauthorized, "POLICY_DENIED")
}

func TestThreadHTTPCreationFailsClosedWithoutCompleteThreadCapability(t *testing.T) {
	fixture := newAPIFixture(t)
	api, err := New(fixture.store, Config{AccessToken: testAccessToken,
		ControlToken: testControlToken, RunCreationEnabled: true,
		SessionMessageEnabled: false, AppVersion: "thread-http-gate-test"})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(ThreadCreationControlRequestView{
		Version: domain.ThreadCreationProtocolVersion, Goal: "must stay unavailable",
		WorkspaceID: fixture.workspace.ID,
	})
	response := performSessionMessageRequest(t, api, http.MethodPost, ThreadCollectionPath,
		testControlToken, "thread-http-disabled-operation-0001", "application/json",
		bytes.NewReader(body))
	assertAPIError(t, response, http.StatusNotFound, "NOT_FOUND")
}
