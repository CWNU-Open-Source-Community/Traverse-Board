package httpapi

import (
	"net/http"
	"reflect"
	"testing"
	"time"

	"cyberagent-workbench/internal/application"
	"cyberagent-workbench/internal/domain"
)

func TestThreadReviewHTTPUsesReadAuthorizationAndDoesNotMutate(t *testing.T) {
	fixture := newAPIFixture(t)
	threadID := domain.InitialThreadID(fixture.run.ID)
	before, err := fixture.store.ExportThread(t.Context(), threadID)
	if err != nil {
		t.Fatal(err)
	}
	api, err := New(fixture.store, Config{AccessToken: testAccessToken, ThreadReviewReader: application.NewThreadReviewService(fixture.store).WithCodeHandoff(application.NewCodeHandoffService(fixture.store))})
	if err != nil {
		t.Fatal(err)
	}
	path := ThreadCollectionPath + "/" + threadID + "/review"
	response := performSessionMessageRequest(t, api, http.MethodGet, path, testAccessToken, "", "", nil)
	var review application.ThreadReview
	decodeDataStatus(t, response, http.StatusOK, &review)
	if review.ThreadID != threadID || review.CurrentRunID != fixture.run.ID || review.ChangeScope != "recorded_file_edits" {
		t.Fatalf("wrong task projection: %+v", review)
	}
	for _, test := range []struct {
		method, suffix, token string
		status                int
	}{
		{http.MethodGet, "?unknown=true", testAccessToken, http.StatusBadRequest},
		{http.MethodPost, "", testAccessToken, http.StatusMethodNotAllowed},
		{http.MethodGet, "", "", http.StatusUnauthorized},
	} {
		response := performSessionMessageRequest(t, api, test.method, path+test.suffix, test.token, "", "", nil)
		if response.Code != test.status {
			t.Fatalf("%s %s expected %d got %d: %s", test.method, test.suffix, test.status, response.Code, response.Body.String())
		}
	}
	after, err := fixture.store.ExportThread(t.Context(), threadID)
	if err != nil {
		t.Fatal(err)
	}
	before.ExportedAt, after.ExportedAt = time.Time{}, time.Time{}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("GET review modified task data")
	}
}
