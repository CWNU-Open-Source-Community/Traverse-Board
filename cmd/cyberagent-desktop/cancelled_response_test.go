//go:build desktop

package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
)

func TestDesktopCancellationResponseUsesStandardStatusAndPreservesReceipt(t *testing.T) {
	const receipt = `{"version":"api.v1","request_id":"req-cancelled","error":{"code":"CANCELLED","message":"turn cancelled","turn_failed":true,"turn_failure":{"thread_id":"thread-one","run_id":"run-one","message_id":"steer-one","event_sequence":42}}}`
	handler := inProcessAPIHandler{next: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("X-Request-Id", "req-cancelled")
		w.WriteHeader(apperror.HTTPStatus(context.Canceled))
		_, _ = io.WriteString(w, receipt)
	})}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, newWailsRendererRequest(http.MethodPost,
		"http://wails.localhost/api/v1/threads/thread-one/turns", nil))
	if response.Code != http.StatusConflict || http.StatusText(response.Code) == "" {
		t.Fatalf("desktop cancellation status=%d; want a standard error status", response.Code)
	}
	if response.Body.String() != receipt || response.Header().Get("X-Request-Id") != "req-cancelled" ||
		response.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("cancellation receipt or headers changed: %s", response.Body.String())
	}
	if apperror.HTTPStatus(context.Canceled) != 499 {
		t.Fatal("native transport adaptation must preserve the standalone API status contract")
	}
}

type deadlineResponseWriter struct {
	*httptest.ResponseRecorder
	deadline time.Time
}

func (w *deadlineResponseWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	return nil
}

func TestDesktopResponsePreservesControllerDeadlineAndOrdinaryStatus(t *testing.T) {
	deadline := time.Unix(100, 0)
	for _, status := range []int{http.StatusAccepted, http.StatusForbidden, http.StatusPreconditionFailed} {
		writer := &deadlineResponseWriter{ResponseRecorder: httptest.NewRecorder()}
		handler := inProcessAPIHandler{next: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := http.NewResponseController(w).SetWriteDeadline(deadline); err != nil {
				t.Fatalf("response controller deadline no longer reaches transport: %v", err)
			}
			w.WriteHeader(status)
			_, _ = io.WriteString(w, "unchanged")
		})}
		handler.ServeHTTP(writer, newWailsRendererRequest(http.MethodGet, "http://wails.localhost/api/v1/health", nil))
		if writer.Code != status || writer.Body.String() != "unchanged" || !writer.deadline.Equal(deadline) {
			t.Fatalf("status=%d deadline=%v body=%q", writer.Code, writer.deadline, writer.Body.String())
		}
	}
}
