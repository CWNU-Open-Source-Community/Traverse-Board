//go:build darwin && cgo && keychainintegration

package credential

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Run on a disposable macOS runner:
// TRAVERSE_TEST_KEYCHAIN=1 go test -tags=keychainintegration -count=1 -timeout=90s ./internal/credential
// This test never opens the user's default keychain for item operations.
func TestDarwinKeychainIsolatedIntegration(t *testing.T) {
	if os.Getenv("TRAVERSE_TEST_KEYCHAIN") != "1" {
		t.Skip("real Keychain integration requires TRAVERSE_TEST_KEYCHAIN=1 on a disposable macOS runner")
	}
	original, err := snapshotDarwinTestKeychains()
	if err != nil {
		t.Fatalf("snapshot keychain configuration: %v", err)
	}
	assertUnchanged := func() {
		t.Helper()
		unchanged, err := original.unchanged()
		if err != nil || !unchanged {
			t.Errorf("default keychain or search list changed: %v", err)
		}
	}
	// Registered first so this runs after all fixture deletion and temp cleanup.
	t.Cleanup(func() {
		defer original.release()
		assertUnchanged()
	})
	newStore := func() darwinStore {
		t.Helper()
		passwordBytes := make([]byte, 32)
		if _, err := rand.Read(passwordBytes); err != nil {
			t.Fatal("generate isolated keychain password")
		}
		// Keychain passwords require canonical UTF-8. Encode only in memory.
		password := []byte(hex.EncodeToString(passwordBytes))
		clear(passwordBytes)
		defer clear(password)
		// Never call this login.keychain or System.keychain: those are special
		// creation cases that can alter the user's search list.
		path := filepath.Join(t.TempDir(), "traverse-private-test.keychain")
		cleanup, err := createDarwinTestKeychain(path, password)
		if err != nil {
			t.Fatalf("create isolated keychain: %v", err)
		}
		t.Cleanup(func() {
			if err := cleanup(); err != nil {
				t.Errorf("delete isolated keychain: %v", err)
			}
		})
		assertUnchanged()
		return darwinStore{keychainPath: path, service: "workbench.prayu.desktop.credentials.test"}
	}
	s := newStore()
	otherKeychain := newStore()
	otherService := s
	otherService.service += ".other"
	ctx := context.Background()
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		t.Fatal("generate synthetic credential")
	}
	secret := "test-" + hex.EncodeToString(random)
	clear(random)
	if value, found, err := s.Get(ctx, "openai"); value != "" || found || err != nil {
		t.Fatal("fresh keychain did not report a missing item")
	}
	if configured, err := s.Configured(ctx, "openai"); configured || err != nil {
		t.Fatal("fresh keychain incorrectly reported configured")
	}
	for _, fixture := range []struct {
		store darwinStore
		name  string
	}{
		{s, "openai"}, {s, "other-account"}, {otherService, "openai"}, {otherKeychain, "openai"},
	} {
		if err := fixture.store.Put(ctx, fixture.name, secret); err != nil {
			t.Fatalf("save isolated credential: %v", err)
		}
	}
	assertValue := func(store darwinStore, name, expected string) {
		t.Helper()
		value, found, err := store.Get(ctx, name)
		if err != nil || !found || value != expected {
			t.Fatalf("isolated credential read failed or differed: %v", err)
		}
		configured, err := store.Configured(ctx, name)
		if err != nil || !configured {
			t.Fatalf("isolated credential status failed: %v", err)
		}
	}
	assertValue(s, "openai", secret)
	replacement := secret + "-replacement"
	if err := s.Put(ctx, "openai", replacement); err != nil {
		t.Fatalf("replace isolated credential: %v", err)
	}
	// A separate store resolves the target again, exercising persistent readback.
	reopened := darwinStore{keychainPath: s.keychainPath, service: s.service}
	assertValue(reopened, "openai", replacement)
	assertValue(s, "other-account", secret)
	assertValue(otherService, "openai", secret)
	assertValue(otherKeychain, "openai", secret)
	if err := s.Put(ctx, "max-size", strings.Repeat("a", MaxSecretBytes)); err != nil {
		t.Fatalf("save maximum supported size: %v", err)
	}
	assertValue(s, "max-size", strings.Repeat("a", MaxSecretBytes))
	for i := 0; i < 2; i++ {
		if err := s.Delete(ctx, "openai"); err != nil {
			t.Fatalf("delete isolated credential idempotently: %v", err)
		}
	}
	if value, found, err := reopened.Get(ctx, "openai"); value != "" || found || err != nil {
		t.Fatal("deleted credential remained visible")
	}
	if configured, err := reopened.Configured(ctx, "openai"); configured || err != nil {
		t.Fatal("deleted credential remained configured")
	}
	assertValue(s, "other-account", secret)
	assertValue(otherService, "openai", secret)
	assertValue(otherKeychain, "openai", secret)
	for _, fixture := range []struct {
		name  string
		bytes []byte
	}{
		{"invalid-empty", nil},
		{"invalid-utf8", []byte{0xff}},
		{"invalid-space", []byte("embedded space")},
		{"invalid-size", []byte(strings.Repeat("x", MaxSecretBytes+1))},
	} {
		if err := writeDarwinTestRaw(s, fixture.name, fixture.bytes); err != nil {
			t.Fatalf("create corrupt isolated credential fixture: %v", err)
		}
		if value, found, err := s.Get(ctx, fixture.name); value != "" || found || err == nil {
			t.Fatal("invalid stored credential was returned or treated as absent")
		}
		if configured, err := s.Configured(ctx, fixture.name); configured || err == nil {
			t.Fatal("invalid stored credential was reported configured or absent")
		}
	}
	assertUnchanged()
}
