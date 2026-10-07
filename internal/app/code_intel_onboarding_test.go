package app

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/httpapi"
)

func TestAPICodeIntelOnboardingUsesManagedModeAndPreservesExplicitConfigPriority(t *testing.T) {
	for _, mode := range []string{"managed", "flag", "environment"} {
		t.Run(mode, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("CYBERAGENT_HOME", home)
			t.Setenv(codeIntelConfigEnvironment, "")
			t.Setenv(apiTokenEnvironment, "onboard-api-read-token-0123456789-abcdefgh")
			t.Setenv(apiControlTokenEnvironment, "onboard-api-control-token-0123456789-abcdef")
			args := []string{"api", "serve", "--listen", "127.0.0.1:0"}
			if mode != "managed" {
				selected := writeCodeIntelCLIConfig(t, "workspace-selected-lsp")
				if err := os.WriteFile(filepath.Join(home, "code-intel.json"), []byte("corrupt ignored managed configuration"), 0o600); err != nil {
					t.Fatal(err)
				}
				if mode == "flag" {
					args = append(args, "--code-intel-config", selected)
				} else {
					t.Setenv(codeIntelConfigEnvironment, selected)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			var stdout, stderr synchronizedBuffer
			done := make(chan int, 1)
			go func() { done <- ExecuteContext(ctx, args, &stdout, &stderr) }()
			output := waitForAPIProcessOutput(t, &stdout, &stderr, done, func(value string) bool { return outputField(value, "api_url") != "" })
			base := strings.TrimSuffix(outputField(output, "api_url"), "/api/v1")
			read := func(path string, value any) {
				request, err := http.NewRequest(http.MethodGet, base+path, nil)
				if err != nil {
					t.Fatal(err)
				}
				request.Header.Set("Authorization", "Bearer onboard-api-read-token-0123456789-abcdefgh")
				response, err := (&http.Client{Timeout: 2 * time.Second}).Do(request)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				if response.StatusCode != http.StatusOK {
					t.Fatalf("%s status=%d", path, response.StatusCode)
				}
				var envelope struct {
					Data json.RawMessage `json:"data"`
				}
				if err = json.NewDecoder(response.Body).Decode(&envelope); err != nil {
					t.Fatal(err)
				}
				if err = json.Unmarshal(envelope.Data, value); err != nil {
					t.Fatal(err)
				}
			}
			var extensions httpapi.ExtensionInventoryView
			read(httpapi.ExtensionInventoryPath, &extensions)
			if extensions.Onboarding == nil || extensions.Onboarding.LSPConfiguration != (mode == "managed") {
				t.Fatalf("mode=%s onboarding=%#v", mode, extensions.Onboarding)
			}
			var inventory httpapi.CodeIntelInventoryView
			read(httpapi.CodeIntelInventoryPath, &inventory)
			if mode == "managed" {
				if len(inventory.Servers) != 0 || len(inventory.Configurations) != 0 {
					t.Fatal("empty managed runtime invented configuration")
				}
			} else if len(inventory.Servers) != 1 || inventory.Servers[0].ServerID != "cli-test-lsp" {
				t.Fatalf("selected explicit config was not authoritative: %#v", inventory)
			}
			cancel()
			select {
			case code := <-done:
				if code != 0 || stderr.String() != "" {
					t.Fatalf("API shutdown code=%d stderr=%s", code, stderr.String())
				}
			case <-time.After(5 * time.Second):
				t.Fatal("API did not stop")
			}
		})
	}
}
