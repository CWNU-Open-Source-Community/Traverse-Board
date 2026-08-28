package httpapi

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"cyberagent-workbench/internal/session"
)

func TestSessionArchiveControlRejectsThreadBoundConversation(t *testing.T) {
	fixture := newAPIFixture(t)
	requestPath := "/api/v1/sessions/" + fixture.run.SessionID + "/archive"
	body := `{"version":"session_archive.v1","confirm":true}`

	response := performSessionMessageRequest(t, fixture.api, http.MethodPost, requestPath,
		testControlToken, "", "application/json", strings.NewReader(body))
	assertAPIError(t, response, http.StatusPreconditionFailed, "FAILED_PRECONDITION")
	threadRecord, err := fixture.store.GetThreadBySession(context.Background(),
		fixture.run.SessionID)
	if err != nil || !strings.Contains(response.Body.String(), threadRecord.ID) {
		t.Fatalf("bound Session rejection omitted canonical Thread: thread=%#v err=%v body=%s",
			threadRecord, err, response.Body.String())
	}
	record, err := fixture.store.GetSession(context.Background(), fixture.run.SessionID)
	if err != nil || record.Status != session.StatusActive {
		t.Fatalf("bound Session changed despite rejection: %#v err=%v", record, err)
	}
	messages, err := fixture.store.ListSessionMessages(context.Background(), fixture.run.SessionID, true)
	if err != nil || len(messages) != 3 {
		t.Fatalf("rejection changed durable history: messages=%d err=%v", len(messages), err)
	}
}

func TestSessionArchiveControlRetainsLegacyUnboundConversationAndReplays(t *testing.T) {
	fixture := newAPIFixture(t)
	ctx := context.Background()
	record := session.New(fixture.workspace.ID, "legacy unbound conversation", "review")
	if err := fixture.store.SaveSession(ctx, record); err != nil {
		t.Fatal(err)
	}
	for _, message := range []session.Message{
		session.NewMessage(record.ID, "user", "legacy request"),
		session.NewMessage(record.ID, "assistant", "legacy response"),
	} {
		if _, err := fixture.store.SaveSessionMessage(ctx, message); err != nil {
			t.Fatal(err)
		}
	}
	requestPath := "/api/v1/sessions/" + record.ID + "/archive"
	body := `{"version":"session_archive.v1","confirm":true}`

	first := performSessionMessageRequest(t, fixture.api, http.MethodPost, requestPath,
		testControlToken, "", "application/json", strings.NewReader(body))
	if first.Code != http.StatusAccepted ||
		!strings.Contains(first.Body.String(), `"status":"archived"`) ||
		!strings.Contains(first.Body.String(), `"replayed":false`) {
		t.Fatalf("archive status=%d body=%s", first.Code, first.Body.String())
	}
	stored, err := fixture.store.GetSession(ctx, record.ID)
	if err != nil || stored.Status != session.StatusArchived {
		t.Fatalf("legacy Session was not archived: %#v err=%v", stored, err)
	}
	messages, err := fixture.store.ListSessionMessages(ctx, record.ID, true)
	if err != nil || len(messages) != 2 {
		t.Fatalf("archive removed durable history: messages=%d err=%v", len(messages), err)
	}

	replay := performSessionMessageRequest(t, fixture.api, http.MethodPost, requestPath,
		testControlToken, "", "application/json", strings.NewReader(body))
	if replay.Code != http.StatusAccepted || !strings.Contains(replay.Body.String(), `"replayed":true`) {
		t.Fatalf("archive replay status=%d body=%s", replay.Code, replay.Body.String())
	}
}

func TestSessionArchiveControlFailsClosed(t *testing.T) {
	fixture := newAPIFixture(t)
	requestPath := "/api/v1/sessions/" + fixture.run.SessionID + "/archive"
	invalid := performSessionMessageRequest(t, fixture.api, http.MethodPost, requestPath,
		testControlToken, "", "application/json",
		strings.NewReader(`{"version":"session_archive.v1","confirm":false}`))
	assertAPIError(t, invalid, http.StatusBadRequest, "INVALID_ARGUMENT")
	unauthorized := performSessionMessageRequest(t, fixture.api, http.MethodPost, requestPath,
		testAccessToken, "", "application/json",
		strings.NewReader(`{"version":"session_archive.v1","confirm":true}`))
	assertAPIError(t, unauthorized, http.StatusUnauthorized, "POLICY_DENIED")
}
