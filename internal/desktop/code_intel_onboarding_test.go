package desktop

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cyberagent-workbench/internal/codeintel"
	"cyberagent-workbench/internal/httpapi"
)

func TestDesktopCodeIntelManagedOnboardingAndExplicitReadOnlyPriority(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "managed", true: "explicit"}[explicit], func(t *testing.T) {
			home := t.TempDir()
			config := ControlPlaneConfig{DatabasePath: filepath.Join(home, "desktop.db"), HomePath: home, ReadToken: desktopControlPlaneTestToken,
				ControlToken: desktopControlPlaneControlToken, AppVersion: "desktop-lsp-onboarding-test"}
			if explicit {
				executable, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				selected := codeintel.Config{ProtocolVersion: codeintel.ConfigProtocolVersion, Servers: []codeintel.ServerDescriptor{{ProtocolVersion: codeintel.ProtocolVersion,
					ID: "desktop-selected-lsp", Name: "Desktop selected LSP", WorkspaceID: "workspace-desktop-selected", Languages: []codeintel.Language{{ID: "go", Extensions: []string{".go"}}},
					Executable: filepath.Clean(executable), ExecutableSHA256: strings.Repeat("a", 64), RequestTimeoutMillis: 1000, ReviewedBy: "fixture_operator", ReviewedAt: time.Unix(1, 0).UTC()}}}
				raw, err := json.Marshal(selected)
				if err != nil {
					t.Fatal(err)
				}
				config.CodeIntelConfigPath = filepath.Join(t.TempDir(), "selected-code-intel.json")
				if err = os.WriteFile(config.CodeIntelConfigPath, raw, 0o600); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(filepath.Join(home, "code-intel.json"), []byte("corrupt ignored managed configuration"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			plane, err := OpenControlPlane(config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = plane.Close() })
			response := desktopAPIRequest(plane.Handler(), httpapi.ExtensionInventoryPath)
			if response.Code != http.StatusOK {
				t.Fatalf("inventory status=%d body=%s", response.Code, response.Body.String())
			}
			var envelope desktopAPIEnvelope
			if err = json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			var inventory httpapi.ExtensionInventoryView
			if err = json.Unmarshal(envelope.Data, &inventory); err != nil {
				t.Fatal(err)
			}
			if inventory.Onboarding == nil || inventory.Onboarding.LSPConfiguration == explicit {
				t.Fatalf("explicit=%t onboarding=%#v", explicit, inventory.Onboarding)
			}
			servers := plane.codeIntelManager.Inventory()
			if explicit {
				if len(servers) != 1 || servers[0].ServerID != "desktop-selected-lsp" {
					t.Fatalf("explicit config lost priority: %#v", servers)
				}
			} else if len(servers) != 0 {
				t.Fatal("empty Desktop runtime invented configuration")
			}
		})
	}
}
