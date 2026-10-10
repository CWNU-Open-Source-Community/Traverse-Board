package sandbox

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

const sbxDaemonTestHealth = `{"api_version":"0.38.0","release":true,"revision":"0411f50ee4700fe7bd37e6e7e3aced563e850ca9","status":"healthy","version":"v0.47.0"}`
const sbxDaemonTestUUID = "00000000-0000-4000-8000-000000000001"

type sbxDaemonRoundTrip func(*http.Request) (*http.Response, error)

func (roundtrip sbxDaemonRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundtrip(request)
}

func sbxDaemonTestResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestSBXDaemonRemovalAlwaysCarriesImmutableIDCondition(t *testing.T) {
	name := "traverse-sbx-" + strings.Repeat("a", 32)
	requests := 0
	transport := &sbxDaemonHTTPTransport{client: &http.Client{Transport: sbxDaemonRoundTrip(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.URL.Host != "sbx.local" || request.URL.Scheme != "http" || request.Header.Get("Authorization") != "" || request.Body != nil {
			t.Fatal("endpoint, credentials or body escaped the compiled local contract")
		}
		if requests == 1 {
			if request.Method != http.MethodGet || request.URL.Path != "/daemon/health" {
				t.Fatal("conditional deletion did not first check daemon compatibility")
			}
			return sbxDaemonTestResponse(http.StatusOK, sbxDaemonTestHealth), nil
		}
		query := request.URL.Query()
		if request.Method != http.MethodDelete || request.URL.Path != "/sandbox/"+name || len(query) != 3 ||
			query.Get("expected_id") != sbxDaemonTestUUID || query.Get("force") != "true" || query.Get("delete_scoped_secrets") != "false" {
			t.Fatal("cleanup request lost the exact-ID condition or scoped contract")
		}
		return sbxDaemonTestResponse(http.StatusNoContent, ""), nil
	})}}
	if err := transport.Remove(context.Background(), name, sbxDaemonTestUUID); err != nil || requests != 2 {
		t.Fatalf("conditional deletion failed: requests=%d error=%v", requests, err)
	}
}

func TestSBXDaemonRejectsIdentityBeforeAnyRequest(t *testing.T) {
	requests := 0
	transport := &sbxDaemonHTTPTransport{client: &http.Client{Transport: sbxDaemonRoundTrip(func(*http.Request) (*http.Response, error) {
		requests++
		return sbxDaemonTestResponse(http.StatusNoContent, ""), nil
	})}}
	name := "traverse-sbx-" + strings.Repeat("a", 32)
	for _, value := range [][2]string{{name, ""}, {name, "name-only"}, {"other-sandbox", sbxDaemonTestUUID}, {name + "/child", sbxDaemonTestUUID}} {
		if err := transport.Remove(context.Background(), value[0], value[1]); !errors.Is(err, ErrSBXOwnership) {
			t.Fatalf("invalid identity accepted: %v", err)
		}
	}
	if requests != 0 {
		t.Fatal("invalid ownership caused an API request")
	}
}

func TestSBXDaemonIncompatibleHealthPreventsDeletion(t *testing.T) {
	for _, health := range []string{
		strings.Replace(sbxDaemonTestHealth, "v0.47.0", "v0.48.0", 1),
		strings.Replace(sbxDaemonTestHealth, "0.38.0", "0.39.0", 1),
		strings.Replace(sbxDaemonTestHealth, `"release":true`, `"release":false`, 1),
		strings.Replace(sbxDaemonTestHealth, "healthy", "unhealthy", 1),
		strings.Replace(sbxDaemonTestHealth, `"version":"v0.47.0"`, `"version":"v0.48.0","version":"v0.47.0"`, 1),
		`{}`,
	} {
		t.Run(health, func(t *testing.T) {
			requests := 0
			transport := &sbxDaemonHTTPTransport{client: &http.Client{Transport: sbxDaemonRoundTrip(func(request *http.Request) (*http.Response, error) {
				requests++
				if request.Method != http.MethodGet {
					t.Fatal("incompatible daemon received deletion")
				}
				return sbxDaemonTestResponse(http.StatusOK, health), nil
			})}}
			if err := transport.Remove(context.Background(), "traverse-sbx-"+strings.Repeat("a", 32), sbxDaemonTestUUID); !errors.Is(err, ErrSBXUnavailable) || requests != 1 {
				t.Fatalf("incompatible daemon accepted: requests=%d error=%v", requests, err)
			}
		})
	}
}

