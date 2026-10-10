package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

type sbxFakeTransport struct {
	mu                sync.Mutex
	entries           []sbxInventoryEntry
	calls             []SBXProcessRequest
	ssh               string
	exec              func(context.Context, *sbxFakeTransport) (SBXProcessResult, error)
	createError       error
	rmRetains         bool
	inventoryOverride []byte
}

func (f *sbxFakeTransport) Run(ctx context.Context, r SBXProcessRequest) (SBXProcessResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, r)
	args := r.Arguments
	if len(args) < 3 || args[0] != "--app-name" || args[1] != SBXAppName {
		f.mu.Unlock()
		return SBXProcessResult{ExitCode: -1}, errors.New("missing fixed sbx namespace")
	}
	args = args[2:]
	result := SBXProcessResult{ExitCode: 0}
	switch args[0] {
	case "version":
		result.Stdout = []byte("sbx version v0.47.0\n")
	case "settings":
		result.Stdout = []byte(f.ssh + "\n")
	case "ls":
		if f.inventoryOverride != nil {
			result.Stdout = append([]byte{}, f.inventoryOverride...)
		} else {
			entries := append([]sbxInventoryEntry{}, f.entries...)
			result.Stdout, _ = json.Marshal(struct {
				Sandboxes []sbxInventoryEntry `json:"sandboxes"`
			}{entries})
		}
	case "create":
		name := args[slices.Index(args, "--name")+1]
		f.entries = append(f.entries, sbxInventoryEntry{ID: "immutable-owned-id", Name: name, Workspaces: append([]string{}, args[len(args)-2:]...)})
		if f.createError != nil {
			result.ExitCode = -1
			err := f.createError
			f.mu.Unlock()
			return result, err
		}
	case "policy":
	case "exec":
		fn := f.exec
		f.mu.Unlock()
		if fn != nil {
			return fn(ctx, f)
		}
		return SBXProcessResult{ExitCode: 0, Stdout: []byte("guest-output\n")}, nil
	case "stop":
	case "rm":
		if !f.rmRetains {
			name := args[len(args)-1]
			f.entries = slices.DeleteFunc(f.entries, func(e sbxInventoryEntry) bool { return e.Name == name })
		}
	default:
		f.mu.Unlock()
		return SBXProcessResult{ExitCode: -1}, errors.New("unexpected fake CLI verb")
	}
	f.mu.Unlock()
	return result, nil
}
func (f *sbxFakeTransport) count(verb string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, r := range f.calls {
		if len(r.Arguments) > 2 && r.Arguments[2] == verb {
			count++
		}
	}
	return count
}

