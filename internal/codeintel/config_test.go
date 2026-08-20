package codeintel

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadConfigGeneratesSourceAndPinsReviewedExecutable(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable = filepath.Clean(executable)
	digest, available, err := executableDigest(executable)
	if err != nil || !available {
		t.Fatalf("hash executable: available=%t err=%v", available, err)
	}
	config := Config{ProtocolVersion: ConfigProtocolVersion, Servers: []ServerDescriptor{{
		ProtocolVersion: ProtocolVersion, ID: "gopls", Name: "Reviewed gopls",
		WorkspaceID: "workspace-config", Languages: []Language{{ID: "go",
			Extensions: []string{".go"}}}, Executable: executable,
		Arguments: []string{"serve"}, ExecutableSHA256: digest,
		ReviewedBy: "operator", ReviewedAt: time.Now().UTC(),
	}}}
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "code-intel.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, configDigest, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Servers) != 1 || loaded.Servers[0].Source.Kind != "operator_config" ||
		loaded.Servers[0].Source.Label != "code-intel.json" ||
		loaded.Servers[0].Source.SHA256 != configDigest ||
		loaded.Servers[0].RequestTimeoutMillis != DefaultRequestTimeout.Milliseconds() {
		t.Fatalf("process-owned source was not generated: %#v", loaded)
	}
	manager, err := NewManager(loaded.Servers)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeTestManager(manager) })
	root := t.TempDir()
	qualification := manager.Qualify(context.Background(), "workspace-config", root)
	if len(qualification) != 1 || !qualification[0].Eligible ||
		!qualification[0].ExecutableHashMatched || !qualification[0].Reviewed ||
		!qualification[0].ProcessOwned || !qualification[0].MinimalEnvironment ||
		qualification[0].NetworkAccessGranted || qualification[0].CredentialsGranted ||
		qualification[0].ShellProfileLoaded {
		t.Fatalf("unexpected qualification: %#v", qualification)
	}
}

func TestLoadConfigRejectsUntrustedOrRedirectedConfiguration(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable = filepath.Clean(executable)
	digest, _, err := executableDigest(executable)
	if err != nil {
		t.Fatal(err)
	}
	base := ServerDescriptor{ProtocolVersion: ProtocolVersion, ID: "server",
		Name: "Reviewed server", WorkspaceID: "workspace-config",
		Languages:  []Language{{ID: "go", Extensions: []string{".go"}}},
		Executable: executable, ExecutableSHA256: digest,
		RequestTimeoutMillis: time.Second.Milliseconds(), ReviewedBy: "operator",
		ReviewedAt: time.Now().UTC()}
	tests := []struct {
		name   string
		mutate func(*ServerDescriptor)
	}{
		{name: "secret argument", mutate: func(value *ServerDescriptor) {
			value.Arguments = []string{"--api-key=super-secret-value"}
		}},
		{name: "secret argument value", mutate: func(value *ServerDescriptor) {
			value.Arguments = []string{"--label", "sk-123456789012345678901234567890"}
		}},
		{name: "secret initialization field", mutate: func(value *ServerDescriptor) {
			value.InitializationOptions = json.RawMessage(`{"apiKey":"not-allowed"}`)
		}},
		{name: "secret initialization value", mutate: func(value *ServerDescriptor) {
			value.InitializationOptions = json.RawMessage(
				`{"label":"sk-123456789012345678901234567890"}`)
		}},
		{name: "secret projected name", mutate: func(value *ServerDescriptor) {
			value.Name = "sk-123456789012345678901234567890"
		}},
		{name: "relative executable", mutate: func(value *ServerDescriptor) {
			value.Executable = "gopls"
		}},
		{name: "unreviewed", mutate: func(value *ServerDescriptor) {
			value.ReviewedBy = ""
		}},
		{name: "supplied source", mutate: func(value *ServerDescriptor) {
			value.Source = Source{Kind: "operator_config", Label: "forged.json",
				SHA256: strings.Repeat("a", 64)}
		}},
	}
	for _, current := range tests {
		t.Run(current.name, func(t *testing.T) {
			descriptor := base
			current.mutate(&descriptor)
			raw, err := json.Marshal(Config{ProtocolVersion: ConfigProtocolVersion,
				Servers: []ServerDescriptor{descriptor}})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "code-intel.json")
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := LoadConfig(path); err == nil {
				t.Fatal("untrusted Code Intel configuration was accepted")
			}
		})
	}

	validRaw, err := json.Marshal(Config{ProtocolVersion: ConfigProtocolVersion,
		Servers: []ServerDescriptor{base}})
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	target := filepath.Join(directory, "target.json")
	link := filepath.Join(directory, "linked.json")
	if err := os.WriteFile(target, validRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err == nil {
		if _, _, err := LoadConfig(link); err == nil {
			t.Fatal("symlinked Code Intel configuration was accepted")
		}
	}
	unsafeName := filepath.Join(directory, "sk-123456789012345678901234567890.json")
	if err := os.WriteFile(unsafeName, validRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadConfig(unsafeName); err == nil {
		t.Fatal("secret-shaped Code Intel config filename was accepted")
	}
}

func TestQualificationDetectsExecutableHashDrift(t *testing.T) {
	root := t.TempDir()
	descriptor := testServerDescriptor(t, root, "normal", time.Second)
	descriptor.ExecutableSHA256 = strings.Repeat("0", 64)
	manager, err := NewManager([]ServerDescriptor{descriptor})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeTestManager(manager) })
	qualification := manager.Qualify(context.Background(), helperWorkspaceID, root)
	if len(qualification) != 1 || qualification[0].Eligible ||
		qualification[0].ExecutableHashMatched ||
		!strings.Contains(qualification[0].Reason, "no longer matches") {
		t.Fatalf("hash drift was not diagnosed: %#v", qualification)
	}
}
