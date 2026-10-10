package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

type dockerEnvironmentStub struct{ calls int }

func (stub *dockerEnvironmentStub) DockerEnvironment(context.Context) (DockerEnvironmentView, error) {
	stub.calls++
	return DockerEnvironmentView{ProtocolVersion: "docker_environment.v1"}, nil
}

func TestDockerEnvironmentIsReadOnlyBoundedToTheFixedConfiguration(t *testing.T) {
	fixture := newAPIFixture(t)
	stub := &dockerEnvironmentStub{}
	fixture.api.dockerEnvironmentController = stub
	for _, current := range []struct {
		method, path, token string
		status              int
	}{
		{http.MethodGet, DockerEnvironmentPath, testAccessToken, http.StatusOK},
		{http.MethodGet, DockerEnvironmentPath, "", http.StatusUnauthorized},
		{http.MethodPost, DockerEnvironmentPath, testAccessToken, http.StatusMethodNotAllowed},
		{http.MethodGet, DockerEnvironmentPath + "?image=other", testAccessToken, http.StatusBadRequest},
		{http.MethodGet, DockerEnvironmentPath + "?endpoint=other", testAccessToken, http.StatusBadRequest},
	} {
		request := httptest.NewRequest(current.method, current.path, nil)
		request.Host = "127.0.0.1"
		request.RemoteAddr = "127.0.0.1:12345"
		if current.token != "" {
			request.Header.Set("Authorization", "Bearer "+current.token)
		}
		response := httptest.NewRecorder()
		fixture.api.ServeHTTP(response, request)
		if response.Code != current.status {
			t.Fatalf("%s %s: status %d body %s", current.method, current.path, response.Code, response.Body.String())
		}
		if current.status == http.StatusOK && response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("fresh readiness can be cached")
		}
	}
	if stub.calls != 1 {
		t.Fatalf("read-only probe called %d times", stub.calls)
	}
}
