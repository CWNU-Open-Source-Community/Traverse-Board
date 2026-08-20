package githubreview

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cyberagent-workbench/internal/credential"
)

func TestDeviceFlowStoresOnlyCredentialBundleAndRefreshes(t *testing.T) {
	var polls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case deviceCodePath:
			_ = json.NewEncoder(writer).Encode(map[string]any{"device_code": "device-code-secret",
				"user_code": "ABCD-EFGH", "verification_uri": "https://github.com/login/device",
				"expires_in": 900, "interval": 1})
		case accessTokenPath:
			_ = request.ParseForm()
			if request.Form.Get("grant_type") == "refresh_token" {
				_ = json.NewEncoder(writer).Encode(map[string]any{"access_token": "ghu_refreshed_abcdefghijklmnopqrstuvwxyz",
					"refresh_token": "ghr_rotated_abcdefghijklmnopqrstuvwxyz", "token_type": "bearer",
					"expires_in": 28800, "refresh_token_expires_in": 15811200})
				return
			}
			if polls.Add(1) == 1 {
				_ = json.NewEncoder(writer).Encode(map[string]any{"error": "authorization_pending"})
				return
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"access_token": "ghu_initial_abcdefghijklmnopqrstuvwxyz",
				"refresh_token": "ghr_initial_abcdefghijklmnopqrstuvwxyz", "token_type": "bearer",
				"expires_in": 1, "refresh_token_expires_in": 3600})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	store := credential.NewMemoryStore()
	manager, err := NewAuthManagerForTest(store, "Iv1.test-client-id", server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 21, 1, 2, 3, 0, time.UTC)
	manager.now = func() time.Time { return now }
	ref := CredentialReference{Name: "github-review-test", Kind: AuthGitHubAppDevice}
	authorization, err := manager.BeginDeviceFlow(context.Background(), ref)
	if err != nil || authorization.UserCode != "ABCD-EFGH" ||
		authorization.PollIntervalMS != 1000 ||
		strings.Contains(string(mustJSON(t, authorization)), "device-code-secret") {
		t.Fatalf("device authorization boundary failed: %v %#v", err, authorization)
	}
	pending, err := manager.PollDeviceFlow(context.Background(), authorization.SessionID)
	if err != nil || pending.State != DevicePending || pending.Configured {
		t.Fatalf("pending device poll failed: %v %#v", err, pending)
	}
	now = now.Add(time.Second)
	authorized, err := manager.PollDeviceFlow(context.Background(), authorization.SessionID)
	if err != nil || authorized.State != DeviceAuthorized || !authorized.Configured {
		t.Fatalf("authorized device poll failed: %v %#v", err, authorized)
	}
	raw, found, err := store.Get(context.Background(), ref.Name)
	if err != nil || !found || strings.Contains(raw, "device-code-secret") ||
		!strings.Contains(raw, "ghu_initial_") {
		t.Fatalf("credential bundle was not stored safely: found=%t err=%v", found, err)
	}
	now = now.Add(3 * time.Minute)
	lease, err := manager.resolve(context.Background(), ref)
	if err != nil || !strings.HasPrefix(lease.value, "ghu_refreshed_") {
		t.Fatalf("expired credential was not refreshed: %v", err)
	}
	status, err := manager.Status(context.Background(), ref)
	if err != nil || !status.Configured || !status.Refreshable || status.StoreKind != "memory_test_only" {
		t.Fatalf("credential status projection failed: %v %#v", err, status)
	}
}

func TestDeviceFlowRejectsNonLoopbackTestEndpointAndNeverProjectsToken(t *testing.T) {
	store := credential.NewMemoryStore()
	if _, err := NewAuthManagerForTest(store, "Iv1.client", "https://example.com", nil); err == nil {
		t.Fatal("non-loopback OAuth test endpoint was accepted")
	}
	if err := store.Put(context.Background(), "github-pat",
		"github_pat_abcdefghijklmnopqrstuvwxyz0123456789"); err != nil {
		t.Fatal(err)
	}
	manager, err := NewAuthManager(store, "Iv1.client")
	if err != nil {
		t.Fatal(err)
	}
	status, err := manager.Status(context.Background(), CredentialReference{
		Name: "github-pat", Kind: AuthFineGrainedPAT})
	encoded := string(mustJSON(t, status))
	if err != nil || strings.Contains(encoded, "github_pat_") {
		t.Fatalf("credential status leaked a token: %v %s", err, encoded)
	}
}

func TestDeviceFlowRejectsOversizedOAuthResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"device_code":"device-code-secret","user_code":"ABCD-EFGH","verification_uri":"https://github.com/login/device","expires_in":900,"interval":5}` +
			strings.Repeat(" ", maxOAuthBody)))
	}))
	defer server.Close()
	manager, err := NewAuthManagerForTest(credential.NewMemoryStore(), "Iv1.client",
		server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.BeginDeviceFlow(context.Background(), CredentialReference{
		Name: "github-device-bound", Kind: AuthGitHubAppDevice})
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != FailureResponseBound {
		t.Fatalf("oversized OAuth response was not rejected: %v", err)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
