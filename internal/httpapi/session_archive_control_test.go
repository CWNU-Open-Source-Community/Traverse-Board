package httpapi

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"cyberagent-workbench/internal/session"
)

func TestSessionArchiveControlRetainsDurableConversationAndReplays(t *testing.T) {
	fixture := newAPIFixture(t)
	requestPath := "/api/v1/sessions/" + fixture.run.SessionID + "/archive"
	body := `{"version":"session_archive.v1","confirm":true}`

	first := performSessionMessageRequest(t, fixture.api, http.MethodPost, requestPath,
		testControlToken, "", "application/json", strings.NewReader(body))
	if first.Code != http.StatusAccepted ||
		!strings.Contains(first.Body.String(), `"status":"archived"`) ||
		!strings.Contains(first.Body.String(), `"replayed":false`) {
		t.Fatalf("archive status=%d body=%s", first.Code, first.Body.String())
	}
	record, err := fixture.store.GetSession(context.Background(), fixture.run.SessionID)
	if err != nil || record.Status != session.StatusArchived {
		t.Fatalf("Session was not archived: %#v err=%v", record, err)
	}
	messages, err := fixture.store.ListSessionMessages(context.Background(), fixture.run.SessionID, true)
	if err != nil || len(messages) != 3 {
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
