package repository

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cyberagent-workbench/internal/apperror"
)

func TestPRClientErrorMatrix(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name   string
		status int
		body   map[string]any
		want   apperror.Code
	}{
		{name: "created", status: 201, body: map[string]any{"html_url": "https://github.com/o/r/pull/7", "number": 7, "state": "open", "title": "feat"}},
		{name: "unauthorized", status: 401, body: map[string]any{}, want: apperror.CodeUnavailable},
		{name: "rate limit", status: 403, body: map[string]any{"message": "API rate limit exceeded"}, want: apperror.CodeResourceExhausted},
		{name: "permission", status: 403, body: map[string]any{"message": "Must have push access"}, want: apperror.CodePolicyDenied},
		{name: "unprocessable", status: 422, body: map[string]any{"message": "Validation Failed"}, want: apperror.CodeFailedPrecondition},
		{name: "not found", status: 404, body: map[string]any{}, want: apperror.CodeNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Header.Get("Authorization") != "Bearer token-value" {
					t.Fatalf("missing bearer token: %q", request.Header.Get("Authorization"))
				}
				writer.WriteHeader(tc.status)
				_ = json.NewEncoder(writer).Encode(tc.body)
			}))
			defer server.Close()
			client := NewPRClient()
			client.apiBase = server.URL
			receipt, err := client.CreatePR(ctx, "https://github.com/owner/repo.git", "feat", "main", "t", "b", "token-value")
			if tc.want == "" {
				if err != nil || receipt.Number != 7 || receipt.URL == "" {
					t.Fatalf("create PR failed: %#v err=%v", receipt, err)
				}
				return
			}
			if err == nil || apperror.CodeOf(err) != tc.want {
				t.Fatalf("error code = %s, want %s (err=%v)", apperror.CodeOf(err), tc.want, err)
			}
		})
	}
}

func TestPRClientRejectsUnknownHost(t *testing.T) {
	client := NewPRClient()
	_, err := client.CreatePR(context.Background(), "https://evil.example.com/o/r.git", "f", "m", "t", "b", "token")
	if err == nil || !strings.Contains(err.Error(), "github.com") {
		t.Fatalf("unknown host accepted: %v", err)
	}
}
