package sandbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This opt-in test uses an installed, authenticated local sbx and an already
// cached pinned template. It creates only a new owned fixture and verifies the
// real daemon's conditional removal, including a same-name recreation.
func TestSBXRealConditionalRemovalRejectsReplacedIdentity(t *testing.T) {
	if os.Getenv("CYBERAGENT_SBX_LIVE_ACCEPTANCE") != "1" {
		t.Skip("set explicit SBX live acceptance paths to exercise the local daemon")
	}
	base := os.Getenv("CYBERAGENT_SBX_LIVE_ARTIFACT_ROOT")
	if !filepath.IsAbs(base) {
		t.Fatal("absolute owned artifact root required")
	}
	root, err := os.MkdirTemp(base, "conditional-removal-")
	if err != nil {
		t.Fatal(err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	journal, workspace := filepath.Join(root, "journal"), filepath.Join(root, "workspace")
	for _, path := range []string{journal, workspace} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(workspace, ".git"), []byte("gitdir: /host-only-unmounted-fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	b, err := NewSBXBackend(SBXBackendConfig{Enabled: true,
		ExecutablePath: os.Getenv("CYBERAGENT_SBX_LIVE_EXECUTABLE"), HelperExecutable: os.Getenv("CYBERAGENT_SBX_LIVE_HELPER"),
		TemplateReference: os.Getenv("CYBERAGENT_SBX_LIVE_TEMPLATE"), JournalRoot: journal})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := b.Close(); err != nil {
			t.Errorf("owned cleanup remains unresolved at %s: %v", journal, err)
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	if err := b.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	proof, err := b.Readiness(ctx)
	if err != nil || !proof.Ready {
		t.Fatalf("production readiness: %+v %v", proof, err)
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	name := "traverse-sbx-" + hex.EncodeToString(nonce[:])
	create := func(operation string) sbxRecord {
		record := sbxRecord{AppName: SBXAppName, Version: SBXPolicyVersion, OperationDigest: sbxDigest(operation), RequestFingerprint: strings.Repeat("a", 64), Name: name, Workspace: workspace, Phase: "reserved"}
		if err := b.save(record); err != nil {
			t.Fatal(err)
		}
		result, err := b.call(ctx, []string{"create", "--name", name, "--template", b.TemplateReference(), "--pull", "never", "--skills", "off", "--static-mcp", b.helperName, "--deny-network", "**", "--cpus", "2", "--memory", "2g", "shell", workspace, filepath.Join(workspace, ".git") + ":ro"}, nil, 64*1024)
		if err != nil || result.ExitCode != 0 {
			t.Fatalf("create result=%d err=%v", result.ExitCode, err)
		}
		entries, err := b.inventory(ctx)
		if err != nil {
			t.Fatal(err)
		}
		entry, found, _ := sbxFind(entries, name)
		if !found || !sbxUUID(entry.ID) || !sbxMountsMatch(entry, record) {
			t.Fatal("exact owned identity not returned")
		}
		record.ID, record.Phase = entry.ID, "created"
		if err := b.save(record); err != nil {
			t.Fatal(err)
		}
		return record
	}
	first := create("conditional-first-" + name)
	for _, credentialMode := range []string{"none", "apikey"} {
		guarded, err := b.call(ctx, []string{"exec", name, "/usr/bin/env", "SBX_CRED_OPENAI_MODE=" + credentialMode,
			"/bin/bash", "--noprofile", "--norc", "-p", "-c", sbxGuestEnvironmentGuard,
			"traverse-sbx-env", "/usr/bin/printf", "guard-dispatched"}, nil, 4096)
		if err != nil {
			t.Fatal(err)
		}
		if credentialMode == "none" && (guarded.ExitCode != 0 || string(guarded.Stdout) != "guard-dispatched") {
			t.Fatalf("empty credential modes were not admitted: exit=%d stdout=%q", guarded.ExitCode, guarded.Stdout)
		}
		if credentialMode == "apikey" && (guarded.ExitCode != 125 || len(guarded.Stdout) != 0) {
			t.Fatalf("active credential mode reached payload: exit=%d stdout=%q", guarded.ExitCode, guarded.Stdout)
		}
	}
	wrongID := "00000000-0000-4000-8000-000000000001"
	if wrongID == first.ID {
		t.Fatal("unexpected random UUID collision")
	}
	if err := b.daemon.Remove(ctx, name, wrongID); !errors.Is(err, ErrSBXOwnership) {
		t.Fatalf("wrong UUID removal=%v", err)
	}
	entries, err := b.inventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	entry, found, _ := sbxFind(entries, name)
	if !found || entry.ID != first.ID {
		t.Fatal("wrong UUID request changed the owned VM")
	}
	if err := b.removeOwned(ctx, &first); err != nil {
		t.Fatal(err)
	}
	second := create("conditional-second-" + name)
	if second.ID == first.ID {
		t.Fatal("same-name recreation reused immutable identity")
	}
	if err := b.daemon.Remove(ctx, name, first.ID); !errors.Is(err, ErrSBXOwnership) {
		t.Fatalf("old UUID removal=%v", err)
	}
	entries, err = b.inventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	entry, found, _ = sbxFind(entries, name)
	if !found || entry.ID != second.ID {
		t.Fatal("old UUID request changed the replacement VM")
	}
	if err := b.removeOwned(ctx, &second); err != nil {
		t.Fatal(err)
	}
	if err := b.RecoverStartup(ctx); err != nil {
		t.Fatal(err)
	}
	data, _ := json.MarshalIndent(map[string]any{"first_id": first.ID, "replacement_id": second.ID,
		"wrong_uuid_refused": true, "old_uuid_refused_after_recreation": true, "both_owned_removals_confirmed": first.Removed && second.Removed}, "", "  ")
	if err := os.WriteFile(filepath.Join(root, "result.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("conditional deletion/recreation evidence: %s", root)
}
