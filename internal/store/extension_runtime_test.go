package store

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"cyberagent-workbench/internal/apperror"
	"cyberagent-workbench/internal/credential"
	"cyberagent-workbench/internal/hooks"
	"cyberagent-workbench/internal/mcp"
	"cyberagent-workbench/internal/plugins"
)

func TestExtensionRuntimeSurvivesRestartAndFencesConcurrentReview(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "extension-runtime.db")
	st, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	workspace := WorkspaceRecord{ID: "workspace-extension-runtime", Name: "extensions",
		RootPath: t.TempDir(), CreatedAt: time.Now().UTC()}
	if err := st.SaveWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	manager, err := mcp.NewClientManager(st, credential.NewMemoryStore(), mcp.ManagerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	descriptor := mcp.ServerDescriptor{ProtocolVersion: mcp.ClientProtocolVersion,
		ID: "restart-mcp", Name: "Restart MCP", Transport: mcp.TransportStreamableHTTP,
		Target: "https://mcp.invalid/v1", CredentialRef: "restart-mcp-token",
		DeclaredCapabilities: []mcp.CapabilityKind{mcp.CapabilityTools},
		Scope:                mcp.ScopeWorkspace, WorkspaceID: workspace.ID,
		Source:            mcp.Source{Kind: "manual", URI: "operator"},
		CallTimeoutMillis: 1_000, MaxResultBytes: 4_096}
	stagedServer, replayed, err := manager.Stage(ctx, descriptor)
	if err != nil || replayed {
		t.Fatalf("stage MCP replayed=%t err=%v", replayed, err)
	}

	manifest := plugins.Manifest{ProtocolVersion: plugins.ProtocolVersion,
		ID: "restart-plugin", Name: "Restart Plugin", Version: "1.0.0",
		Publisher: "fixture.publisher", Description: "Durable restricted Hook fixture.",
		Capabilities: []plugins.Capability{plugins.CapabilityHooks},
		Files:        []plugins.FileEntry{}, Hooks: []hooks.Declaration{{
			ProtocolVersion: hooks.ProtocolVersion, ID: "record-run-start",
			Event: hooks.RunStarted, Action: hooks.ActionRecord,
			FailurePolicy: hooks.FailureContinue, TimeoutMillis: 100,
		}}}
	archive, err := plugins.BuildUnsignedPackage(manifest, nil)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(archive)
	service, err := plugins.NewService(st)
	if err != nil {
		t.Fatal(err)
	}
	stagedPlugin, replayed, err := service.Stage(ctx, archive, plugins.InstallSource{
		Kind: "local_file", URI: filepath.Join(t.TempDir(), "restart-plugin.zip"),
		SHA256: hex.EncodeToString(digest[:]),
	}, "", "restart-test-operator")
	if err != nil || replayed {
		t.Fatalf("stage Plugin replayed=%t err=%v", replayed, err)
	}
	approved, err := service.Review(ctx, stagedPlugin.ID, plugins.ReviewRequest{
		Action: plugins.ReviewApprove, ExpectedPackageFingerprint: stagedPlugin.PackageFingerprint,
		ExpectedGeneration: stagedPlugin.Generation, ConfirmUntrusted: true,
		ReviewedBy: "restart-test-reviewer",
	})
	if err != nil {
		t.Fatal(err)
	}
	enabled, err := service.Review(ctx, stagedPlugin.ID, plugins.ReviewRequest{
		Action: plugins.ReviewEnable, ExpectedPackageFingerprint: approved.PackageFingerprint,
		ExpectedGeneration: approved.Generation,
		Capabilities:       []plugins.Capability{plugins.CapabilityHooks},
		ConfirmUntrusted:   true, ReviewedBy: "restart-test-reviewer",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	recoveredServer, err := reopened.GetMCPClientServer(ctx, stagedServer.Descriptor.ID)
	if err != nil || recoveredServer.DescriptorFingerprint != stagedServer.DescriptorFingerprint ||
		recoveredServer.State != mcp.TrustStaged {
		t.Fatalf("MCP restart state is invalid: %#v err=%v", recoveredServer, err)
	}
	recoveredService, err := plugins.NewService(reopened)
	if err != nil {
		t.Fatal(err)
	}
	activeHooks, err := recoveredService.ActiveHooks(ctx)
	if err != nil || len(activeHooks) != 1 ||
		activeHooks[0].PluginFingerprint != enabled.PackageFingerprint {
		t.Fatalf("Plugin Hook restart state is invalid: %#v err=%v", activeHooks, err)
	}

	request := plugins.ReviewRequest{Action: plugins.ReviewDisable,
		ExpectedPackageFingerprint: enabled.PackageFingerprint,
		ExpectedGeneration:         enabled.Generation, ReviewedBy: "concurrent-reviewer"}
	start := make(chan struct{})
	errorsFound := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for range 2 {
		go func() {
			ready.Done()
			<-start
			_, reviewErr := recoveredService.Review(ctx, enabled.ID, request)
			errorsFound <- reviewErr
		}()
	}
	ready.Wait()
	close(start)
	successes, conflicts := 0, 0
	for range 2 {
		reviewErr := <-errorsFound
		if reviewErr == nil {
			successes++
		} else if apperror.CodeOf(apperror.Normalize(reviewErr)) == apperror.CodeConflict {
			conflicts++
		} else {
			t.Fatalf("unexpected concurrent review error: %v", reviewErr)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent reviews successes=%d conflicts=%d", successes, conflicts)
	}
	current, err := reopened.GetPluginInstallation(ctx, enabled.ID)
	if err != nil || current.State != plugins.StateDisabled || current.Generation != enabled.Generation+1 {
		t.Fatalf("concurrent review result is invalid: %#v err=%v", current, err)
	}
	if _, err := reopened.db.ExecContext(ctx,
		`DELETE FROM plugin_installations WHERE id = ?`, enabled.ID); err == nil {
		t.Fatal("durable Plugin installation could be deleted")
	}
	if _, err := reopened.db.ExecContext(ctx,
		`DELETE FROM mcp_client_servers WHERE id = ?`, stagedServer.Descriptor.ID); err == nil {
		t.Fatal("durable MCP server could be deleted")
	}
}

func TestExtensionRuntimeSchemaHasNoPlaintextCredentialOrCallPayloadColumns(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "extension-schema.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	for _, table := range []string{"mcp_client_servers", "mcp_client_calls",
		"plugin_installations", "plugin_installation_transitions", "plugin_hook_audits"} {
		rows, err := st.db.Query(`PRAGMA table_info(` + table + `)`)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var ordinal int
			var name, dataType string
			var notNull, primaryKey int
			var defaultValue any
			if err := rows.Scan(&ordinal, &name, &dataType, &notNull, &defaultValue,
				&primaryKey); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			switch name {
			case "credential", "secret", "authorization", "arguments", "result",
				"request_body", "response_body":
				rows.Close()
				t.Fatalf("%s unexpectedly persists plaintext %s", table, name)
			}
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPluginUpgradeRollbackAndPublisherRevocationAreFailClosed(t *testing.T) {
	ctx := t.Context()
	st, err := Open(filepath.Join(t.TempDir(), "plugin-state-machine.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	service, err := plugins.NewService(st)
	if err != nil {
		t.Fatal(err)
	}

	first := stageHookPluginFixture(t, ctx, service, "upgrade-plugin", "1.0.0", "", nil)
	first = reviewHookPluginFixture(t, ctx, service, first, plugins.ReviewApprove, true)
	first = reviewHookPluginFixture(t, ctx, service, first, plugins.ReviewEnable, true)
	second := stageHookPluginFixture(t, ctx, service, "upgrade-plugin", "2.0.0", first.ID, nil)
	second = reviewHookPluginFixture(t, ctx, service, second, plugins.ReviewApprove, true)
	second = reviewHookPluginFixture(t, ctx, service, second, plugins.ReviewEnable, true)
	retired, err := st.GetPluginInstallation(ctx, first.ID)
	if err != nil || retired.State != plugins.StateRolledBack ||
		len(retired.EnabledCapabilities) != 0 {
		t.Fatalf("upgrade did not atomically retire predecessor: %#v err=%v", retired, err)
	}
	activeHooks, err := service.ActiveHooks(ctx)
	if err != nil || len(activeHooks) != 1 ||
		activeHooks[0].PluginFingerprint != second.PackageFingerprint {
		t.Fatalf("upgrade exposed more than one active Hook version: %#v err=%v", activeHooks, err)
	}

	unrelated := stageHookPluginFixture(t, ctx, service, "upgrade-plugin", "3.0.0", "", nil)
	unrelated = reviewHookPluginFixture(t, ctx, service, unrelated, plugins.ReviewApprove, true)
	if _, err := service.Review(ctx, unrelated.ID, plugins.ReviewRequest{
		Action: plugins.ReviewEnable, ExpectedPackageFingerprint: unrelated.PackageFingerprint,
		ExpectedGeneration: unrelated.Generation, Capabilities: []plugins.Capability{plugins.CapabilityHooks},
		ConfirmUntrusted: true, ReviewedBy: "state-machine-reviewer",
	}); apperror.CodeOf(apperror.Normalize(err)) != apperror.CodeConflict {
		t.Fatalf("unrelated version replaced the active plugin without predecessor binding: %v", err)
	}

	rolledBack, restored, err := service.Rollback(ctx, second.ID, first.ID,
		plugins.RollbackRequest{ExpectedCurrentFingerprint: second.PackageFingerprint,
			ExpectedCurrentGeneration: second.Generation,
			ExpectedTargetFingerprint: retired.PackageFingerprint,
			ExpectedTargetGeneration:  retired.Generation,
			Capabilities:              []plugins.Capability{plugins.CapabilityHooks},
			ConfirmUntrusted:          true, ReviewedBy: "rollback-reviewer"})
	if err != nil || rolledBack.State != plugins.StateRolledBack ||
		restored.State != plugins.StateEnabled {
		t.Fatalf("atomic plugin rollback failed: current=%#v target=%#v err=%v",
			rolledBack, restored, err)
	}

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signed := stageHookPluginFixture(t, ctx, service, "publisher-plugin", "1.0.0", "", privateKey)
	trust, err := service.TrustPublisher(ctx, signed.ID, "publisher-reviewer")
	if err != nil {
		t.Fatal(err)
	}
	signed = reviewHookPluginFixture(t, ctx, service, signed, plugins.ReviewApprove, false)
	signed = reviewHookPluginFixture(t, ctx, service, signed, plugins.ReviewEnable, false)
	if _, err := service.RevokePublisher(ctx, trust.Fingerprint, trust.Generation,
		"publisher-revoker"); err != nil {
		t.Fatal(err)
	}
	revoked, err := st.GetPluginInstallation(ctx, signed.ID)
	if err != nil || revoked.State != plugins.StateRevoked {
		t.Fatalf("publisher revocation did not revoke enabled package: %#v err=%v", revoked, err)
	}
	signedNext := stageHookPluginFixture(t, ctx, service, "publisher-plugin", "2.0.0", "", privateKey)
	if _, err := service.Review(ctx, signedNext.ID, plugins.ReviewRequest{
		Action: plugins.ReviewApprove, ExpectedPackageFingerprint: signedNext.PackageFingerprint,
		ExpectedGeneration: signedNext.Generation, ConfirmUntrusted: true,
		ReviewedBy: "publisher-reviewer",
	}); apperror.CodeOf(apperror.Normalize(err)) != apperror.CodePolicyDenied {
		t.Fatalf("confirm-untrusted bypassed an explicitly revoked publisher: %v", err)
	}
	if _, err := service.TrustPublisher(ctx, signedNext.ID, "publisher-retrust-reviewer"); err != nil {
		t.Fatalf("explicit publisher re-trust failed: %v", err)
	}
	_ = reviewHookPluginFixture(t, ctx, service, signedNext, plugins.ReviewApprove, false)
}

func stageHookPluginFixture(t *testing.T, ctx context.Context, service *plugins.Service,
	pluginID, version, supersedes string, privateKey ed25519.PrivateKey,
) plugins.Installation {
	t.Helper()
	manifest := plugins.Manifest{ProtocolVersion: plugins.ProtocolVersion,
		ID: pluginID, Name: "State Machine Plugin", Version: version,
		Publisher: "state-machine.publisher", Description: "Restricted Hook state machine fixture.",
		Capabilities: []plugins.Capability{plugins.CapabilityHooks}, Files: []plugins.FileEntry{},
		Hooks: []hooks.Declaration{{ProtocolVersion: hooks.ProtocolVersion,
			ID: "record-run-start", Event: hooks.RunStarted, Action: hooks.ActionRecord,
			FailurePolicy: hooks.FailureContinue, TimeoutMillis: 100}}}
	var raw []byte
	var err error
	if len(privateKey) == ed25519.PrivateKeySize {
		raw, err = plugins.SignPackage(manifest, nil, privateKey, time.Now().UTC())
	} else {
		raw, err = plugins.BuildUnsignedPackage(manifest, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	installation, replayed, err := service.Stage(ctx, raw, plugins.InstallSource{
		Kind: "local_file", URI: filepath.Join(t.TempDir(), pluginID+"-"+version+".zip"),
		SHA256: hex.EncodeToString(digest[:]),
	}, supersedes, "state-machine-stager")
	if err != nil || replayed {
		t.Fatalf("stage plugin %s replayed=%t err=%v", version, replayed, err)
	}
	return installation
}

func reviewHookPluginFixture(t *testing.T, ctx context.Context, service *plugins.Service,
	installation plugins.Installation, action plugins.ReviewAction, confirmUntrusted bool,
) plugins.Installation {
	t.Helper()
	capabilities := []plugins.Capability(nil)
	if action == plugins.ReviewEnable {
		capabilities = []plugins.Capability{plugins.CapabilityHooks}
	}
	updated, err := service.Review(ctx, installation.ID, plugins.ReviewRequest{
		Action: action, ExpectedPackageFingerprint: installation.PackageFingerprint,
		ExpectedGeneration: installation.Generation, Capabilities: capabilities,
		ConfirmUntrusted: confirmUntrusted, ReviewedBy: "state-machine-reviewer"})
	if err != nil {
		t.Fatalf("review plugin %s action=%s: %v", installation.Manifest.Version, action, err)
	}
	return updated
}
