package httpapi

import (
	"context"
	"net/http"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/application"
)

const ThreadReviewPathTemplate = "/api/v1/threads/{thread_id}/review"

type ThreadReviewReader interface {
	Review(context.Context, string) (application.ThreadReview, error)
}

func (a *API) threadReviewView(request *http.Request, threadID string) (any, *Page, error) {
	if err := rejectQuery(request.URL.Query()); err != nil {
		return nil, nil, err
	}
	if a.threadReview == nil {
		return nil, nil, apperror.New(apperror.CodeUnavailable, "Thread review is unavailable")
	}
	result, err := a.threadReview.Review(request.Context(), threadID)
	if err != nil {
		return nil, nil, err
	}
	if result.ThreadID != threadID {
		return nil, nil, apperror.New(apperror.CodeConflict, "Thread review returned a mismatched identity")
	}
	return result, nil, nil
}
