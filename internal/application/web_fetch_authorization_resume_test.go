package application

import (
	"context"
	"testing"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/domain"
)

type webFetchLeaseReleaseRetryStore struct {
	calls int
}

func (*webFetchLeaseReleaseRetryStore) GetWebFetchAuthorization(context.Context,
	string,
) (domain.WebFetchAuthorization, error) {
	return domain.WebFetchAuthorization{}, nil
}

func (*webFetchLeaseReleaseRetryStore) ListRecoverableWebFetchAuthorizations(context.Context,
	string, int,
) ([]domain.WebFetchAuthorization, error) {
	return nil, nil
}

func (s *webFetchLeaseReleaseRetryStore) ResumeWebFetchAuthorizationRun(context.Context,
	string,
) (domain.Run, bool, error) {
	s.calls++
	if s.calls == 1 {
		return domain.Run{}, false, apperror.New(apperror.CodeConflict,
			"Run lifecycle control requires no active execution lease")
	}
	return domain.Run{ID: "run-web-fetch-retry", Status: domain.RunRunning}, false, nil
}

func TestResumeWebFetchAuthorizationRunRetriesLeaseReleaseConflict(t *testing.T) {
	store := &webFetchLeaseReleaseRetryStore{}
	if err := resumeWebFetchAuthorizationRun(t.Context(), store,
		"web-fetch-authorization-retry"); err != nil {
		t.Fatal(err)
	}
	if store.calls != 2 {
		t.Fatalf("resume calls=%d want=2", store.calls)
	}
}