func sbxFixture(t *testing.T) (*SBXBackend, *sbxFakeTransport, SBXRunRequest) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cli := filepath.Join(base, "sbx-fixture.exe")
	if err := os.WriteFile(cli, []byte("fake CLI bytes; never executed"), 0700); err != nil {
		t.Fatal(err)
	}
	journal, drydock := filepath.Join(base, "owned-journal"), filepath.Join(base, "owned-drydock")
	for _, root := range []string{journal, drydock} {
		if err := os.Mkdir(root, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(drydock, ".git"), []byte("gitdir: /host-owned-metadata\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fake := &sbxFakeTransport{ssh: "false"}
	b, err := NewSBXBackend(SBXBackendConfig{Enabled: true, ExecutablePath: cli, TemplateReference: "docker.io/example/toolchain@sha256:" + strings.Repeat("a", 64), JournalRoot: journal}, WithSBXProcessTransport(fake), func(b *SBXBackend) { b.mcpIsolationProven = true })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	r := SBXRunRequest{OperationKey: "run-1/operation-1", RequestFingerprint: strings.Repeat("b", 64), RuntimeGeneration: b.Generation(),
		DrydockRoot: drydock, Arguments: []string{"/usr/bin/printf", "literal --cloud %s"}, Environment: []string{"HOME=/home/agent", "PATH=/usr/bin:/bin"},
		WorkingDirectory: ".", Timeout: time.Second, OutputLimit: 4096, AuthorityCheck: func(context.Context) error { return nil }}
	return b, fake, r
}

func TestSBXRunUsesFixedLocalPolicyAndConfirmsCompleteVMRemoval(t *testing.T) {
	b, fake, r := sbxFixture(t)
	value, err := b.Run(context.Background(), r, nil)
	if err != nil || !value.TreeReaped || value.ExitCode != 0 || string(value.Stdout) != "guest-output\n" || !sbxSHA(value.ReceiptFingerprint) {
		t.Fatalf("result=%+v error=%v", value, err)
	}
	if fake.count("stop") != 1 || fake.count("rm") != 1 {
		t.Fatalf("whole-VM cleanup was not performed: %+v", fake.calls)
	}
	for _, call := range fake.calls {
		if slices.Contains(call.Arguments, "--cloud") || slices.Contains(call.Arguments, "--all") || slices.Contains(call.Arguments, "prune") || slices.Contains(call.Arguments, "--privileged") {
			t.Fatalf("unsafe host CLI flags: %+v", call.Arguments)
		}
		for _, binding := range call.Environment {
			name, _, _ := strings.Cut(binding, "=")
			if strings.Contains(name, "TOKEN") || strings.Contains(name, "API_KEY") || name == "SSH_AUTH_SOCK" || name == "DOCKER_HOST" {
				t.Fatalf("host credentials were forwarded: %s", name)
			}
		}
		if call.Arguments[2] == "create" {
			for flag, wanted := range map[string]string{"--skills": "off", "--pull": "never", "--deny-network": "**", "--template": b.TemplateReference()} {
				index := slices.Index(call.Arguments, flag)
				if index < 0 || call.Arguments[index+1] != wanted {
					t.Fatalf("missing fixed policy %s", flag)
				}
			}
			if call.Arguments[len(call.Arguments)-1] != filepath.Join(r.DrydockRoot, ".git")+":ro" || slices.Contains(call.Arguments, "--static-mcp") {
				t.Fatal("host Git mask or empty MCP selection changed")
			}
		}
	}
	if _, err := b.Run(context.Background(), r, nil); !errors.Is(err, ErrSBXReplay) || fake.count("exec") != 1 {
		t.Fatalf("replayed command: %v", err)
	}
}

func TestSBXReadinessIsIndependentOfFeatureAndTemplateConfiguration(t *testing.T) {
	b, fake, _ := sbxFixture(t)
	b.config.Enabled = false
	b.config.TemplateReference = ""
	proof, err := b.Readiness(context.Background())
	if err != nil || proof.Validate() != nil || proof.Ready || !proof.CLIInstalled || proof.FeatureEnabled || proof.TemplateConfigured || fake.count("create") != 0 || fake.count("ls") != 0 {
		t.Fatalf("proof=%+v error=%v", proof, err)
	}
	b.config.Enabled = true
	b.config.TemplateReference = "example/toolchain@sha256:" + strings.Repeat("a", 64)
	proof, err = b.Readiness(context.Background())
	if err != nil || !proof.ReadyAt(proof.CheckedAt) || proof.ReadyAt(proof.ExpiresAt) || proof.ReadyAt(proof.CheckedAt.Add(-time.Second)) {
		t.Fatalf("invalid proof lifetime: %+v error=%v", proof, err)
	}
	fake.ssh = "true"
	proof, _ = b.Readiness(context.Background())
	if proof.Ready || proof.CredentialIsolationProven || proof.ReasonCode != "ssh_forwarding_not_disabled" {
		t.Fatalf("SSH forwarding was admitted: %+v", proof)
	}
}

func TestSBXProductionReadinessRefusesUnverifiedMCPIsolation(t *testing.T) {
	b, fake, r := sbxFixture(t)
	b.mcpIsolationProven = false
	proof, err := b.Readiness(context.Background())
	if err != nil || proof.Validate() != nil || proof.Ready || proof.CredentialIsolationProven || proof.MCPIsolationProven ||
		proof.ReasonCode != "mcp_isolation_unverified" || !proof.CLIInstalled || !proof.DaemonReachable {
		t.Fatalf("unverified host gateway admitted: %+v error=%v", proof, err)
	}
	if _, err := b.Run(context.Background(), r, nil); !errors.Is(err, ErrSBXUnavailable) || fake.count("create") != 0 || fake.count("exec") != 0 {
		t.Fatalf("blocked isolation created a VM: %v", err)
	}
}

func TestSBXCancellationReapsVMWithIndependentContext(t *testing.T) {
	b, fake, r := sbxFixture(t)
	started := make(chan struct{})
	fake.exec = func(ctx context.Context, _ *sbxFakeTransport) (SBXProcessResult, error) {
		close(started)
		<-ctx.Done()
		return SBXProcessResult{ExitCode: -1}, ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	completed := make(chan SBXExecutionResult, 1)
	failures := make(chan error, 1)
	go func() { value, err := b.Run(ctx, r, nil); completed <- value; failures <- err }()
	<-started
	cancel()
	value, err := <-completed, <-failures
	if !errors.Is(err, context.Canceled) || !value.TreeReaped || fake.count("stop") != 1 || fake.count("rm") != 1 {
		t.Fatalf("result=%+v error=%v", value, err)
	}
}

func TestSBXCloseCancelsRunBeforeReleasingJournalOwnership(t *testing.T) {
	b, fake, r := sbxFixture(t)
	started := make(chan struct{})
	fake.exec = func(ctx context.Context, _ *sbxFakeTransport) (SBXProcessResult, error) {
		close(started)
		<-ctx.Done()
		return SBXProcessResult{ExitCode: -1}, ctx.Err()
	}
	completed := make(chan SBXExecutionResult, 1)
	go func() { value, _ := b.Run(context.Background(), r, nil); completed <- value }()
	<-started
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if value := <-completed; !value.TreeReaped || b.lock != nil || fake.count("rm") != 1 {
		t.Fatalf("shutdown released live VM ownership: %+v", value)
	}
	if _, err := b.Run(context.Background(), r, nil); !errors.Is(err, ErrSBXUnavailable) {
		t.Fatalf("closed backend accepted execution: %v", err)
	}
}

func TestSBXTimeoutDoesNotTreatCLIExitAsVMReaped(t *testing.T) {
	b, fake, r := sbxFixture(t)
	r.Timeout = 15 * time.Millisecond
	fake.exec = func(ctx context.Context, _ *sbxFakeTransport) (SBXProcessResult, error) {
		<-ctx.Done()
		return SBXProcessResult{ExitCode: -1}, ctx.Err()
	}
	fake.rmRetains = true
	value, err := b.Run(context.Background(), r, nil)
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, ErrSBXCleanup) || value.TreeReaped {
		t.Fatalf("false complete-VM receipt: %+v error=%v", value, err)
	}
}

func TestSBXIdentityReplacementIsNeverStoppedOrDeleted(t *testing.T) {
	b, fake, r := sbxFixture(t)
	fake.exec = func(_ context.Context, f *sbxFakeTransport) (SBXProcessResult, error) {
		f.mu.Lock()
		f.entries[0].ID = "foreign-replacement-id"
		f.mu.Unlock()
		return SBXProcessResult{ExitCode: 0}, nil
	}
	value, err := b.Run(context.Background(), r, nil)
	if !errors.Is(err, ErrSBXOwnership) || value.TreeReaped || fake.count("stop") != 0 || fake.count("rm") != 0 {
		t.Fatalf("foreign VM mutated: %+v error=%v", value, err)
	}
	if err := b.RecoverStartup(context.Background()); !errors.Is(err, ErrSBXOwnership) || fake.count("rm") != 0 {
		t.Fatalf("recovery adopted foreign VM: %v", err)
	}
}

func TestSBXRecoveryOnlyCleansJournaledExactIdentity(t *testing.T) {
	b, fake, r := sbxFixture(t)
	owned := sbxRecord{AppName: SBXAppName, Version: SBXPolicyVersion, OperationDigest: sbxDigest(r.OperationKey), RequestFingerprint: r.RequestFingerprint,
		Name: "traverse-sbx-" + strings.Repeat("c", 32), ID: "exact-owned-id", Workspace: r.DrydockRoot, Phase: "dispatching"}
	if err := b.save(owned); err != nil {
		t.Fatal(err)
	}
	fake.entries = []sbxInventoryEntry{{ID: owned.ID, Name: owned.Name, Workspaces: []string{owned.Workspace, filepath.Join(owned.Workspace, ".git") + ":ro"}},
		{ID: "foreign-vm", Name: "traverse-sbx-" + strings.Repeat("d", 32), Workspaces: []string{owned.Workspace}}}
	if err := b.RecoverStartup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fake.count("exec") != 0 || fake.count("rm") != 1 || len(fake.entries) != 1 || fake.entries[0].ID != "foreign-vm" {
		t.Fatalf("recovery replayed or removed foreign resource: %+v", fake.entries)
	}
	path := filepath.Join(b.config.JournalRoot, owned.OperationDigest+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var removed sbxRecord
	if err := json.Unmarshal(data, &removed); err != nil || !removed.Removed || removed.Phase != "removed" || removed.ID != owned.ID {
		t.Fatalf("successful rm did not persist removal: %+v error=%v", removed, err)
	}
	// A durable confirmed removal needs no new inventory or destructive calls.
	queries := fake.count("ls")
	if err := b.RecoverStartup(context.Background()); err != nil || fake.count("ls") != queries || fake.count("stop") != 1 || fake.count("rm") != 1 {
		t.Fatalf("confirmed removal was repeated: error=%v calls=%+v", err, fake.calls)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(data) {
		t.Fatalf("confirmed journal changed during repeated recovery: error=%v", err)
	}
}

func TestSBXRecoveryPreservesMissingInventoryJournal(t *testing.T) {
	for _, phase := range []string{"created", "dispatching"} {
		t.Run(phase, func(t *testing.T) {
			b, fake, r := sbxFixture(t)
			owned := sbxRecord{AppName: SBXAppName, Version: SBXPolicyVersion, OperationDigest: sbxDigest(r.OperationKey), RequestFingerprint: r.RequestFingerprint,
				Name: "traverse-sbx-" + strings.Repeat("c", 32), ID: "exact-owned-id", Workspace: r.DrydockRoot, Phase: phase}
			if err := b.save(owned); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(b.config.JournalRoot, owned.OperationDigest+".json")
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			// A failed daemon backend may omit a VM that still has runtime metadata.
			fake.entries = []sbxInventoryEntry{{ID: owned.ID, Name: owned.Name, Workspaces: []string{owned.Workspace, filepath.Join(owned.Workspace, ".git") + ":ro"}}}
			fake.inventoryOverride = []byte(`{"sandboxes":[]}`)
			for attempt := 1; attempt <= 2; attempt++ {
				if err := b.RecoverStartup(context.Background()); !errors.Is(err, ErrSBXCleanup) {
					t.Fatalf("recovery %d confirmed absent inventory as removal: %v", attempt, err)
				}
				after, err := os.ReadFile(path)
				if err != nil || string(after) != string(before) {
					t.Fatalf("recovery %d changed unresolved journal: error=%v", attempt, err)
				}
			}
			if fake.count("ls") != 2 || fake.count("create") != 0 || fake.count("exec") != 0 || fake.count("stop") != 0 || fake.count("rm") != 0 || len(fake.entries) != 1 || fake.entries[0].ID != owned.ID {
				t.Fatalf("missing inventory mutated or adopted VM: calls=%+v entries=%+v", fake.calls, fake.entries)
			}
		})
	}
}

func TestSBXRunDoesNotConfirmCleanupFromMissingInventory(t *testing.T) {
	b, fake, r := sbxFixture(t)
	fake.exec = func(_ context.Context, f *sbxFakeTransport) (SBXProcessResult, error) {
		f.mu.Lock()
		f.inventoryOverride = []byte(`{"sandboxes":[]}`)
		f.mu.Unlock()
		return SBXProcessResult{ExitCode: 0}, nil
	}
	value, err := b.Run(context.Background(), r, nil)
	if !errors.Is(err, ErrSBXCleanup) || value.TreeReaped || value.ReceiptFingerprint != "" || fake.count("stop") != 0 || fake.count("rm") != 0 {
		t.Fatalf("missing inventory produced a whole-VM cleanup receipt: %+v error=%v", value, err)
	}
	data, err := os.ReadFile(filepath.Join(b.config.JournalRoot, sbxDigest(r.OperationKey)+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var record sbxRecord
	if err := json.Unmarshal(data, &record); err != nil || record.Removed || record.Phase != "dispatching" || record.ID == "" {
		t.Fatalf("missing inventory lost active journal: %+v error=%v", record, err)
	}
}

func TestSBXPartialCreateCannotAdoptAnUnjournaledID(t *testing.T) {
	b, fake, r := sbxFixture(t)
	fake.createError = context.DeadlineExceeded
	value, err := b.Run(context.Background(), r, nil)
	if err == nil || value.TreeReaped || fake.count("exec") != 0 || fake.count("rm") != 0 {
		t.Fatalf("ambiguous create was adopted: %+v error=%v", value, err)
	}
	if err := b.RecoverStartup(context.Background()); !errors.Is(err, ErrSBXOwnership) {
		t.Fatalf("recovery guessed create ownership: %v", err)
	}
}

func TestSBXRecoveryRejectsOversizedOwnershipRecordWithoutCLI(t *testing.T) {
	b, fake, r := sbxFixture(t)
	path := filepath.Join(b.config.JournalRoot, sbxDigest(r.OperationKey)+".json")
	if err := os.WriteFile(path, []byte(strings.Repeat(" ", 128*1024)), 0600); err != nil {
		t.Fatal(err)
	}
	data, err := sbxReadRecord(path)
	if err != nil || len(data) != 64*1024+1 {
		t.Fatalf("ownership record read was not bounded: bytes=%d error=%v", len(data), err)
	}
	if err := b.RecoverStartup(context.Background()); !errors.Is(err, ErrSBXOwnership) || fake.count("ls") != 0 || fake.count("stop") != 0 || fake.count("rm") != 0 {
		t.Fatalf("oversized ownership record reached daemon: error=%v calls=%+v", err, fake.calls)
	}
}

func TestSBXAuthorityRevocationStopsBeforeCommandDispatch(t *testing.T) {
	b, fake, r := sbxFixture(t)
	denied := errors.New("permission generation revoked")
	r.AuthorityCheck = func(context.Context) error {
		if fake.count("create") > 0 {
			return denied
		}
		return nil
	}
	value, err := b.Run(context.Background(), r, nil)
	if !errors.Is(err, denied) || !value.TreeReaped || fake.count("exec") != 0 || fake.count("rm") != 1 {
		t.Fatalf("authority revoked but command dispatched: %+v error=%v", value, err)
	}
}

func TestSBXInventoryRejectsAmbiguousOrUndocumentedIdentity(t *testing.T) {
	b, fake, _ := sbxFixture(t)
	for _, input := range []string{`[]`, `{"items":[]}`, `{"sandboxes":null}`, `{"sandboxes":[],"sandboxes":[]}`, `{"sandboxes":[{"name":"foo","workspaces":[]}]}`, `{"sandboxes":[]} {}`} {
		fake.inventoryOverride = []byte(input)
		if _, err := b.inventory(context.Background()); err == nil {
			t.Fatalf("accepted ambiguous inventory: %s", input)
		}
	}
	fake.inventoryOverride = []byte(`{"sandboxes":[],"future_additive_field":true}`)
	if _, err := b.inventory(context.Background()); err != nil {
		t.Fatalf("rejected additive CLI field: %v", err)
	}
}

func TestSBXTemplateRejectsUnpinnedTagsAndHostFlags(t *testing.T) {
	for _, value := range []string{"latest", "ubuntu:latest", "sha256:" + strings.Repeat("a", 64), "--cloud", "https://example.com/image@sha256:" + strings.Repeat("a", 64), "example/image@sha256:" + strings.Repeat("A", 64)} {
		if ValidSBXTemplateReference(value) {
			t.Fatalf("accepted unpinned template: %q", value)
		}
	}
	if !ValidSBXTemplateReference("registry.example.com:5000/toolchain@sha256:" + strings.Repeat("a", 64)) {
		t.Fatal("rejected pinned registry template")
	}
}

func TestSBXBackendConfigValidatesAppNamespace(t *testing.T) {
	config := SBXBackendConfig{Enabled: true, TemplateReference: "example/toolchain@sha256:" + strings.Repeat("a", 64)}
	for _, tc := range []struct {
		name, appName string
		valid         bool
	}{
		{"fixed product namespace", SBXAppName, true},
		{"maximum length", strings.Repeat("a", 20), true},
		{"CLI allowed characters", "App_9-test", true},
		{"default user namespace", "", false},
		{"over maximum length", strings.Repeat("a", 21), false},
		{"previous oversized namespace", "traverse-command-runtime", false},
		{"path separator", "traverse/runtime", false},
		{"dot", "traverse.runtime", false},
		{"space", "traverse runtime", false},
		{"non ASCII", "traverse-运行", false},
		{"control character", "traverse-runtime\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := config.validate(tc.appName)
			if tc.valid && err != nil || !tc.valid && !errors.Is(err, ErrSBXBoundary) {
				t.Fatalf("config validation for namespace %q = %v; valid=%v", tc.appName, err, tc.valid)
			}
		})
	}
	// Exercise the normal constructor as well: a future invalid fixed namespace
	// must fail before any CLI dispatch or resource initialization.
	b, err := NewSBXBackend(config)
	if err != nil {
		t.Fatalf("fixed namespace rejected during backend initialization: %v", err)
	}
	t.Cleanup(func() { _ = b.Close() })
	config.TemplateReference = "example/toolchain:latest"
	if _, err := NewSBXBackend(config); !errors.Is(err, ErrSBXBoundary) {
		t.Fatalf("unpinned configuration was accepted: %v", err)
	}
}
