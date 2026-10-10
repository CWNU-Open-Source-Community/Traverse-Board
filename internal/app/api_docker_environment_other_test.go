//go:build !windows

package app

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/sandbox"
)

type apiEnvironmentDockerTransport struct{ digest string }

func (apiEnvironmentDockerTransport) Endpoint() sandbox.DockerObservationEndpoint {
	endpoint, _ := sandbox.NewDockerObservationEndpoint(sandbox.DockerObservationEndpointLocalUnix)
	return endpoint
}
func (apiEnvironmentDockerTransport) Ping(context.Context) error { return nil }
func (apiEnvironmentDockerTransport) Version(context.Context) (sandbox.DockerDaemonVersion, error) {
	return sandbox.DockerDaemonVersion{APIVersion: "1.47", MinAPIVersion: "1.24", EngineVersion: "27.5.1", OSType: "linux", Architecture: "amd64"}, nil
}
func (apiEnvironmentDockerTransport) Info(context.Context) (sandbox.DockerDaemonInfo, error) {
	return sandbox.DockerDaemonInfo{ID: "fixture", ServerVersion: "27.5.1", OSType: "linux", Architecture: "amd64", NCPU: 8, MemoryBytes: 8 * 1024 * 1024 * 1024, PidsLimit: true}, nil
}
func (transport apiEnvironmentDockerTransport) InspectImage(context.Context, string) (sandbox.DockerImageInspection, error) {
	return sandbox.DockerImageInspection{ID: transport.digest, RepoDigests: []string{"fixture@" + transport.digest}, OSType: "linux", Architecture: "amd64", SizeBytes: 1024, User: "65532:65532", RootFSType: "layers", GraphDriver: "overlay2"}, nil
}

func TestAPIServeDockerRuntimeAssemblesWithUnsupportedLocalBackend(t *testing.T) {
	t.Setenv("CYBERAGENT_HOME", newCanonicalCLIHome(t))
	t.Setenv("MIMO_API_KEY", "")
	t.Setenv("DEEPSEEK_API_KEY", "")
	t.Setenv("CYBERAGENT_ANTHROPIC_API_KEY", "")
	token := "cli-read-test-0123456789-abcdefghijkl"
	t.Setenv(apiTokenEnvironment, token)
	t.Setenv(apiControlTokenEnvironment, "cli-control-test-0123456789-abcdefgh")
	digest := "sha256:" + strings.Repeat("a", 64)
	t.Setenv(standardCodeDockerImageEnvironment, digest)
	probe, err := sandbox.NewDockerReadinessProbe(apiEnvironmentDockerTransport{digest: digest})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var stdout, stderr synchronizedBuffer
	done := make(chan int, 1)
	go func() {
		done <- executeContextWithConfig(ctx, []string{"api", "serve", "--listen", "127.0.0.1:0", "--enable-permission-control", "--enable-workspace-sandbox", "--enable-docker-execution"}, &stdout, &stderr, func(app *App) { app.dockerReadinessProbe = probe })
	}()
	output := waitForAPIProcessOutput(t, &stdout, &stderr, done, func(value string) bool {
		return outputField(value, "api_url") != "" && outputField(value, "command_runtime_enabled") != ""
	})
	if outputField(output, "workspace_sandbox_enabled") != "true" || outputField(output, "command_runtime_enabled") != "true" {
		t.Fatalf("Docker assembly was blocked by unsupported Local: %s %s", output, stderr.String())
	}
	request, _ := http.NewRequest(http.MethodGet, outputField(output, "api_url")+"/capabilities", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := (&http.Client{Timeout: 2 * time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil || response.StatusCode != http.StatusOK || !bytes.Contains(body, []byte(`"standard_code_preset_enabled":true`)) {
		t.Fatalf("Docker Standard Code preset unavailable: %d %s %v", response.StatusCode, body, readErr)
	}
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("API shutdown failed: %d %s", code, stderr.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("API did not shut down")
	}
}