func TestSBXDaemonRemovalRetainsUncertainAndReplacedResources(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"conditional-replacement", http.StatusConflict, `{"message":"replacement"}`, ErrSBXOwnership},
		{"precondition-failed", http.StatusPreconditionFailed, `{}`, ErrSBXOwnership},
		{"missing-is-not-removed", http.StatusNotFound, `{"removed":true}`, ErrSBXCleanup},
		{"server-unavailable", http.StatusInternalServerError, `{}`, ErrSBXCleanup},
		{"unexpected-success", http.StatusAccepted, `{}`, ErrSBXCleanup},
		{"duplicate-receipt", http.StatusOK, `{"removed":false,"removed":true}`, ErrSBXCleanup},
		{"ambiguous-array", http.StatusOK, `[]`, ErrSBXCleanup},
		{"oversized-receipt", http.StatusOK, strings.Repeat(" ", sbxDaemonBodyLimit+1), ErrSBXOutputLimit},
		{"empty-204", http.StatusNoContent, "", nil},
		{"object-200", http.StatusOK, `{"removed_credential_sources":[]}`, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			transport := &sbxDaemonHTTPTransport{client: &http.Client{Transport: sbxDaemonRoundTrip(func(request *http.Request) (*http.Response, error) {
				if request.Method == http.MethodGet {
					return sbxDaemonTestResponse(http.StatusOK, sbxDaemonTestHealth), nil
				}
				return sbxDaemonTestResponse(test.status, test.body), nil
			})}}
			err := transport.Remove(context.Background(), "traverse-sbx-"+strings.Repeat("a", 32), sbxDaemonTestUUID)
			if !errors.Is(err, test.want) {
				t.Fatalf("cleanup result=%v want=%v", err, test.want)
			}
		})
	}
}

func TestSBXDaemonDefaultClientDoesNotFollowRedirects(t *testing.T) {
	transport, supported := newSBXDaemonTransport().(*sbxDaemonHTTPTransport)
	if !supported {
		t.Skip("fixed daemon endpoint not yet accepted on this platform")
	}
	requests := 0
	transport.client.Transport = sbxDaemonRoundTrip(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.URL.Path == "/daemon/health" {
			return sbxDaemonTestResponse(http.StatusOK, sbxDaemonTestHealth), nil
		}
		response := sbxDaemonTestResponse(http.StatusTemporaryRedirect, "")
		response.Header.Set("Location", "https://unrelated.invalid/sandbox/foreign")
		return response, nil
	})
	if err := transport.Remove(context.Background(), "traverse-sbx-"+strings.Repeat("a", 32), sbxDaemonTestUUID); !errors.Is(err, ErrSBXCleanup) || requests != 2 {
		t.Fatalf("local removal followed redirect: requests=%d error=%v", requests, err)
	}
}

func TestSBXDaemonDoesNotExposeTransportSecrets(t *testing.T) {
	transport := &sbxDaemonHTTPTransport{client: &http.Client{Transport: sbxDaemonRoundTrip(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("PRIVATE-DAEMON-PAYLOAD")
	})}}
	err := transport.Check(context.Background())
	if !errors.Is(err, ErrSBXUnavailable) || strings.Contains(err.Error(), "PRIVATE-DAEMON-PAYLOAD") {
		t.Fatalf("daemon error leaked: %v", err)
	}
}
